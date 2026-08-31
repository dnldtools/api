package browser

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeManager struct {
	name    string
	acquire func(ctx context.Context) (*Session, error)
}

func (f *fakeManager) Name() string                   { return f.name }
func (f *fakeManager) Start(context.Context) error    { return nil }
func (f *fakeManager) Shutdown(context.Context) error { return nil }
func (f *fakeManager) Acquire(ctx context.Context) (*Session, error) {
	if f.acquire == nil {
		return nil, nil
	}
	return f.acquire(ctx)
}

func TestNewRuntimeRejectsNilManager(t *testing.T) {
	if _, err := NewRuntime(nil, time.Second); err == nil {
		t.Fatal("NewRuntime(nil, ...) should return an error")
	}
}

func TestNewRuntimeDefaultsNavigationTimeout(t *testing.T) {
	rt, err := NewRuntime(&fakeManager{name: "fake"}, 0)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if rt.navTimeout != DefaultConfig().NavigationTimeout {
		t.Errorf("navTimeout = %v, want default %v", rt.navTimeout, DefaultConfig().NavigationTimeout)
	}
}

func TestRuntimeNamePassthrough(t *testing.T) {
	rt, err := NewRuntime(&fakeManager{name: "playwright"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Name() != "playwright" {
		t.Errorf("Name() = %q, want playwright", rt.Name())
	}
}

func TestRuntimeFetchReturnsContent(t *testing.T) {
	lb := &fakeBrowser{}
	launcher := &fakeLauncher{browser: lb}

	m, err := NewManager(testConfig(), launcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	rt, err := NewRuntime(m, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	got, err := rt.Fetch(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got != "<html></html>" {
		t.Errorf("Fetch() = %q, want rendered HTML", got)
	}
}

func TestRuntimeFetchHonorsCanceledContext(t *testing.T) {
	rt, err := NewRuntime(&fakeManager{name: "fake"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := rt.Fetch(ctx, "https://example.com"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch() error = %v, want context.Canceled", err)
	}
}

func TestRuntimeFetchPropagatesCancellation(t *testing.T) {
	rt, err := NewRuntime(&fakeManager{
		name: "fake",
		acquire: func(ctx context.Context) (*Session, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	if _, err := rt.Fetch(ctx, "https://example.com"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch() error = %v, want context.Canceled", err)
	}
}

func TestRuntimeFetchMapsDeadlineToTimeout(t *testing.T) {
	rt, err := NewRuntime(&fakeManager{
		name: "fake",
		acquire: func(context.Context) (*Session, error) {
			return nil, context.DeadlineExceeded
		},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Fetch(context.Background(), "https://example.com"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Fetch() error = %v, want ErrTimeout", err)
	}
}

func TestRuntimeFetchMapsTimeoutFromOwnDeadline(t *testing.T) {
	rt, err := NewRuntime(&fakeManager{
		name: "fake",
		acquire: func(ctx context.Context) (*Session, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Fetch(context.Background(), "https://example.com"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Fetch() error = %v, want ErrTimeout", err)
	}
}

func TestRuntimeFetchMapsClosedManagerToUnavailable(t *testing.T) {
	rt, err := NewRuntime(&fakeManager{
		name: "fake",
		acquire: func(context.Context) (*Session, error) {
			return nil, ErrManagerClosed
		},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Fetch(context.Background(), "https://example.com"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Fetch() error = %v, want ErrUnavailable", err)
	}
}

func TestRuntimeFetchMapsNotStartedToUnavailable(t *testing.T) {
	rt, err := NewRuntime(&fakeManager{
		name: "fake",
		acquire: func(context.Context) (*Session, error) {
			return nil, ErrNotStarted
		},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Fetch(context.Background(), "https://example.com"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Fetch() error = %v, want ErrUnavailable", err)
	}
}
