package quota

import (
	"context"
	"time"

	"rest-api/internal/plans"
)

type Usage struct {
	DailyTotal     int64 `json:"daily_total"`
	DailySuccess   int64 `json:"daily_success"`
	DailyFailed    int64 `json:"daily_failed"`
	MonthlyTotal   int64 `json:"monthly_total"`
	MonthlySuccess int64 `json:"monthly_success"`
	MonthlyFailed  int64 `json:"monthly_failed"`
}

type UsageReport struct {
	Usage

	DailyQuota       int64 `json:"daily_quota"`
	MonthlyQuota     int64 `json:"monthly_quota"`
	DailyRemaining   int64 `json:"daily_remaining"`
	MonthlyRemaining int64 `json:"monthly_remaining"`
}

type Result struct {
	Allowed bool

	DailyRemaining int64

	MonthlyRemaining int64
}

type Repository interface {
	Increment(ctx context.Context, accountID int64, plan plans.Plan, success bool, dailyStart, monthlyStart time.Time) error

	Usage(ctx context.Context, accountID int64, dailyStart, monthlyStart time.Time) (Usage, error)

	TotalUsage(ctx context.Context, dailyStart, monthlyStart time.Time) (Usage, error)
}

type Counter interface {
	Increment(ctx context.Context, accountID int64, plan plans.Plan, dailyStart, monthlyStart time.Time) error
}

type Service interface {
	Check(ctx context.Context, accountID int64, plan plans.Plan) (Result, error)

	Record(ctx context.Context, accountID int64, plan plans.Plan, statusCode int)

	Usage(ctx context.Context, accountID int64, plan plans.Plan) (UsageReport, error)

	TotalUsage(ctx context.Context) (Usage, error)
}

func PeriodStarts(now time.Time) (daily, monthly time.Time) {
	now = now.UTC()
	daily = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthly = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return daily, monthly
}
