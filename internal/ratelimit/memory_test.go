package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestMemoryLimiterAllowsWithinLimit(t *testing.T) {
	limiter := NewMemoryLimiter()
	ctx := context.Background()
	key := "account:42"
	window := time.Minute

	r1, err := limiter.Allow(ctx, key, 2, window)
	if err != nil {
		t.Fatalf("Allow 1: %v", err)
	}
	if !r1.Allowed || r1.Remaining != 1 {
		t.Errorf("Allow 1 = %+v, want allowed with remaining 1", r1)
	}

	r2, err := limiter.Allow(ctx, key, 2, window)
	if err != nil {
		t.Fatalf("Allow 2: %v", err)
	}
	if !r2.Allowed || r2.Remaining != 0 {
		t.Errorf("Allow 2 = %+v, want allowed with remaining 0", r2)
	}

	r3, err := limiter.Allow(ctx, key, 2, window)
	if err != nil {
		t.Fatalf("Allow 3: %v", err)
	}
	if r3.Allowed {
		t.Errorf("Allow 3 = %+v, want denied", r3)
	}
	if r3.RetryAfter <= 0 {
		t.Errorf("denied result should have a positive RetryAfter, got %v", r3.RetryAfter)
	}
}

func TestMemoryLimiterKeysAreIsolated(t *testing.T) {
	limiter := NewMemoryLimiter()
	ctx := context.Background()
	window := time.Minute

	if _, err := limiter.Allow(ctx, "account:1", 1, window); err != nil {
		t.Fatalf("account:1 first: %v", err)
	}

	r, err := limiter.Allow(ctx, "account:2", 1, window)
	if err != nil {
		t.Fatalf("account:2: %v", err)
	}
	if !r.Allowed {
		t.Error("a different key should be allowed independently")
	}
}

func TestMemoryLimiterZeroLimitDenies(t *testing.T) {
	limiter := NewMemoryLimiter()
	r, err := limiter.Allow(context.Background(), "account:42", 0, time.Minute)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if r.Allowed {
		t.Error("zero limit should deny")
	}
}
