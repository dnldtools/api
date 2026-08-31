package quota

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"rest-api/internal/plans"
	"rest-api/internal/testutil"
)

func seedAccount(t *testing.T, pool *pgxpool.Pool, id int64, name string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO accounts (id, name, role, plan) VALUES ($1, $2, 'user', 'trial') ON CONFLICT (id) DO NOTHING`,
		id, name)
	if err != nil {
		t.Fatalf("seed account %d: %v", id, err)
	}
}

func TestPostgresRepositoryIncrementAndUsage(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Truncate(t, pool)
	seedAccount(t, pool, 424242, "quota-int-1")

	repo := NewPostgresRepository(pool)
	ctx := context.Background()

	const accountID int64 = 424242
	plan := plans.PlanTrial
	daily, monthly := PeriodStarts(time.Now())

	u, err := repo.Usage(ctx, accountID, daily, monthly)
	if err != nil {
		t.Fatalf("Usage(empty): %v", err)
	}
	if u.DailyTotal != 0 || u.MonthlyTotal != 0 {
		t.Fatalf("empty usage = %+v, want zero", u)
	}

	if err := repo.Increment(ctx, accountID, plan, true, daily, monthly); err != nil {
		t.Fatalf("Increment(success): %v", err)
	}
	if err := repo.Increment(ctx, accountID, plan, false, daily, monthly); err != nil {
		t.Fatalf("Increment(failure): %v", err)
	}

	u, err = repo.Usage(ctx, accountID, daily, monthly)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if u.DailyTotal != 2 || u.DailySuccess != 1 || u.DailyFailed != 1 {
		t.Errorf("daily usage = %+v, want total=2 success=1 failed=1", u)
	}
	if u.MonthlyTotal != 2 || u.MonthlySuccess != 1 || u.MonthlyFailed != 1 {
		t.Errorf("monthly usage = %+v, want total=2 success=1 failed=1", u)
	}
}

func TestPostgresRepositorySeparatesAccounts(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Truncate(t, pool)
	seedAccount(t, pool, 1, "quota-int-a")
	seedAccount(t, pool, 2, "quota-int-b")

	repo := NewPostgresRepository(pool)
	ctx := context.Background()

	plan := plans.PlanTrial
	daily, monthly := PeriodStarts(time.Now())

	if err := repo.Increment(ctx, 1, plan, true, daily, monthly); err != nil {
		t.Fatalf("Increment(account 1): %v", err)
	}

	u, err := repo.Usage(ctx, 2, daily, monthly)
	if err != nil {
		t.Fatalf("Usage(account 2): %v", err)
	}
	if u.DailyTotal != 0 || u.MonthlyTotal != 0 {
		t.Errorf("account 2 usage = %+v, want zero (must not see account 1)", u)
	}
}
