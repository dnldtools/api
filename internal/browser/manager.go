package browser

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type manager struct {
	cfg      Config
	launcher Launcher

	mu      sync.RWMutex
	browser Browser
	started bool
	closed  bool

	sem chan struct{}

	wg sync.WaitGroup
}

func NewManager(cfg Config, launcher Launcher) (Manager, error) {
	if launcher == nil {
		return nil, errors.New("browser: launcher is nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &manager{
		cfg:      cfg,
		launcher: launcher,
		sem:      make(chan struct{}, cfg.MaxConcurrency),
	}, nil
}

func (m *manager) Name() string {
	return m.launcher.Name()
}

func (m *manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrManagerClosed
	}
	if m.started {
		return nil
	}

	b, err := m.launcher.Launch(ctx, m.launchOptions())
	if err != nil {
		return fmt.Errorf("browser: launch: %w", err)
	}

	m.browser = b
	m.started = true
	return nil
}

func (m *manager) Acquire(ctx context.Context) (*Session, error) {

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrManagerClosed
	}
	m.wg.Add(1)
	m.mu.Unlock()

	if err := m.Start(ctx); err != nil {
		m.wg.Done()
		return nil, err
	}

	select {
	case m.sem <- struct{}{}:
	case <-ctx.Done():
		m.wg.Done()
		return nil, ctx.Err()
	}

	m.mu.RLock()
	b := m.browser
	m.mu.RUnlock()
	if b == nil {
		<-m.sem
		m.wg.Done()
		return nil, ErrNotStarted
	}

	bctx, err := b.NewContext(ctx, m.contextOptions())
	if err != nil {
		<-m.sem
		m.wg.Done()
		return nil, fmt.Errorf("browser: create context: %w", err)
	}

	return newSession(bctx, m.cfg.CleanupTimeout, func(ctx context.Context) {
		_ = bctx.Close(ctx)
		<-m.sem
		m.wg.Done()
	}), nil
}

func (m *manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}

	var errs []error

	m.mu.RLock()
	b := m.browser
	m.mu.RUnlock()
	if b != nil {
		if err := b.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("browser: close browser: %w", err))
		}
	}

	if err := m.launcher.Stop(ctx); err != nil {
		errs = append(errs, fmt.Errorf("browser: stop launcher: %w", err))
	}

	return errors.Join(errs...)
}

func (m *manager) launchOptions() LaunchOptions {
	return LaunchOptions{
		Headless:       m.cfg.Headless,
		ExecutablePath: m.cfg.ExecutablePath,
		UserAgent:      m.cfg.UserAgent,
		Proxy:          m.cfg.Proxy,
		Timeout:        m.cfg.Timeout,
	}
}

func (m *manager) contextOptions() ContextOptions {
	return ContextOptions{
		UserAgent:         m.cfg.UserAgent,
		Proxy:             m.cfg.Proxy,
		Timeout:           m.cfg.Timeout,
		NavigationTimeout: m.cfg.NavigationTimeout,
	}
}
