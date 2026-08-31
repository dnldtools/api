package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type Config struct {
	Addr     string
	Password string
	DB       int
	PoolSize int
}

func (c Config) Enabled() bool { return c.Addr != "" }

func (c Config) Validate() error {
	if c.Addr == "" {
		return fmt.Errorf("REDIS_ADDR must not be empty")
	}
	if c.PoolSize <= 0 {
		return fmt.Errorf("REDIS_POOL_SIZE must be positive")
	}
	return nil
}

type Redis struct {
	client *redis.Client
}

func New(cfg Config) (*Redis, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
		PoolSize: cfg.PoolSize,
	})
	return &Redis{client: client}, nil
}

func (r *Redis) Client() *redis.Client { return r.client }

func (r *Redis) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }

func (r *Redis) Close() error {
	if r.client == nil {
		return nil
	}
	return r.client.Close()
}
