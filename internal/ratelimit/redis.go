package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisLimiter struct {
	client redis.Cmdable
}

func NewRedisLimiter(client redis.Cmdable) *RedisLimiter {
	return &RedisLimiter{client: client}
}

func (r *RedisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	if r.client == nil {
		return Result{}, ErrUnavailable
	}
	if window <= 0 {
		window = time.Minute
	}
	if limit <= 0 {
		return Result{Allowed: false, Remaining: 0}, nil
	}

	bucket := time.Now().UTC().Unix() / int64(window.Seconds())
	redisKey := "rl:" + key + ":" + strconv.FormatInt(bucket, 10)

	count, err := r.client.Incr(ctx, redisKey).Result()
	if err != nil {
		return Result{}, fmt.Errorf("%w: incr: %v", ErrUnavailable, err)
	}
	if count == 1 {

		_ = r.client.Expire(ctx, redisKey, window).Err()
	}

	remaining := int64(limit) - count
	if remaining < 0 {
		remaining = 0
	}
	res := Result{
		Allowed:   count <= int64(limit),
		Remaining: int(remaining),
	}

	if !res.Allowed {
		if ttl, err := r.client.TTL(ctx, redisKey).Result(); err == nil && ttl > 0 {
			res.RetryAfter = ttl
		} else {
			res.RetryAfter = window
		}
	}
	return res, nil
}
