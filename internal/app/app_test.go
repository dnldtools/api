package app

import (
	"context"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"rest-api/internal/browser"
	"rest-api/internal/config"
)

func testConfig(addr string) *config.Config {
	host, port, _ := net.SplitHostPort(addr)
	return &config.Config{
		AppName:         "rest-api-test",
		AppEnv:          "test",
		AppVersion:      "test",
		HTTPHost:        host,
		HTTPPort:        port,
		ReadTimeout:     time.Second,
		WriteTimeout:    time.Second,
		IdleTimeout:     time.Second,
		ShutdownTimeout: 2 * time.Second,
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeAddr: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func waitForServer(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server did not start listening on %s", addr)
}

func TestNewRequiresConfig(t *testing.T) {
	if _, err := New(nil, slog.Default()); err == nil {
		t.Error("New(nil, ...) should return an error")
	}
}

func TestNewWiresDependencies(t *testing.T) {
	a, err := New(testConfig(freeAddr(t)), slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if a.youtube == nil {
		t.Error("youtube service not wired")
	}
	if a.downloader == nil {
		t.Error("downloader service not wired")
	}
	if a.server == nil {
		t.Error("http server not wired")
	}
	if a.browser != nil {
		t.Error("browser manager should be nil when BROWSER_ENABLED=false")
	}
}

func TestRunShutsDownOnContextCancellation(t *testing.T) {
	addr := freeAddr(t)
	a, err := New(testConfig(addr), slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	waitForServer(t, addr)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestRunReturnsServerError(t *testing.T) {

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	a, err := New(testConfig(ln.Addr().String()), slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run() = nil, want a server error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return on fatal server error")
	}
}

type fakeAppBrowser struct {
	name   string
	closed atomic.Bool
}

func (f *fakeAppBrowser) Name() string    { return f.name }
func (f *fakeAppBrowser) Version() string { return "0.0.0" }
func (f *fakeAppBrowser) NewContext(context.Context, browser.ContextOptions) (browser.BrowserContext, error) {
	return &fakeAppContext{}, nil
}
func (f *fakeAppBrowser) Close(context.Context) error {
	f.closed.Store(true)
	return nil
}

type fakeAppContext struct{}

func (f *fakeAppContext) NewPage(context.Context) (browser.Page, error) { return &fakeAppPage{}, nil }
func (f *fakeAppContext) Close(context.Context) error                   { return nil }

type fakeAppPage struct{}

func (f *fakeAppPage) Goto(context.Context, string) error      { return nil }
func (f *fakeAppPage) Content(context.Context) (string, error) { return "", nil }
func (f *fakeAppPage) Title(context.Context) (string, error)   { return "", nil }
func (f *fakeAppPage) URL() string                             { return "" }
func (f *fakeAppPage) Close(context.Context) error             { return nil }

type fakeAppLauncher struct {
	b     *fakeAppBrowser
	stops atomic.Int32
}

func (f *fakeAppLauncher) Name() string { return "fake" }
func (f *fakeAppLauncher) Launch(context.Context, browser.LaunchOptions) (browser.Browser, error) {
	return f.b, nil
}
func (f *fakeAppLauncher) Stop(context.Context) error {
	f.stops.Add(1)
	return nil
}

func TestRunShutsDownBrowserOnCancellation(t *testing.T) {
	fb := &fakeAppBrowser{name: "fake"}
	launcher := &fakeAppLauncher{b: fb}

	prev := browserLauncherFactory
	browserLauncherFactory = func() browser.Launcher { return launcher }
	defer func() { browserLauncherFactory = prev }()

	cfg := testConfig(freeAddr(t))
	cfg.Browser = browser.DefaultConfig()
	cfg.Browser.Enabled = true

	a, err := New(cfg, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	waitForServer(t, cfg.Addr())
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	if !fb.closed.Load() {
		t.Error("browser should be closed on shutdown")
	}
	if got := launcher.stops.Load(); got != 1 {
		t.Errorf("launcher stops = %d, want 1", got)
	}
}
