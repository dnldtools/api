package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/tiktok"
)

func registryWithTikTok(t *testing.T, relayURL string) *downloader.Service {
	t.Helper()
	registry := downloader.NewRegistry()
	for _, p := range []downloader.Provider{
		tiktok.NewWithConfig(tiktok.Config{RelayBaseURL: relayURL}),
	} {
		if err := registry.Register(p); err != nil {
			t.Fatalf("register provider: %v", err)
		}
	}
	return downloader.NewService(registry)
}

func TestDownloadTikTokProviderThroughHandler(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(tiktokRehydrationHTML("TikTok Reel", "alice", "https://cdn.example.com/t.jpg", "https://cdn.example.com/v.mp4")))
	}))
	defer relay.Close()

	router := newRouterWithService(t, registryWithTikTok(t, relay.URL))

	rec := postDownload(t, router, testAPIKey, `{"platform":"tiktok","url":"https://www.tiktok.com/@alice/video/1234567890"}`)

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
	if envelope.Data.Platform != "tiktok" {
		t.Errorf("data.platform = %q, want tiktok", envelope.Data.Platform)
	}
	if envelope.Data.URL != "https://www.tiktok.com/@alice/video/1234567890" {
		t.Errorf("data.url = %q, want requested URL", envelope.Data.URL)
	}
	if envelope.Data.Title != "TikTok Reel" {
		t.Errorf("data.title = %q, want TikTok Reel", envelope.Data.Title)
	}
	if len(envelope.Data.Formats) != 1 {
		t.Fatalf("len(data.formats) = %d, want 1", len(envelope.Data.Formats))
	}
	if envelope.Data.Formats[0].URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("data.formats[0].url = %q, want https://cdn.example.com/v.mp4", envelope.Data.Formats[0].URL)
	}
}

func TestDownloadTikTokMediaNotFoundThroughHandler(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "snaptik.net") {
			payload, _ := json.Marshal(map[string]interface{}{
				"status":     "ok",
				"statusCode": float64(200),
				"data":       `<h3>Title</h3><a href="https://other.example.com/x">Nope</a>`,
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("no rehydration here ", 40)))
	}))
	defer relay.Close()

	router := newRouterWithService(t, registryWithTikTok(t, relay.URL))

	rec := postDownload(t, router, testAPIKey, `{"platform":"tiktok","url":"https://www.tiktok.com/@alice/video/1234567890"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if got := errCode(t, rec.Body.Bytes()); got != "MEDIA_NOT_FOUND" {
		t.Errorf("error.code = %q, want MEDIA_NOT_FOUND", got)
	}
}

func TestDownloadTikTokUpstreamUnavailableThroughHandler(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer relay.Close()

	router := newRouterWithService(t, registryWithTikTok(t, relay.URL))

	rec := postDownload(t, router, testAPIKey, `{"platform":"tiktok","url":"https://www.tiktok.com/@alice/video/1234567890"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if got := errCode(t, rec.Body.Bytes()); got != "PROVIDER_UNAVAILABLE" {
		t.Errorf("error.code = %q, want PROVIDER_UNAVAILABLE", got)
	}
}

func tiktokRehydrationHTML(desc, author, cover, videoURL string) string {
	item := map[string]interface{}{
		"__DEFAULT_SCOPE__": map[string]interface{}{
			"webapp.video-detail": map[string]interface{}{
				"itemInfo": map[string]interface{}{
					"itemStruct": map[string]interface{}{
						"desc":   desc,
						"author": map[string]interface{}{"uniqueId": author},
						"video": map[string]interface{}{
							"cover": cover,
							"bitrateInfo": []interface{}{
								map[string]interface{}{
									"PlayAddr": map[string]interface{}{"UrlList": []interface{}{videoURL}},
								},
							},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(item)
	return `<script id="__UNIVERSAL_DATA_FOR_REHYDRATION__" type="application/json">` + string(b) + `</script>` + strings.Repeat("x", 300)
}
