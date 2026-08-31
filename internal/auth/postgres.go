package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) FindKeyByHash(ctx context.Context, hash string) (*APIKey, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, name, key_hash, status, created_at, last_used_at, expires_at
		FROM api_keys
		WHERE key_hash = $1`, hash)

	var k APIKey
	err := row.Scan(
		&k.ID, &k.AccountID, &k.Name, &k.KeyHash, &k.Status,
		&k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: find key by hash: %w", err)
	}
	return &k, nil
}

func (r *PostgresRepository) FindAccount(ctx context.Context, id int64) (*Account, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, name, role, plan, status, created_at, updated_at
		FROM accounts
		WHERE id = $1`, id)

	var a Account
	err := row.Scan(&a.ID, &a.Name, &a.Role, &a.Plan, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: find account: %w", err)
	}
	return &a, nil
}

func (r *PostgresRepository) TouchKey(ctx context.Context, id int64, now time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE api_keys SET last_used_at = $2 WHERE id = $1`, id, now)
	if err != nil {
		return fmt.Errorf("auth: touch key: %w", err)
	}
	return nil
}

func (r *PostgresRepository) CreateAccount(ctx context.Context, name string, role Role, plan string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO accounts (name, role, plan) VALUES ($1, $2, $3) RETURNING id`,
		name, string(role), plan,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("auth: create account: %w", err)
	}
	return id, nil
}

func (r *PostgresRepository) CreateKey(ctx context.Context, accountID int64, name, keyHash string, expiresAt *time.Time) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO api_keys (account_id, name, key_hash, expires_at) VALUES ($1, $2, $3, $4) RETURNING id`,
		accountID, name, keyHash, expiresAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("auth: create key: %w", err)
	}
	return id, nil
}

func (r *PostgresRepository) ListKeys(ctx context.Context, accountID int64) ([]APIKey, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, account_id, name, key_hash, status, created_at, last_used_at, expires_at
		FROM api_keys
		WHERE account_id = $1
		ORDER BY id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("auth: list keys: %w", err)
	}
	defer rows.Close()

	keys := make([]APIKey, 0)
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(
			&k.ID, &k.AccountID, &k.Name, &k.KeyHash, &k.Status,
			&k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("auth: scan key: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: iterate keys: %w", err)
	}
	return keys, nil
}

func (r *PostgresRepository) RevokeKey(ctx context.Context, accountID, keyID int64) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE api_keys SET status = 'revoked'
		WHERE id = $1 AND account_id = $2 AND status = 'active'`, keyID, accountID)
	if err != nil {
		return fmt.Errorf("auth: revoke key: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	var status string
	err = r.pool.QueryRow(ctx, `
		SELECT status FROM api_keys WHERE id = $1 AND account_id = $2`, keyID, accountID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrKeyNotFound
	}
	if err != nil {
		return fmt.Errorf("auth: check key on revoke: %w", err)
	}
	return nil
}
