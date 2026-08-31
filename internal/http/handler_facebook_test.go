package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/facebook"
)

func registryWithFacebook(t *testing.T, upstreamURL string) *downloader.Service {
	t.Helper()
	registry := downloader.NewRegistry()
	for _, p := range []downloader.Provider{
		facebook.NewWithConfig(facebook.Config{BaseURL: upstreamURL}),
	} {
		if err := registry.Register(p); err != nil {
			t.Fatalf("register provider: %v", err)
		}
	}
	return downloader.NewService(registry)
}

func TestDownloadFacebookProviderThroughHandler(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>
<h3 class="result-title">Facebook Reel</h3>
<div class="text-sm">1080p</div><div class="text-xs">video</div><a href="https://cdn.example.com/v.mp4">Download</a>
</body></html>`))
	}))
	defer upstream.Close()

	router := newRouterWithService(t, registryWithFacebook(t, upstream.URL))

	rec := postDownload(t, router, testAPIKey, `{"platform":"facebook","url":"https://www.facebook.com/reel/123"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			Platform string `json:"platform"`
			URL      string `json:"url"`
			Title    string `json:"title"`
			Formats  []struct {
				Type    string `json:"type"`
				URL     string `json:"url"`
				Quality string `json:"quality"`
			} `json:"formats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !envelope.Success {
		t.Error("success = false, want true")
	}
	if envelope.Data.Platform != "facebook" {
		t.Errorf("data.platform = %q, want facebook", envelope.Data.Platform)
	}
	if envelope.Data.URL != "https://www.facebook.com/reel/123" {
		t.Errorf("data.url = %q, want requested URL", envelope.Data.URL)
	}
	if envelope.Data.Title != "Facebook Reel" {
		t.Errorf("data.title = %q, want Facebook Reel", envelope.Data.Title)
	}
	if len(envelope.Data.Formats) != 1 {
		t.Fatalf("len(data.formats) = %d, want 1", len(envelope.Data.Formats))
	}
	if envelope.Data.Formats[0].URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("data.formats[0].url = %q, want https://cdn.example.com/v.mp4", envelope.Data.Formats[0].URL)
	}
}

func TestDownloadFacebookUpstreamUnavailableThroughHandler(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	router := newRouterWithService(t, registryWithFacebook(t, upstream.URL))

	rec := postDownload(t, router, testAPIKey, `{"platform":"facebook","url":"https://www.facebook.com/reel/123"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if got := errCode(t, rec.Body.Bytes()); got != "PROVIDER_UNAVAILABLE" {
		t.Errorf("error.code = %q, want PROVIDER_UNAVAILABLE", got)
	}
}
