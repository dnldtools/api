package media

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Status string

const StatusReady Status = "ready"

var ErrNotFound = errors.New("media: not found")

type Record struct {
	ID          int64
	Platform    string
	SourceURL   string
	ObjectKey   string
	Bucket      string
	Account     string
	ContentType string
	Size        int64
	Status      Status
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Create(ctx context.Context, r *Record) error {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO media (platform, source_url, object_key, bucket, account, content_type, size, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (object_key) DO UPDATE
		SET size = EXCLUDED.size, content_type = EXCLUDED.content_type, status = EXCLUDED.status, updated_at = now()
		RETURNING id, created_at, updated_at`,
		r.Platform, r.SourceURL, r.ObjectKey, r.Bucket, r.Account, r.ContentType, r.Size, string(r.Status),
	)
	if err := row.Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return fmt.Errorf("media: create: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, id int64) (*Record, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, platform, source_url, object_key, bucket, account, content_type, size, status, created_at, updated_at
		FROM media
		WHERE id = $1`, id)

	var (
		r      Record
		status string
	)
	err := row.Scan(
		&r.ID, &r.Platform, &r.SourceURL, &r.ObjectKey, &r.Bucket, &r.Account,
		&r.ContentType, &r.Size, &status, &r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("media: get: %w", err)
	}
	r.Status = Status(status)
	return &r, nil
}
