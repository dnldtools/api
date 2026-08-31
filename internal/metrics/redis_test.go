package metrics

import (
	"context"
	"testing"
	"time"

	"rest-api/internal/testutil"
)

func TestRedisAggregatorIncrements(t *testing.T) {
	rdb := testutil.OpenTestRedis(t)
	testutil.FlushRedis(t, rdb)

	agg := NewRedisAggregator(rdb.Client())
	ctx := context.Background()

	if err := agg.Increment(ctx, Event{
		StatusCode:  200,
		Success:     true,
		ValidAPIKey: true,
		Endpoint:    "POST /v1/downloads",
		Timestamp:   time.Now(),
	}); err != nil {
		t.Fatalf("Increment(success): %v", err)
	}

	assertRedis(t, rdb, "metrics:total", "1")
	assertRedis(t, rdb, "metrics:success", "1")
	assertRedis(t, rdb, "metrics:valid_api_key", "1")
	assertRedis(t, rdb, "metrics:endpoint:POST /v1/downloads:total", "1")
	assertRedis(t, rdb, "metrics:status:200", "1")

	if got := redisGetValue(t, rdb, "metrics:failed"); got != "" {
		t.Errorf("metrics:failed = %q, want absent", got)
	}
	if got := redisGetValue(t, rdb, "metrics:invalid_api_key"); got != "" {
		t.Errorf("metrics:invalid_api_key = %q, want absent", got)
	}

	if err := agg.Increment(ctx, Event{
		StatusCode:    401,
		Success:       false,
		InvalidAPIKey: true,
		APIKeyMissing: true,
		Endpoint:      "POST /v1/downloads",
		Timestamp:     time.Now(),
	}); err != nil {
		t.Fatalf("Increment(failure): %v", err)
	}

	assertRedis(t, rdb, "metrics:total", "2")
	assertRedis(t, rdb, "metrics:success", "1")
	assertRedis(t, rdb, "metrics:failed", "1")
	assertRedis(t, rdb, "metrics:invalid_api_key", "1")
	assertRedis(t, rdb, "metrics:missing_api_key", "1")
	assertRedis(t, rdb, "metrics:status:401", "1")
}
