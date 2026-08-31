package downloader

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"
)

func TestServiceImplementsDownloaderContract(t *testing.T) {
	var _ Downloader = (*Service)(nil)
}

func TestServiceResolveInvalidRequest(t *testing.T) {
	svc := NewService(NewRegistry())

	_, err := svc.Resolve(context.Background(), DownloadRequest{URL: ""})
	if !stderrors.Is(err, ErrInvalidURL) {
		t.Fatalf("expected ErrInvalidURL, got %v", err)
	}

	_, err = svc.Resolve(context.Background(), DownloadRequest{URL: "   "})
	if !stderrors.Is(err, ErrInvalidURL) {
		t.Fatalf("expected ErrInvalidURL for whitespace URL, got %v", err)
	}
}

func TestServiceResolveUnsupportedPlatform(t *testing.T) {
	svc := NewService(NewRegistry())

	_, err := svc.Resolve(context.Background(), DownloadRequest{
		Platform: Platform("unknown"),
		URL:      "https://unknown.example/p/1",
	})
	if !stderrors.Is(err, ErrPlatformUnsupported) {
		t.Fatalf("expected ErrPlatformUnsupported, got %v", err)
	}
}

func TestServiceSelectsProviderForPlatform(t *testing.T) {
	r := NewRegistry()
	var called string
	if err := r.Register(&stubProvider{
		name:     "other",
		platform: Platform("other"),
		resolve: func(_ context.Context, _ DownloadRequest) (*DownloadResult, error) {
			called = "other"
			return &DownloadResult{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(&stubProvider{
		name:     "facebook",
		platform: PlatformFacebook,
		resolve: func(_ context.Context, _ DownloadRequest) (*DownloadResult, error) {
			called = "facebook"
			return &DownloadResult{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(r)

	_, err := svc.Resolve(context.Background(), DownloadRequest{
		Platform: PlatformFacebook,
		URL:      "https://facebook.com/reel/123",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if called != "facebook" {
		t.Errorf("selected provider = %q, want facebook", called)
	}
}

func TestServiceAutoDetectsPlatform(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubProvider{
		name:     "facebook",
		platform: PlatformFacebook,
		matches:  func(url string) bool { return strings.Contains(url, "facebook.com") },
		resolve: func(_ context.Context, req DownloadRequest) (*DownloadResult, error) {
			return &DownloadResult{URL: req.URL}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(r)

	res, err := svc.Resolve(context.Background(), DownloadRequest{
		URL: "https://facebook.com/reel/123",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if res.Platform != PlatformFacebook {
		t.Errorf("result.Platform = %q, want %q", res.Platform, PlatformFacebook)
	}
	if res.URL != "https://facebook.com/reel/123" {
		t.Errorf("result.URL = %q, want the request URL", res.URL)
	}
}

func TestServicePropagatesContextCancellation(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubProvider{
		name:     "cancel",
		platform: "cancel-test",
		resolve: func(ctx context.Context, _ DownloadRequest) (*DownloadResult, error) {
			cctx, cancel := context.WithCancel(ctx)
			cancel()
			return nil, cctx.Err()
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(r)

	_, err := svc.Resolve(context.Background(), DownloadRequest{
		Platform: "cancel-test",
		URL:      "https://x",
	})
	if !stderrors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestServicePropagatesProviderUnavailable(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubProvider{
		name:     "flaky",
		platform: "flaky-test",
		resolve: func(context.Context, DownloadRequest) (*DownloadResult, error) {
			return nil, ErrProviderUnavailable
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(r)

	_, err := svc.Resolve(context.Background(), DownloadRequest{
		Platform: "flaky-test",
		URL:      "https://x",
	})
	if !stderrors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("expected ErrProviderUnavailable, got %v", err)
	}
}

func TestServiceNormalizesPartialResult(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubProvider{
		name:     "partial",
		platform: "partial-test",
		resolve: func(_ context.Context, _ DownloadRequest) (*DownloadResult, error) {

			return &DownloadResult{Title: "Only a title"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(r)

	res, err := svc.Resolve(context.Background(), DownloadRequest{
		Platform: "partial-test",
		URL:      "https://x",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if res.Platform != "partial-test" {
		t.Errorf("result.Platform = %q, want partial-test", res.Platform)
	}
	if res.URL != "https://x" {
		t.Errorf("result.URL = %q, want https://x", res.URL)
	}
	if res.Title != "Only a title" {
		t.Errorf("result.Title = %q, want Only a title", res.Title)
	}
}

func TestServiceHandlesNilResult(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubProvider{
		name:     "nil-result",
		platform: "nil-result-test",
		resolve: func(context.Context, DownloadRequest) (*DownloadResult, error) {
			return nil, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(r)

	res, err := svc.Resolve(context.Background(), DownloadRequest{
		Platform: "nil-result-test",
		URL:      "https://x",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if res == nil {
		t.Fatal("expected a non-nil normalized result")
	}
	if res.Platform != "nil-result-test" || res.URL != "https://x" {
		t.Errorf("result = %+v, want platform/url filled", res)
	}
}

func TestServiceSupportedPlatforms(t *testing.T) {
	r := NewRegistry()
	for _, platform := range []Platform{PlatformFacebook} {
		if err := r.Register(&stubProvider{name: string(platform), platform: platform}); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewService(r)

	got := svc.SupportedPlatforms()
	if len(got) != 1 || got[0] != PlatformFacebook {
		t.Errorf("SupportedPlatforms() = %v, want [facebook]", got)
	}
}

func TestServiceWithCustomResolver(t *testing.T) {

	custom := &stubResolver{platform: PlatformFacebook}
	svc := NewServiceWithResolver(custom, NewRegistry())

	_, err := svc.Resolve(context.Background(), DownloadRequest{URL: "https://x"})
	if !stderrors.Is(err, ErrPlatformUnsupported) {
		t.Fatalf("expected ErrPlatformUnsupported from empty registry, got %v", err)
	}
	if custom.called == 0 {
		t.Error("custom resolver was not invoked")
	}
}

type stubResolver struct {
	platform Platform
	called   int
}

func (s *stubResolver) ResolvePlatform(context.Context, DownloadRequest) (Platform, error) {
	s.called++
	return s.platform, nil
}
