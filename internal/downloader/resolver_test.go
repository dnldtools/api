package downloader

import (
	"context"
	stderrors "errors"
	"testing"
)

func TestResolverUsesExplicitPlatform(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubProvider{name: "facebook", platform: PlatformFacebook}); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(r)

	got, err := resolver.ResolvePlatform(context.Background(), DownloadRequest{
		Platform: PlatformFacebook,
		URL:      "https://facebook.com/reel/123",
	})
	if err != nil {
		t.Fatalf("ResolvePlatform() error = %v", err)
	}
	if got != PlatformFacebook {
		t.Errorf("ResolvePlatform() = %q, want %q", got, PlatformFacebook)
	}
}

func TestResolverExplicitPlatformMustBeRegistered(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubProvider{name: "facebook", platform: PlatformFacebook}); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(r)

	_, err := resolver.ResolvePlatform(context.Background(), DownloadRequest{
		Platform: Platform("unknown"),
		URL:      "https://unknown.example/video/1",
	})
	if !stderrors.Is(err, ErrPlatformUnsupported) {
		t.Errorf("expected ErrPlatformUnsupported, got %v", err)
	}
}

func TestResolverDetectsPlatformViaURLMatcher(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubProvider{
		name:     "facebook",
		platform: PlatformFacebook,
		matches:  func(url string) bool { return len(url) > 0 },
	}); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(r)

	got, err := resolver.ResolvePlatform(context.Background(), DownloadRequest{
		URL: "https://facebook.com/reel/123",
	})
	if err != nil {
		t.Fatalf("ResolvePlatform() error = %v", err)
	}
	if got != PlatformFacebook {
		t.Errorf("ResolvePlatform() = %q, want %q", got, PlatformFacebook)
	}
}

func TestResolverDetectionUsesRegistrationOrder(t *testing.T) {
	r := NewRegistry()

	if err := r.Register(&stubProvider{
		name:     "first",
		platform: PlatformFacebook,
		matches:  func(string) bool { return true },
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(&stubProvider{
		name:     "second",
		platform: Platform("second"),
		matches:  func(string) bool { return true },
	}); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(r)

	got, err := resolver.ResolvePlatform(context.Background(), DownloadRequest{URL: "https://example.com"})
	if err != nil {
		t.Fatalf("ResolvePlatform() error = %v", err)
	}
	if got != PlatformFacebook {
		t.Errorf("ResolvePlatform() = %q, want %q (first registered)", got, PlatformFacebook)
	}
}

func TestResolverNoMatchReturnsUnsupported(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubProvider{name: "facebook", platform: PlatformFacebook}); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(r)

	_, err := resolver.ResolvePlatform(context.Background(), DownloadRequest{
		URL: "https://unknown.example/video",
	})
	if !stderrors.Is(err, ErrPlatformUnsupported) {
		t.Errorf("expected ErrPlatformUnsupported, got %v", err)
	}
}
