package quota

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"rest-api/internal/plans"
)

const redisCounterTTL = 40 * 24 * time.Hour

type RedisCounter struct {
	client redis.Cmdable
}

func NewRedisCounter(client redis.Cmdable) *RedisCounter {
	return &RedisCounter{client: client}
}

func (r *RedisCounter) Increment(ctx context.Context, accountID int64, plan plans.Plan, dailyStart, monthlyStart time.Time) error {
	if r.client == nil {
		return nil
	}

	dayKey := "quota:day:" + strconv.FormatInt(accountID, 10) + ":" + dailyStart.Format("20060102")
	monthKey := "quota:month:" + strconv.FormatInt(accountID, 10) + ":" + monthlyStart.Format("200601")

	pipe := r.client.TxPipeline()
	pipe.Incr(ctx, dayKey)
	pipe.Expire(ctx, dayKey, redisCounterTTL)
	pipe.Incr(ctx, monthKey)
	pipe.Expire(ctx, monthKey, redisCounterTTL)
	_, err := pipe.Exec(ctx)
	return err
}
