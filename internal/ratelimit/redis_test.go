package ratelimit

import (
	"context"
	"testing"
	"time"

	"rest-api/internal/testutil"
)

func TestRedisLimiterFixedWindow(t *testing.T) {
	rdb := testutil.OpenTestRedis(t)
	testutil.FlushRedis(t, rdb)

	limiter := NewRedisLimiter(rdb.Client())
	ctx := context.Background()

	const limit = 3
	key := "account:test-redis-limiter"
	window := time.Minute

	for i := 0; i < limit; i++ {
		res, err := limiter.Allow(ctx, key, limit, window)
		if err != nil {
			t.Fatalf("Allow(%d): %v", i, err)
		}
		if !res.Allowed {
			t.Fatalf("Allow(%d).Allowed = false, want true", i)
		}
		if res.Remaining != limit-(i+1) {
			t.Errorf("Allow(%d).Remaining = %d, want %d", i, res.Remaining, limit-(i+1))
		}
		if res.RetryAfter != 0 {
			t.Errorf("Allow(%d).RetryAfter = %v, want 0", i, res.RetryAfter)
		}
	}

	res, err := limiter.Allow(ctx, key, limit, window)
	if err != nil {
		t.Fatalf("Allow(denied): %v", err)
	}
	if res.Allowed {
		t.Error("Allow(denied).Allowed = true, want false")
	}
	if res.Remaining != 0 {
		t.Errorf("Allow(denied).Remaining = %d, want 0", res.Remaining)
	}
	if res.RetryAfter <= 0 {
		t.Errorf("Allow(denied).RetryAfter = %v, want positive", res.RetryAfter)
	}
}

func TestRedisLimiterZeroLimit(t *testing.T) {
	rdb := testutil.OpenTestRedis(t)
	testutil.FlushRedis(t, rdb)

	limiter := NewRedisLimiter(rdb.Client())
	res, err := limiter.Allow(context.Background(), "account:zero-limit", 0, time.Minute)
	if err != nil {
		t.Fatalf("Allow(zero limit): %v", err)
	}
	if res.Allowed {
		t.Error("Allow(zero limit).Allowed = true, want false")
	}
	if res.Remaining != 0 {
		t.Errorf("Remaining = %d, want 0", res.Remaining)
	}
}

func TestRedisLimiterIndependentKeys(t *testing.T) {
	rdb := testutil.OpenTestRedis(t)
	testutil.FlushRedis(t, rdb)

	limiter := NewRedisLimiter(rdb.Client())
	ctx := context.Background()

	if _, err := limiter.Allow(ctx, "account:a", 1, time.Minute); err != nil {
		t.Fatalf("Allow(a): %v", err)
	}
	res, err := limiter.Allow(ctx, "account:b", 1, time.Minute)
	if err != nil {
		t.Fatalf("Allow(b): %v", err)
	}
	if !res.Allowed {
		t.Error("Allow(b).Allowed = false, want true (b has its own bucket)")
	}
}
