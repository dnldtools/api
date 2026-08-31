package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisBucketTTL = 48 * time.Hour

type RedisAggregator struct {
	client redis.Cmdable
}

func NewRedisAggregator(client redis.Cmdable) *RedisAggregator {
	return &RedisAggregator{client: client}
}

func (r *RedisAggregator) Increment(ctx context.Context, e Event) error {
	if r.client == nil {
		return nil
	}

	pipe := r.client.TxPipeline()
	pipe.Incr(ctx, "metrics:total")
	if e.Success {
		pipe.Incr(ctx, "metrics:success")
	} else {
		pipe.Incr(ctx, "metrics:failed")
	}
	pipe.Incr(ctx, "metrics:endpoint:"+e.Endpoint+":total")
	pipe.Incr(ctx, "metrics:status:"+fmt.Sprint(e.StatusCode))
	if e.ValidAPIKey {
		pipe.Incr(ctx, "metrics:valid_api_key")
	}
	if e.InvalidAPIKey {
		pipe.Incr(ctx, "metrics:invalid_api_key")
	}
	if e.APIKeyMissing {
		pipe.Incr(ctx, "metrics:missing_api_key")
	}
	if e.RateLimited {
		pipe.Incr(ctx, "metrics:rate_limited")
	}
	if e.QuotaExceeded {
		pipe.Incr(ctx, "metrics:quota_exceeded")
	}

	bucket := e.Timestamp.UTC().Format("2006010215")
	pipe.Incr(ctx, "metrics:hour:"+bucket+":total")
	pipe.Expire(ctx, "metrics:hour:"+bucket+":total", redisBucketTTL)

	_, err := pipe.Exec(ctx)
	return err
}
