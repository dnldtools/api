package quota

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"rest-api/internal/plans"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Increment(ctx context.Context, accountID int64, plan plans.Plan, success bool, dailyStart, monthlyStart time.Time) error {
	successFlag, failedFlag := 0, 0
	if success {
		successFlag = 1
	} else {
		failedFlag = 1
	}

	_, err := r.pool.Exec(ctx, `
		INSERT INTO quota_usage
			(account_id, plan, period_type, period_start, total_requests, success_count, failed_count)
		VALUES
			($1, $2, 'daily',   $3, 1, $5, $6),
			($1, $2, 'monthly', $4, 1, $5, $6)
		ON CONFLICT (account_id, plan, period_type, period_start) DO UPDATE SET
			total_requests = quota_usage.total_requests + 1,
			success_count  = quota_usage.success_count  + EXCLUDED.success_count,
			failed_count   = quota_usage.failed_count   + EXCLUDED.failed_count,
			updated_at     = now()`,
		accountID, string(plan), dailyStart, monthlyStart, successFlag, failedFlag,
	)
	if err != nil {
		return fmt.Errorf("quota: increment usage: %w", err)
	}
	return nil
}

func (r *PostgresRepository) Usage(ctx context.Context, accountID int64, dailyStart, monthlyStart time.Time) (Usage, error) {
	var u Usage
	err := r.pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(total_requests) FILTER (WHERE period_type = 'daily'),   0),
			COALESCE(SUM(success_count)  FILTER (WHERE period_type = 'daily'),   0),
			COALESCE(SUM(failed_count)   FILTER (WHERE period_type = 'daily'),   0),
			COALESCE(SUM(total_requests) FILTER (WHERE period_type = 'monthly'), 0),
			COALESCE(SUM(success_count)  FILTER (WHERE period_type = 'monthly'), 0),
			COALESCE(SUM(failed_count)   FILTER (WHERE period_type = 'monthly'), 0)
		FROM quota_usage
		WHERE account_id = $1
		  AND period_start IN ($2, $3)`,
		accountID, dailyStart, monthlyStart,
	).Scan(
		&u.DailyTotal, &u.DailySuccess, &u.DailyFailed,
		&u.MonthlyTotal, &u.MonthlySuccess, &u.MonthlyFailed,
	)
	if err != nil {
		return Usage{}, fmt.Errorf("quota: read usage: %w", err)
	}
	return u, nil
}
