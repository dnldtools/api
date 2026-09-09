package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/apple"
)

func registryWithApple(t *testing.T, aaplURL, aplmateURL string) *downloader.Service {
	t.Helper()
	registry := downloader.NewRegistry()
	if err := registry.Register(apple.NewWithConfig(apple.Config{
		AaplBaseURL:    aaplURL,
		AplmateBaseURL: aplmateURL,
	})); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	return downloader.NewService(registry)
}

func TestDownloadAppleProviderThroughHandler(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" || r.URL.Path == "":
			_, _ = w.Write([]byte("ok"))
		case strings.HasPrefix(r.URL.Path, "/api/pl.php"):
			_, _ = w.Write([]byte(`{"album_details":{"album":"Rodecia - Single","artist":"Kangen Band","thumb":"https://th.example/t.jpg","count":1,"0":{"link":"https://music.apple.com/id/album/rodecia/6790279558?i=6790279559","name":"Rodecia","artist":"Kangen Band","duration":"4m 11s","thumb":"https://th.example/t.jpg","album":"Rodecia - Single"}}}`))
		case r.URL.Path == "/api/composer/swd.php":
			_, _ = w.Write([]byte(`{"status":"success","dlink":"https://mymp3.xyz/phmp4?fname=Rodecia.m4a"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	router := newRouterWithService(t, registryWithApple(t, upstream.URL, upstream.URL+"/nope"))
	rec := postDownload(t, router, testAPIKey, `{"platform":"apple","url":"https://music.apple.com/id/album/rodecia-single/6790279558"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			Platform string `json:"platform"`
			URL      string `json:"url"`
			Title    string `json:"title"`
			Type     string `json:"type"`
			Formats  []struct {
				Type string `json:"type"`
				URL  string `json:"url"`
				Ext  string `json:"ext"`
			} `json:"formats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !envelope.Success {
		t.Error("success = false")
	}
	if envelope.Data.Platform != "apple" {
		t.Errorf("platform = %q", envelope.Data.Platform)
	}
	if envelope.Data.Type != "audio" {
		t.Errorf("type = %q", envelope.Data.Type)
	}
	if len(envelope.Data.Formats) != 1 || envelope.Data.Formats[0].URL != "https://mymp3.xyz/phmp4?fname=Rodecia.m4a" {
		t.Fatalf("formats = %+v", envelope.Data.Formats)
	}
}

func TestDownloadAppleUpstreamUnavailableThroughHandler(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	router := newRouterWithService(t, registryWithApple(t, upstream.URL, upstream.URL))
	rec := postDownload(t, router, testAPIKey, `{"platform":"apple","url":"https://music.apple.com/id/album/rodecia-single/6790279558"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}
