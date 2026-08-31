package browser

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type fakePage struct{}

func (f *fakePage) Goto(context.Context, string) error      { return nil }
func (f *fakePage) Content(context.Context) (string, error) { return "<html></html>", nil }
func (f *fakePage) Title(context.Context) (string, error)   { return "title", nil }
func (f *fakePage) URL() string                             { return "about:blank" }
func (f *fakePage) Close(context.Context) error             { return nil }

type fakeContext struct{}

func (f *fakeContext) NewPage(context.Context) (Page, error) { return &fakePage{}, nil }
func (f *fakeContext) Close(context.Context) error           { return nil }

type fakeBrowser struct {
	contexts atomic.Int32
	closed   atomic.Bool
}

func (f *fakeBrowser) Name() string    { return "fake" }
func (f *fakeBrowser) Version() string { return "0.0.0" }
func (f *fakeBrowser) NewContext(context.Context, ContextOptions) (BrowserContext, error) {
	f.contexts.Add(1)
	return &fakeContext{}, nil
}
func (f *fakeBrowser) Close(context.Context) error {
	f.closed.Store(true)
	return nil
}

type fakeLauncher struct {
	browser  *fakeBrowser
	launches atomic.Int32
	stops    atomic.Int32
}

func (f *fakeLauncher) Name() string { return "fake" }
func (f *fakeLauncher) Launch(context.Context, LaunchOptions) (Browser, error) {
	f.launches.Add(1)
	return f.browser, nil
}
func (f *fakeLauncher) Stop(context.Context) error {
	f.stops.Add(1)
	return nil
}

func testConfig() Config {
	c := DefaultConfig()
	c.MaxConcurrency = 2
	return c
}

func TestManagerStartIsIdempotent(t *testing.T) {
	lb := &fakeBrowser{}
	launcher := &fakeLauncher{browser: lb}

	m, err := NewManager(testConfig(), launcher)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := launcher.launches.Load(); got != 1 {
		t.Fatalf("launches = %d, want 1", got)
	}
}

func TestManagerAcquireProvidesIsolatedContexts(t *testing.T) {
	lb := &fakeBrowser{}
	launcher := &fakeLauncher{browser: lb}

	m, _ := NewManager(testConfig(), launcher)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	s1, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s2, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s1.Release()
	s2.Release()

	if got := lb.contexts.Load(); got != 2 {
		t.Fatalf("contexts = %d, want 2 (one isolated context per acquire)", got)
	}
}

func TestManagerAcquireRespectsConcurrencyLimit(t *testing.T) {
	lb := &fakeBrowser{}
	launcher := &fakeLauncher{browser: lb}

	cfg := testConfig()
	cfg.MaxConcurrency = 1
	m, _ := NewManager(cfg, launcher)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	s1, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := m.Acquire(ctx); err == nil {
		t.Fatal("expected acquire to block/timeout when concurrency limit is reached")
	}

	s1.Release()

	s2, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire after release should succeed, got %v", err)
	}
	s2.Release()
}

func TestSessionReleaseIsIdempotent(t *testing.T) {
	lb := &fakeBrowser{}
	launcher := &fakeLauncher{browser: lb}

	m, _ := NewManager(testConfig(), launcher)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	s, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	s.Release()
	s.Release()

	if s.Context() != nil {
		t.Fatal("Context() should be nil after release")
	}
	if _, err := s.NewPage(context.Background()); err != ErrSessionReleased {
		t.Fatalf("NewPage error = %v, want ErrSessionReleased", err)
	}
}

func TestManagerShutdownClosesBrowserAndLauncher(t *testing.T) {
	lb := &fakeBrowser{}
	launcher := &fakeLauncher{browser: lb}

	m, _ := NewManager(testConfig(), launcher)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !lb.closed.Load() {
		t.Fatal("browser should be closed")
	}
	if got := launcher.stops.Load(); got != 1 {
		t.Fatalf("stops = %d, want 1", got)
	}

	if _, err := m.Acquire(context.Background()); err != ErrManagerClosed {
		t.Fatalf("Acquire after shutdown = %v, want ErrManagerClosed", err)
	}
}

func TestManagerShutdownIsIdempotent(t *testing.T) {
	lb := &fakeBrowser{}
	launcher := &fakeLauncher{browser: lb}

	m, _ := NewManager(testConfig(), launcher)
	_ = m.Start(context.Background())

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown should be a no-op, got %v", err)
	}
	if got := launcher.stops.Load(); got != 1 {
		t.Fatalf("stops = %d, want 1", got)
	}
}

func TestNewManagerRejectsNilLauncher(t *testing.T) {
	if _, err := NewManager(testConfig(), nil); err == nil {
		t.Fatal("expected error for nil launcher")
	}
}
