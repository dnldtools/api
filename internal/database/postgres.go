package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	URL            string
	MaxConns       int32
	MinConns       int32
	ConnectTimeout time.Duration
}

func (c Config) Enabled() bool { return c.URL != "" }

func (c Config) Validate() error {
	if c.URL == "" {
		return fmt.Errorf("DATABASE_URL must not be empty")
	}
	if c.MaxConns <= 0 {
		return fmt.Errorf("DATABASE_MAX_CONNS must be positive")
	}
	if c.MinConns < 0 {
		return fmt.Errorf("DATABASE_MIN_CONNS must not be negative")
	}
	if c.ConnectTimeout <= 0 {
		return fmt.Errorf("DATABASE_CONNECT_TIMEOUT must be positive")
	}
	return nil
}

type DB struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, cfg Config) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &DB{pool: pool}, nil
}

func (db *DB) Pool() *pgxpool.Pool { return db.pool }

func (db *DB) Ping(ctx context.Context) error { return db.pool.Ping(ctx) }

func (db *DB) Close() {
	if db.pool != nil {
		db.pool.Close()
	}
}
