package quota

import (
	"context"
	"testing"
	"time"

	"rest-api/internal/plans"
)

type fakeRepo struct {
	usage Usage
	err   error

	incremented []increment
}

type increment struct {
	accountID  int64
	plan       plans.Plan
	success    bool
	dailyStart time.Time
	monthly    time.Time
}

func (f *fakeRepo) Increment(_ context.Context, accountID int64, plan plans.Plan, success bool, dailyStart, monthlyStart time.Time) error {
	f.incremented = append(f.incremented, increment{
		accountID: accountID, plan: plan, success: success, dailyStart: dailyStart, monthly: monthlyStart,
	})
	return nil
}

func (f *fakeRepo) Usage(context.Context, int64, time.Time, time.Time) (Usage, error) {
	if f.err != nil {
		return Usage{}, f.err
	}
	return f.usage, nil
}

func (f *fakeRepo) TotalUsage(context.Context, time.Time, time.Time) (Usage, error) {
	if f.err != nil {
		return Usage{}, f.err
	}
	return f.usage, nil
}

type fakeCounter struct {
	calls int
}

func (f *fakeCounter) Increment(context.Context, int64, plans.Plan, time.Time, time.Time) error {
	f.calls++
	return nil
}

func testManager(repo Repository, policies plans.PolicySet) *Manager {
	return NewManager(repo, nil, policies, nil, func() time.Time {
		return time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	})
}

func TestCheckAllowed(t *testing.T) {
	m := testManager(&fakeRepo{usage: Usage{DailyTotal: 5, MonthlyTotal: 50}}, plans.Defaults())

	res, err := m.Check(context.Background(), 42, plans.PlanPro)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Allowed {
		t.Error("under quota should be allowed")
	}
	if res.DailyRemaining <= 0 || res.MonthlyRemaining <= 0 {
		t.Errorf("remaining should be positive, got daily=%d monthly=%d", res.DailyRemaining, res.MonthlyRemaining)
	}
}

func TestCheckDailyExceeded(t *testing.T) {
	policy := plans.Defaults()[plans.PlanTrial]
	m := testManager(&fakeRepo{usage: Usage{DailyTotal: policy.DailyQuota}}, plans.Defaults())

	res, err := m.Check(context.Background(), 42, plans.PlanTrial)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Allowed {
		t.Error("daily quota exhausted should deny")
	}
	if res.DailyRemaining != 0 {
		t.Errorf("daily remaining = %d, want 0", res.DailyRemaining)
	}
}

func TestCheckMonthlyExceeded(t *testing.T) {
	policy := plans.Defaults()[plans.PlanTrial]
	m := testManager(&fakeRepo{usage: Usage{MonthlyTotal: policy.MonthlyQuota}}, plans.Defaults())

	res, err := m.Check(context.Background(), 42, plans.PlanTrial)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Allowed {
		t.Error("monthly quota exhausted should deny")
	}
	if res.MonthlyRemaining != 0 {
		t.Errorf("monthly remaining = %d, want 0", res.MonthlyRemaining)
	}
}

func TestCheckUnlimitedQuota(t *testing.T) {
	set := plans.Defaults()
	p := set[plans.PlanPro]
	p.DailyQuota = 0
	p.MonthlyQuota = 0
	set[plans.PlanPro] = p

	m := testManager(&fakeRepo{usage: Usage{DailyTotal: 999999, MonthlyTotal: 999999}}, set)
	res, err := m.Check(context.Background(), 42, plans.PlanPro)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Allowed {
		t.Error("disabled quotas should allow unlimited usage")
	}
	if res.DailyRemaining != -1 || res.MonthlyRemaining != -1 {
		t.Errorf("remaining should be -1 for unlimited, got daily=%d monthly=%d", res.DailyRemaining, res.MonthlyRemaining)
	}
}

func TestRecordDerivesSuccessFromStatus(t *testing.T) {
	repo := &fakeRepo{}
	counter := &fakeCounter{}
	m := NewManager(repo, counter, plans.Defaults(), nil, func() time.Time {
		return time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	})

	m.Record(context.Background(), 42, plans.PlanFree, 200)
	m.Record(context.Background(), 42, plans.PlanFree, 500)

	if len(repo.incremented) != 2 {
		t.Fatalf("incremented %d times, want 2", len(repo.incremented))
	}
	if !repo.incremented[0].success {
		t.Error("status 200 should record success=true")
	}
	if repo.incremented[1].success {
		t.Error("status 500 should record success=false")
	}
	if repo.incremented[0].plan != plans.PlanFree {
		t.Errorf("plan = %q, want free", repo.incremented[0].plan)
	}
	if counter.calls != 2 {
		t.Errorf("counter calls = %d, want 2", counter.calls)
	}
}

func TestCheckRepoErrorPropagates(t *testing.T) {
	repo := &fakeRepo{err: &repoErr{}}
	m := testManager(repo, plans.Defaults())
	if _, err := m.Check(context.Background(), 42, plans.PlanPro); err == nil {
		t.Error("Check should propagate repository errors")
	}
}

func TestUsageReturnsReportWithQuotaAndRemaining(t *testing.T) {
	m := testManager(&fakeRepo{usage: Usage{DailyTotal: 10, MonthlyTotal: 100}}, plans.Defaults())

	report, err := m.Usage(context.Background(), 42, plans.PlanPro)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}

	policy := plans.Defaults()[plans.PlanPro]
	if report.DailyTotal != 10 {
		t.Errorf("daily total = %d, want 10", report.DailyTotal)
	}
	if report.MonthlyTotal != 100 {
		t.Errorf("monthly total = %d, want 100", report.MonthlyTotal)
	}
	if report.DailyQuota != policy.DailyQuota {
		t.Errorf("daily quota = %d, want %d", report.DailyQuota, policy.DailyQuota)
	}
	if report.DailyRemaining != policy.DailyQuota-10 {
		t.Errorf("daily remaining = %d, want %d", report.DailyRemaining, policy.DailyQuota-10)
	}
	if report.MonthlyRemaining != policy.MonthlyQuota-100 {
		t.Errorf("monthly remaining = %d, want %d", report.MonthlyRemaining, policy.MonthlyQuota-100)
	}
}

type repoErr struct{}

func (e *repoErr) Error() string { return "db boom" }
