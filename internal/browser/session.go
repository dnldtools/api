package browser

import (
	"context"
	"sync"
	"time"
)

type Session struct {
	mu             sync.Mutex
	bctx           BrowserContext
	released       bool
	cleanupTimeout time.Duration
	release        func(context.Context)
	once           sync.Once
}

func newSession(bctx BrowserContext, cleanupTimeout time.Duration, release func(context.Context)) *Session {
	return &Session{
		bctx:           bctx,
		cleanupTimeout: cleanupTimeout,
		release:        release,
	}
}

func (s *Session) Context() BrowserContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil
	}
	return s.bctx
}

func (s *Session) NewPage(ctx context.Context) (Page, error) {
	bctx := s.Context()
	if bctx == nil {
		return nil, ErrSessionReleased
	}
	return bctx.NewPage(ctx)
}

func (s *Session) Release() {
	s.once.Do(func() {
		s.mu.Lock()
		s.released = true
		s.bctx = nil
		s.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), s.cleanupTimeout)
		defer cancel()
		s.release(ctx)
	})
}
