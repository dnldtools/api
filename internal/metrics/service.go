package metrics

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const persistTimeout = 5 * time.Second

type Service struct {
	repo    Repository
	agg     Aggregator
	queue   chan Event
	logger  *slog.Logger
	enabled bool

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	closed   atomic.Bool
	dropped  atomic.Int64
}

func New(repo Repository, agg Aggregator, logger *slog.Logger, queueSize int) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if queueSize <= 0 {
		queueSize = 1024
	}

	s := &Service{
		repo:    repo,
		agg:     agg,
		queue:   make(chan Event, queueSize),
		logger:  logger,
		enabled: repo != nil || agg != nil,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}

	if s.enabled {
		go s.worker()
	}

	return s
}

func (s *Service) Record(e Event) {
	if s == nil || s.closed.Load() {
		return
	}
	if !s.enabled {
		return
	}

	e.Normalize()

	select {
	case s.queue <- e:
	default:
		s.dropped.Add(1)
		s.logger.Warn("metrics queue full; dropping event", "dropped_total", s.dropped.Load())
	}
}

func (s *Service) Aggregate(ctx context.Context, from, to time.Time) (Stats, error) {
	if s == nil || s.repo == nil {
		return Stats{}, nil
	}
	return s.repo.Aggregate(ctx, from, to)
}

func (s *Service) EndpointBreakdown(ctx context.Context, from, to time.Time) ([]EndpointStat, error) {
	if s == nil || s.repo == nil {
		return []EndpointStat{}, nil
	}
	return s.repo.EndpointBreakdown(ctx, from, to)
}

func (s *Service) PlatformBreakdown(ctx context.Context, from, to time.Time) ([]PlatformStat, error) {
	if s == nil || s.repo == nil {
		return []PlatformStat{}, nil
	}
	return s.repo.PlatformBreakdown(ctx, from, to)
}

func (s *Service) Close(ctx context.Context) {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if !s.enabled {
			s.closed.Store(true)
			return
		}

		s.closed.Store(true)
		close(s.stop)
		<-s.done

		s.drain(ctx)

		if n := s.dropped.Load(); n > 0 {
			s.logger.Warn("metrics events dropped due to full queue", "count", n)
		}
	})
}

func (s *Service) worker() {
	defer close(s.done)
	for {
		select {
		case e := <-s.queue:
			ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
			s.persist(ctx, e)
			cancel()
		case <-s.stop:
			return
		}
	}
}

func (s *Service) drain(ctx context.Context) {
	for {
		select {
		case e := <-s.queue:
			s.persist(ctx, e)
		default:
			return
		}
	}
}

func (s *Service) persist(ctx context.Context, e Event) {
	if s.repo != nil {
		if err := s.repo.Insert(ctx, e); err != nil {
			s.logger.Error("metrics insert failed", "error", err)
		}
	}
	if s.agg != nil {
		if err := s.agg.Increment(ctx, e); err != nil {
			s.logger.Warn("metrics counter failed", "error", err)
		}
	}
}
