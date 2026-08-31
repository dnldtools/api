package metrics

import (
	"context"
	"time"
)

type Repository interface {
	Insert(ctx context.Context, e Event) error

	Aggregate(ctx context.Context, from, to time.Time) (Stats, error)
}

type Aggregator interface {
	Increment(ctx context.Context, e Event) error
}
