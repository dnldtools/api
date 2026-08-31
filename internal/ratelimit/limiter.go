package ratelimit

import (
	"context"
	"errors"
	"time"
)

var ErrUnavailable = errors.New("ratelimit: backend unavailable")

type Result struct {
	Allowed bool

	Remaining int

	RetryAfter time.Duration
}

type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error)
}
