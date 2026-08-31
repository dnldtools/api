package downloader

import (
	"context"
	stderrors "errors"
	"testing"
)

func TestDownloadRequestValidate(t *testing.T) {
	cases := []struct {
		name    string
		req     DownloadRequest
		wantErr error
	}{
		{"valid with platform", DownloadRequest{URL: "https://x", Platform: PlatformFacebook}, nil},
		{"valid without platform", DownloadRequest{URL: "https://x"}, nil},
		{"missing url", DownloadRequest{URL: "", Platform: PlatformFacebook}, ErrInvalidURL},
		{"whitespace url", DownloadRequest{URL: "   "}, ErrInvalidURL},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if !stderrors.Is(err, tc.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestProviderTypeValues(t *testing.T) {
	values := map[ProviderType]bool{
		ProviderNative:      true,
		ProviderBrowser:     true,
		ProviderExternalAPI: true,
	}
	if len(values) != 3 {
		t.Errorf("expected 3 distinct provider types, got %d", len(values))
	}
	for _, ptype := range []ProviderType{ProviderNative, ProviderBrowser, ProviderExternalAPI} {
		if ptype == "" {
			t.Error("provider type must not be empty")
		}
	}
}

func TestBrowserCapableInjectionPattern(t *testing.T) {
	rt := &fakeBrowserRuntime{name: "playwright"}

	p := &browserCapableProvider{}
	p.stubProvider = stubProvider{name: "facebook", platform: PlatformFacebook, ptype: ProviderBrowser}

	bc, ok := interface{}(p).(BrowserCapable)
	if !ok {
		t.Fatal("browser-capable provider should implement BrowserCapable")
	}
	bc.SetBrowserRuntime(rt)

	if p.runtime != rt {
		t.Error("runtime was not injected")
	}
	if p.runtime.Name() != "playwright" {
		t.Errorf("runtime name = %q, want playwright", p.runtime.Name())
	}
}

func TestBrowserRuntimeFetchContract(t *testing.T) {
	rt := &fakeBrowserRuntime{
		name: "playwright",
		fetch: func(ctx context.Context, url string) (string, error) {
			if url == "" {
				return "", context.Canceled
			}
			return "<html></html>", nil
		},
	}

	got, err := rt.Fetch(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got != "<html></html>" {
		t.Errorf("Fetch() = %q, want rendered HTML", got)
	}
	if _, err := rt.Fetch(context.Background(), ""); !stderrors.Is(err, context.Canceled) {
		t.Errorf("Fetch(empty) error = %v, want context.Canceled", err)
	}
}

func TestURLMatcherCapability(t *testing.T) {
	p := &stubProvider{name: "facebook", platform: PlatformFacebook, matches: func(url string) bool {
		return url == "https://facebook.com/reel/123"
	}}

	m, ok := interface{}(p).(URLMatcher)
	if !ok {
		t.Fatal("stub provider should implement URLMatcher")
	}
	if !m.MatchesURL("https://facebook.com/reel/123") {
		t.Error("MatchesURL() = false, want true")
	}
	if m.MatchesURL("https://example.com") {
		t.Error("MatchesURL() = true, want false")
	}
}
