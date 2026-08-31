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

const accountSummaryColumns = `
		a.id, a.name, a.role, a.plan, a.status, a.created_at, a.updated_at,
		COUNT(k.id) AS key_count`

func (r *PostgresRepository) ListAccounts(ctx context.Context) ([]AccountSummary, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT`+accountSummaryColumns+`
		FROM accounts a
		LEFT JOIN api_keys k ON k.account_id = a.id
		GROUP BY a.id
		ORDER BY a.id`)
	if err != nil {
		return nil, fmt.Errorf("auth: list accounts: %w", err)
	}
	defer rows.Close()

	accounts := make([]AccountSummary, 0)
	for rows.Next() {
		var a AccountSummary
		if err := rows.Scan(
			&a.ID, &a.Name, &a.Role, &a.Plan, &a.Status, &a.CreatedAt, &a.UpdatedAt, &a.KeyCount,
		); err != nil {
			return nil, fmt.Errorf("auth: scan account: %w", err)
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: iterate accounts: %w", err)
	}
	return accounts, nil
}

func (r *PostgresRepository) FindAccountSummary(ctx context.Context, id int64) (*AccountSummary, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT`+accountSummaryColumns+`
		FROM accounts a
		LEFT JOIN api_keys k ON k.account_id = a.id
		WHERE a.id = $1
		GROUP BY a.id`, id)

	var a AccountSummary
	err := row.Scan(
		&a.ID, &a.Name, &a.Role, &a.Plan, &a.Status, &a.CreatedAt, &a.UpdatedAt, &a.KeyCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: find account summary: %w", err)
	}
	return &a, nil
}

func (r *PostgresRepository) UpdateAccount(ctx context.Context, id int64, changes AccountChanges) (*Account, error) {
	var name, role, plan, status *string
	if changes.Name != nil {
		name = changes.Name
	}
	if changes.Role != nil {
		s := string(*changes.Role)
		role = &s
	}
	if changes.Plan != nil {
		s := string(*changes.Plan)
		plan = &s
	}
	if changes.Status != nil {
		s := string(*changes.Status)
		status = &s
	}

	row := r.pool.QueryRow(ctx, `
		UPDATE accounts SET
			name       = COALESCE($2, name),
			role       = COALESCE($3, role),
			plan       = COALESCE($4, plan),
			status     = COALESCE($5, status),
			updated_at = now()
		WHERE id = $1
		RETURNING id, name, role, plan, status, created_at, updated_at`,
		id, name, role, plan, status,
	)

	var a Account
	err := row.Scan(&a.ID, &a.Name, &a.Role, &a.Plan, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: update account: %w", err)
	}
	return &a, nil
}

func (r *PostgresRepository) DisableAccount(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE accounts SET status = 'disabled', updated_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("auth: disable account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAccountNotFound
	}
	return nil
}

const apiKeySummaryColumns = `
		k.id, k.account_id, k.name, k.key_hash, k.status, k.created_at, k.last_used_at, k.expires_at, a.name`

func (r *PostgresRepository) ListAllKeys(ctx context.Context) ([]APIKeySummary, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT`+apiKeySummaryColumns+`
		FROM api_keys k
		JOIN accounts a ON a.id = k.account_id
		ORDER BY k.id`)
	if err != nil {
		return nil, fmt.Errorf("auth: list all keys: %w", err)
	}
	defer rows.Close()

	keys := make([]APIKeySummary, 0)
	for rows.Next() {
		var k APIKeySummary
		if err := rows.Scan(
			&k.ID, &k.AccountID, &k.Name, &k.KeyHash, &k.Status,
			&k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.AccountName,
		); err != nil {
			return nil, fmt.Errorf("auth: scan key summary: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: iterate keys: %w", err)
	}
	return keys, nil
}

func (r *PostgresRepository) FindKeyByID(ctx context.Context, id int64) (*APIKeySummary, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT`+apiKeySummaryColumns+`
		FROM api_keys k
		JOIN accounts a ON a.id = k.account_id
		WHERE k.id = $1`, id)

	var k APIKeySummary
	err := row.Scan(
		&k.ID, &k.AccountID, &k.Name, &k.KeyHash, &k.Status,
		&k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.AccountName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: find key by id: %w", err)
	}
	return &k, nil
}

func (r *PostgresRepository) UpdateKey(ctx context.Context, id int64, changes KeyChanges) (*APIKeySummary, error) {
	var name, status *string
	if changes.Name != nil {
		name = changes.Name
	}
	if changes.Status != nil {
		s := string(*changes.Status)
		status = &s
	}

	row := r.pool.QueryRow(ctx, `
		WITH updated AS (
			UPDATE api_keys SET
				name   = COALESCE($2, name),
				status = COALESCE($3, status)
			WHERE id = $1
			RETURNING id, account_id, name, key_hash, status, created_at, last_used_at, expires_at
		)
		SELECT`+apiKeySummaryColumns+`
		FROM updated k
		JOIN accounts a ON a.id = k.account_id`,
		id, name, status,
	)

	var k APIKeySummary
	err := row.Scan(
		&k.ID, &k.AccountID, &k.Name, &k.KeyHash, &k.Status,
		&k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.AccountName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: update key: %w", err)
	}
	return &k, nil
}

func (r *PostgresRepository) RevokeKeyByID(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE api_keys SET status = 'revoked' WHERE id = $1 AND status = 'active'`, id)
	if err != nil {
		return fmt.Errorf("auth: revoke key by id: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	var status string
	err = r.pool.QueryRow(ctx, `SELECT status FROM api_keys WHERE id = $1`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrKeyNotFound
	}
	if err != nil {
		return fmt.Errorf("auth: check key on revoke by id: %w", err)
	}
	return nil
}

func (r *PostgresRepository) CountAccounts(ctx context.Context) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("auth: count accounts: %w", err)
	}
	return count, nil
}

func (r *PostgresRepository) CountKeysByStatus(ctx context.Context) (map[KeyStatus]int64, error) {
	rows, err := r.pool.Query(ctx, `SELECT status, COUNT(*) FROM api_keys GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("auth: count keys by status: %w", err)
	}
	defer rows.Close()

	counts := make(map[KeyStatus]int64)
	for rows.Next() {
		var status KeyStatus
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("auth: scan key count: %w", err)
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: iterate key counts: %w", err)
	}
	return counts, nil
}
