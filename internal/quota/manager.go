package quota

import (
	"context"
	"log/slog"
	"time"

	"rest-api/internal/metrics"
	"rest-api/internal/plans"
)

type Manager struct {
	repo     Repository
	counter  Counter
	policies plans.PolicySet
	logger   *slog.Logger
	now      func() time.Time
}

func NewManager(repo Repository, counter Counter, policies plans.PolicySet, logger *slog.Logger, now func() time.Time) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	if policies == nil {
		policies = plans.Defaults()
	}
	return &Manager{repo: repo, counter: counter, policies: policies, logger: logger, now: now}
}

func (m *Manager) Check(ctx context.Context, accountID int64, plan plans.Plan) (Result, error) {
	policy := m.policies.Get(plan)
	dailyStart, monthlyStart := PeriodStarts(m.now())

	usage, err := m.repo.Usage(ctx, accountID, dailyStart, monthlyStart)
	if err != nil {
		return Result{}, err
	}

	dailyRemaining := remaining(policy.DailyQuota, usage.DailyTotal)
	monthlyRemaining := remaining(policy.MonthlyQuota, usage.MonthlyTotal)

	dailyExceeded := policy.DailyQuota > 0 && usage.DailyTotal >= policy.DailyQuota
	monthlyExceeded := policy.MonthlyQuota > 0 && usage.MonthlyTotal >= policy.MonthlyQuota

	return Result{
		Allowed:          !dailyExceeded && !monthlyExceeded,
		DailyRemaining:   dailyRemaining,
		MonthlyRemaining: monthlyRemaining,
	}, nil
}

func (m *Manager) Usage(ctx context.Context, accountID int64, plan plans.Plan) (UsageReport, error) {
	policy := m.policies.Get(plan)
	dailyStart, monthlyStart := PeriodStarts(m.now())

	usage, err := m.repo.Usage(ctx, accountID, dailyStart, monthlyStart)
	if err != nil {
		return UsageReport{}, err
	}

	return UsageReport{
		Usage:            usage,
		DailyQuota:       policy.DailyQuota,
		MonthlyQuota:     policy.MonthlyQuota,
		DailyRemaining:   remaining(policy.DailyQuota, usage.DailyTotal),
		MonthlyRemaining: remaining(policy.MonthlyQuota, usage.MonthlyTotal),
	}, nil
}

func (m *Manager) Record(ctx context.Context, accountID int64, plan plans.Plan, statusCode int) {
	success := metrics.IsSuccess(statusCode)
	dailyStart, monthlyStart := PeriodStarts(m.now())

	if m.repo != nil {
		if err := m.repo.Increment(ctx, accountID, plan, success, dailyStart, monthlyStart); err != nil {
			m.logger.Error("quota increment failed", "account_id", accountID, "error", err)
		}
	}
	if m.counter != nil {
		if err := m.counter.Increment(ctx, accountID, plan, dailyStart, monthlyStart); err != nil {
			m.logger.Warn("quota counter failed", "account_id", accountID, "error", err)
		}
	}
}

func remaining(quota, total int64) int64 {
	if quota <= 0 {
		return -1
	}
	r := quota - total
	if r < 0 {
		r = 0
	}
	return r
}
