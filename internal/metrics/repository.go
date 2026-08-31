package metrics

import (
	"context"
	"time"
)

type Repository interface {
	Insert(ctx context.Context, e Event) error

	Aggregate(ctx context.Context, from, to time.Time) (Stats, error)

	EndpointBreakdown(ctx context.Context, from, to time.Time) ([]EndpointStat, error)

	PlatformBreakdown(ctx context.Context, from, to time.Time) ([]PlatformStat, error)
}

type Aggregator interface {
	Increment(ctx context.Context, e Event) error
}
