package plans

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func LoadFromDB(ctx context.Context, pool *pgxpool.Pool) (PolicySet, error) {
	set := Defaults()
	if pool == nil {
		return set, nil
	}

	rows, err := pool.Query(ctx, `
		SELECT plan, rate_limit, rate_window, daily_quota, monthly_quota
		FROM plan_policies`)
	if err != nil {
		return nil, fmt.Errorf("plans: query plan_policies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			planName   string
			rateLimit  int
			rateWindow string
			daily      int64
			monthly    int64
		)
		if err := rows.Scan(&planName, &rateLimit, &rateWindow, &daily, &monthly); err != nil {
			return nil, fmt.Errorf("plans: scan plan_policies: %w", err)
		}

		window, err := time.ParseDuration(rateWindow)
		if err != nil {
			return nil, fmt.Errorf("plans: plan %q has invalid rate_window %q: %w", planName, rateWindow, err)
		}

		set[Plan(planName)] = Policy{
			RateLimit:    rateLimit,
			RateWindow:   window,
			DailyQuota:   daily,
			MonthlyQuota: monthly,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("plans: iterate plan_policies: %w", err)
	}

	if err := set.Validate(); err != nil {
		return nil, err
	}
	return set, nil
}
