package metrics

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

const insertMetricSQL = `
INSERT INTO api_metrics (
	request_id, status_code, success, failed,
	valid_api_key, invalid_api_key, api_key_missing, client_id, account_id,
	endpoint, platform, rate_limited, quota_exceeded, duration_ms, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

func (r *PostgresRepository) Insert(ctx context.Context, e Event) error {
	e.Normalize()
	_, err := r.pool.Exec(ctx, insertMetricSQL,
		e.RequestID,
		e.StatusCode,
		e.Success,
		e.Failed,
		e.ValidAPIKey,
		e.InvalidAPIKey,
		e.APIKeyMissing,
		nullableText(e.ClientID),
		nullableInt8(e.AccountID),
		e.Endpoint,
		nullableText(e.Platform),
		e.RateLimited,
		e.QuotaExceeded,
		e.DurationMilliseconds(),
		e.Timestamp,
	)
	return err
}

const aggregateSQL = `
SELECT
	COUNT(*)                                     AS total,
	COUNT(*) FILTER (WHERE success)              AS success,
	COUNT(*) FILTER (WHERE NOT success)          AS failed,
	COUNT(*) FILTER (WHERE valid_api_key)        AS valid_key,
	COUNT(*) FILTER (WHERE invalid_api_key)      AS invalid_key,
	COUNT(*) FILTER (WHERE api_key_missing)      AS missing_key,
	COUNT(*) FILTER (WHERE rate_limited)         AS rate_limited,
	COUNT(*) FILTER (WHERE quota_exceeded)       AS quota_exceeded,
	COALESCE(AVG(duration_ms), 0)::float8        AS avg_duration
FROM api_metrics
WHERE created_at >= $1 AND created_at < $2`

func (r *PostgresRepository) Aggregate(ctx context.Context, from, to time.Time) (Stats, error) {
	var (
		total, success, failed, valid, invalid, missing, rateLimited, quotaExceeded int64
		avg                                                                         float64
	)
	err := r.pool.QueryRow(ctx, aggregateSQL, from, to).
		Scan(&total, &success, &failed, &valid, &invalid, &missing, &rateLimited, &quotaExceeded, &avg)
	if err != nil {
		return Stats{}, err
	}

	return Stats{
		TotalRequests:      total,
		SuccessCount:       success,
		ErrorCount:         failed,
		SuccessRate:        SuccessRate(success, total),
		ValidAPIKeyCount:   valid,
		InvalidAPIKeyCount: invalid,
		MissingAPIKeyCount: missing,
		RateLimitedCount:   rateLimited,
		QuotaExceededCount: quotaExceeded,
		AvgDurationMs:      avg,
	}, nil
}

func nullableText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: s, Valid: true}
}

func nullableInt8(id int64) pgtype.Int8 {
	if id == 0 {
		return pgtype.Int8{Valid: false}
	}
	return pgtype.Int8{Int64: id, Valid: true}
}
