package quota

import (
	"context"
	"strconv"
	"testing"
	"time"

	"rest-api/internal/plans"
	"rest-api/internal/testutil"
)

func TestRedisCounterIncrements(t *testing.T) {
	rdb := testutil.OpenTestRedis(t)
	testutil.FlushRedis(t, rdb)

	counter := NewRedisCounter(rdb.Client())
	ctx := context.Background()

	const accountID int64 = 777
	plan := plans.PlanTrial
	daily, monthly := PeriodStarts(time.Now())

	if err := counter.Increment(ctx, accountID, plan, daily, monthly); err != nil {
		t.Fatalf("Increment: %v", err)
	}

	dayKey := "quota:day:" + strconv.FormatInt(accountID, 10) + ":" + daily.Format("20060102")
	monthKey := "quota:month:" + strconv.FormatInt(accountID, 10) + ":" + monthly.Format("200601")

	dayVal, err := rdb.Client().Get(ctx, dayKey).Result()
	if err != nil {
		t.Fatalf("get %s: %v", dayKey, err)
	}
	if dayVal != "1" {
		t.Errorf("%s = %q, want 1", dayKey, dayVal)
	}

	monthVal, err := rdb.Client().Get(ctx, monthKey).Result()
	if err != nil {
		t.Fatalf("get %s: %v", monthKey, err)
	}
	if monthVal != "1" {
		t.Errorf("%s = %q, want 1", monthKey, monthVal)
	}

	ttl, err := rdb.Client().TTL(ctx, dayKey).Result()
	if err != nil {
		t.Fatalf("ttl %s: %v", dayKey, err)
	}
	if ttl <= 0 {
		t.Errorf("ttl %s = %v, want positive", dayKey, ttl)
	}
	if ttl > redisCounterTTL+time.Minute {
		t.Errorf("ttl %s = %v, want <= %v", dayKey, ttl, redisCounterTTL)
	}
}

func TestRedisCounterNilClientIsNoop(t *testing.T) {
	counter := NewRedisCounter(nil)
	daily, monthly := PeriodStarts(time.Now())
	if err := counter.Increment(context.Background(), 1, plans.PlanTrial, daily, monthly); err != nil {
		t.Fatalf("Increment(nil): %v", err)
	}
}
