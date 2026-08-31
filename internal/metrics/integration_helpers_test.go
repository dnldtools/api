package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"rest-api/internal/cache"
)

func redisGetValue(t *testing.T, r *cache.Redis, key string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	v, err := r.Client().Get(ctx, key).Result()
	if err == redis.Nil {
		return ""
	}
	if err != nil {
		t.Fatalf("redis get %s: %v", key, err)
	}
	return v
}

func assertRedis(t *testing.T, r *cache.Redis, key, want string) {
	t.Helper()
	if got := redisGetValue(t, r, key); got != want {
		t.Errorf("redis %s = %q, want %q", key, got, want)
	}
}
