package testutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"rest-api/internal/cache"
	"rest-api/internal/database"
)

const (
	EnvTestDatabaseURL = "TEST_DATABASE_URL"

	EnvTestRedisAddr = "TEST_REDIS_ADDR"

	EnvTestRedisDB = "TEST_REDIS_DB"

	EnvTestRedisPassword = "TEST_REDIS_PASSWORD"
)

const DefaultTestRedisDB = 15

const migrationLockID = 0x72657374

func HasDatabase() bool { return os.Getenv(EnvTestDatabaseURL) != "" }

func HasRedis() bool { return os.Getenv(EnvTestRedisAddr) != "" }

func OpenTestDB(t testing.TB) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv(EnvTestDatabaseURL)
	if url == "" {
		t.Skipf("set %s to run PostgreSQL integration tests", EnvTestDatabaseURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvTestDatabaseURL, err)
	}
	dbName := cfg.ConnConfig.Database
	if dbName == "" {
		t.Fatalf("%s must include a database name", EnvTestDatabaseURL)
	}
	if !isTestDatabaseName(dbName) {
		t.Fatalf("refusing to use non-test database %q (name must contain \"test\")", dbName)
	}

	if err := ensureDatabase(ctx, cfg, dbName); err != nil {
		t.Fatalf("ensure test database %q: %v", dbName, err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open test database pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test database: %v", err)
	}

	if err := migrateExclusive(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("migrate test database: %v", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

func Truncate(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()

	name := pool.Config().ConnConfig.Database
	if !isTestDatabaseName(name) {
		t.Fatalf("refusing to truncate non-test database %q", name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := pool.Exec(ctx,
		`TRUNCATE api_metrics, quota_usage, api_keys, accounts RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate test tables: %v", err)
	}
}

func OpenTestRedis(t testing.TB) *cache.Redis {
	t.Helper()

	addr := os.Getenv(EnvTestRedisAddr)
	if addr == "" {
		t.Skipf("set %s to run Redis integration tests", EnvTestRedisAddr)
	}

	db := DefaultTestRedisDB
	if v := os.Getenv(EnvTestRedisDB); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("%s must be an integer: %v", EnvTestRedisDB, err)
		}
		db = n
	}

	cfg := cache.Config{
		Addr:     addr,
		Password: os.Getenv(EnvTestRedisPassword),
		DB:       db,
		PoolSize: 10,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("invalid redis test config: %v", err)
	}

	r, err := cache.New(cfg)
	if err != nil {
		t.Fatalf("build redis client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Ping(ctx); err != nil {
		_ = r.Close()
		t.Fatalf("ping redis (isolated DB %d): %v", db, err)
	}

	t.Cleanup(func() { _ = r.Close() })
	return r
}

func FlushRedis(t testing.TB, r *cache.Redis) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Client().FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush redis test DB: %v", err)
	}
}

func ensureDatabase(ctx context.Context, cfg *pgxpool.Config, dbName string) error {
	adminCfg := cfg.Copy()
	adminCfg.ConnConfig.Database = "postgres"

	admin, err := pgxpool.NewWithConfig(ctx, adminCfg)
	if err != nil {
		return fmt.Errorf("open maintenance database: %w", err)
	}
	defer admin.Close()

	if err := admin.Ping(ctx); err != nil {
		return fmt.Errorf("ping maintenance database: %w", err)
	}

	_, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize())
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P04" {
			return nil
		}
		return fmt.Errorf("create database: %w", err)
	}
	return nil
}

func migrateExclusive(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for migration lock: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockID) }()

	return database.Migrate(ctx, pool)
}

func isTestDatabaseName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "test")
}
