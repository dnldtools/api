package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/instagram"
)

func registryWithInstagram(t *testing.T, relayURL, snapURL string) *downloader.Service {
	t.Helper()
	registry := downloader.NewRegistry()
	for _, p := range []downloader.Provider{
		instagram.NewWithConfig(instagram.Config{RelayBaseURL: relayURL, SnapinstaBaseURL: snapURL}),
	} {
		if err := registry.Register(p); err != nil {
			t.Fatalf("register provider: %v", err)
		}
	}
	return downloader.NewService(registry)
}

func TestDownloadInstagramProviderThroughHandler(t *testing.T) {
	htmlBody := `<html><head><script type="application/json">{"xig_polaris_media":{"code":"abc12","caption":"Instagram Reel","user":{"username":"alice"},"media_type":2,"video_url":"https:\/\/cdn.example.com\/v.mp4","display_url":"https:\/\/cdn.example.com\/t.jpg"}}</script></head><body></body></html>`

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer relay.Close()

	router := newRouterWithService(t, registryWithInstagram(t, relay.URL, relay.URL))

	rec := postDownload(t, router, testAPIKey, `{"platform":"instagram","url":"https://www.instagram.com/p/abc12/"}`)

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
				Type string `json:"type"`
				URL  string `json:"url"`
			} `json:"formats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !envelope.Success {
		t.Error("success = false, want true")
	}
	if envelope.Data.Platform != "instagram" {
		t.Errorf("data.platform = %q, want instagram", envelope.Data.Platform)
	}
	if envelope.Data.URL != "https://www.instagram.com/p/abc12/" {
		t.Errorf("data.url = %q, want requested URL", envelope.Data.URL)
	}
	if envelope.Data.Title != "Instagram Reel" {
		t.Errorf("data.title = %q, want Instagram Reel", envelope.Data.Title)
	}
	if len(envelope.Data.Formats) != 1 {
		t.Fatalf("len(data.formats) = %d, want 1", len(envelope.Data.Formats))
	}
	if envelope.Data.Formats[0].URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("data.formats[0].url = %q, want https://cdn.example.com/v.mp4", envelope.Data.Formats[0].URL)
	}
}

func TestDownloadInstagramMediaNotFoundThroughHandler(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer relay.Close()

	snap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte(`<html><input type="hidden" name="token" value="t"></html>`))
			return
		}
		_, _ = w.Write([]byte(evalPayload(strings.Repeat("no tokens ", 20))))
	}))
	defer snap.Close()

	router := newRouterWithService(t, registryWithInstagram(t, relay.URL, snap.URL))

	rec := postDownload(t, router, testAPIKey, `{"platform":"instagram","url":"https://www.instagram.com/p/abc12/"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if got := errCode(t, rec.Body.Bytes()); got != "MEDIA_NOT_FOUND" {
		t.Errorf("error.code = %q, want MEDIA_NOT_FOUND", got)
	}
}

func TestDownloadInstagramUpstreamUnavailableThroughHandler(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer relay.Close()

	snap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer snap.Close()

	router := newRouterWithService(t, registryWithInstagram(t, relay.URL, snap.URL))

	rec := postDownload(t, router, testAPIKey, `{"platform":"instagram","url":"https://www.instagram.com/p/abc12/"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if got := errCode(t, rec.Body.Bytes()); got != "PROVIDER_UNAVAILABLE" {
		t.Errorf("error.code = %q, want PROVIDER_UNAVAILABLE", got)
	}
}

func evalPayload(inner string) string {
	b, _ := json.Marshal(inner)
	return `eval(function(h,u,n,t,e,r){return ` + string(b) + `;}('x','y'))`
}
