package downloader

import (
	"context"
	stderrors "errors"
	"testing"
)

func TestRegistryInitRunsLifecycleProviders(t *testing.T) {
	r := NewRegistry()
	var inits []string

	registerLifecycle(t, r, "a", func(ctx context.Context) error {
		inits = append(inits, "a")
		return nil
	})
	registerLifecycle(t, r, "b", func(ctx context.Context) error {
		inits = append(inits, "b")
		return nil
	})

	if err := r.Init(context.Background()); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if len(inits) != 2 || inits[0] != "a" || inits[1] != "b" {
		t.Errorf("init order = %v, want [a b]", inits)
	}
}

func TestRegistryInitSkipsNonLifecycleProviders(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubProvider{name: "plain", platform: "plain-test"}); err != nil {
		t.Fatal(err)
	}

	if err := r.Init(context.Background()); err != nil {
		t.Fatalf("Init() error = %v, want nil", err)
	}
}

func TestRegistryInitJoinsFailuresAndContinues(t *testing.T) {
	r := NewRegistry()
	var inits []string

	registerLifecycle(t, r, "broken", func(ctx context.Context) error {
		inits = append(inits, "broken")
		return ErrProviderUnavailable
	})
	registerLifecycle(t, r, "ok", func(ctx context.Context) error {
		inits = append(inits, "ok")
		return nil
	})

	err := r.Init(context.Background())
	if err == nil {
		t.Fatal("Init() error = nil, want joined failure")
	}
	if !stderrors.Is(err, ErrProviderUnavailable) {
		t.Errorf("Init() error should wrap ErrProviderUnavailable, got %v", err)
	}

	if len(inits) != 2 || inits[0] != "broken" || inits[1] != "ok" {
		t.Errorf("init order = %v, want [broken ok] (healthy provider still initialized)", inits)
	}
}

func TestRegistryShutdownRunsLifecycleProviders(t *testing.T) {
	r := NewRegistry()
	var shutdowns []string

	registerLifecycleWithShutdown(t, r, "a", func(ctx context.Context) error {
		shutdowns = append(shutdowns, "a")
		return nil
	})
	registerLifecycleWithShutdown(t, r, "b", func(ctx context.Context) error {
		shutdowns = append(shutdowns, "b")
		return nil
	})

	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if len(shutdowns) != 2 || shutdowns[0] != "a" || shutdowns[1] != "b" {
		t.Errorf("shutdown order = %v, want [a b]", shutdowns)
	}
}

func TestRegistryShutdownJoinsFailures(t *testing.T) {
	r := NewRegistry()
	registerLifecycleWithShutdown(t, r, "broken", func(ctx context.Context) error {
		return stderrors.New("shutdown boom")
	})

	if err := r.Shutdown(context.Background()); err == nil {
		t.Fatal("Shutdown() error = nil, want joined failure")
	}
}

func TestServiceResolveReturnsUnavailableWhenNotReady(t *testing.T) {
	r := NewRegistry()
	registerLifecycle(t, r, "not-ready", func(ctx context.Context) error {
		return nil
	})

	last, err := r.Get("not-ready")
	if err != nil {
		t.Fatal(err)
	}
	last.(*lifecycleProvider).ready = func() error { return ErrProviderUnavailable }

	svc := NewService(r)

	_, err = svc.Resolve(context.Background(), DownloadRequest{
		Platform: "not-ready",
		URL:      "https://x",
	})
	if !stderrors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrProviderUnavailable", err)
	}
}

func TestServiceResolveDelegatesWhenReady(t *testing.T) {
	r := NewRegistry()
	var resolved bool
	registerLifecycle(t, r, "ready", func(ctx context.Context) error { return nil })
	last, _ := r.Get("ready")
	last.(*lifecycleProvider).ready = func() error { return nil }
	last.(*lifecycleProvider).resolve = func(context.Context, DownloadRequest) (*DownloadResult, error) {
		resolved = true
		return &DownloadResult{}, nil
	}

	svc := NewService(r)

	if _, err := svc.Resolve(context.Background(), DownloadRequest{
		Platform: "ready",
		URL:      "https://x",
	}); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !resolved {
		t.Error("provider.Resolve was not called when ready")
	}
}

func TestServiceResolveInitFailureKeepsProviderUnavailable(t *testing.T) {

	r := NewRegistry()
	registerLifecycle(t, r, "flaky", func(ctx context.Context) error {
		return ErrProviderUnavailable
	})
	last, _ := r.Get("flaky")
	last.(*lifecycleProvider).ready = func() error { return ErrProviderUnavailable }

	_ = r.Init(context.Background())

	svc := NewService(r)
	_, err := svc.Resolve(context.Background(), DownloadRequest{
		Platform: "flaky",
		URL:      "https://x",
	})
	if !stderrors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrProviderUnavailable", err)
	}
}

func registerLifecycle(t *testing.T, r *Registry, name Platform, init func(ctx context.Context) error) {
	t.Helper()
	p := &lifecycleProvider{}
	p.stubProvider = stubProvider{name: string(name), platform: name}
	p.init = init
	if err := r.Register(p); err != nil {
		t.Fatalf("register lifecycle provider %q: %v", name, err)
	}
}

func registerLifecycleWithShutdown(t *testing.T, r *Registry, name Platform, shutdown func(ctx context.Context) error) {
	t.Helper()
	p := &lifecycleProvider{}
	p.stubProvider = stubProvider{name: string(name), platform: name}
	p.shutdown = shutdown
	if err := r.Register(p); err != nil {
		t.Fatalf("register lifecycle provider %q: %v", name, err)
	}
}
