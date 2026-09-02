package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/tiktok"
)

func registryWithTikTok(t *testing.T, relayURL string) *downloader.Service {
	t.Helper()
	registry := downloader.NewRegistry()
	for _, p := range []downloader.Provider{
		tiktok.NewWithConfig(tiktok.Config{RelayBaseURL: relayURL, OfficialBaseURL: relayURL}),
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
	pu, err := url.Parse(envelope.Data.Formats[0].URL)
	if err != nil {
		t.Fatalf("parse format url: %v", err)
	}
	if pu.Scheme+"://"+pu.Host != "http://example.com" {
		t.Errorf("format url base = %q, want http://example.com", pu.Scheme+"://"+pu.Host)
	}
	if pu.Path != "/v1/downloads/proxy" {
		t.Errorf("format url path = %q, want /v1/downloads/proxy", pu.Path)
	}
	if got := pu.Query().Get("url"); got != "https://www.tiktok.com/@alice/video/1234567890" {
		t.Errorf("format url url param = %q", got)
	}
	if got := pu.Query().Get("type"); got != "video" {
		t.Errorf("format url type param = %q, want video", got)
	}
	if got := pu.Query().Get("index"); got != "0" {
		t.Errorf("format url index param = %q, want 0", got)
	}
}

func TestDownloadTikTokRewritesFormatsToProxyURLs(t *testing.T) {
	item := map[string]interface{}{
		"id":     "1234567890",
		"desc":   "Slideshow",
		"author": map[string]interface{}{"uniqueId": "alice"},
		"video": map[string]interface{}{
			"cover": "https://cdn.example.com/cover.jpg",
			"bitrateInfo": []interface{}{
				map[string]interface{}{
					"PlayAddr": map[string]interface{}{"UrlList": []interface{}{"https://cdn.example.com/v.mp4"}},
				},
			},
		},
		"imagePost": map[string]interface{}{
			"images": []interface{}{
				map[string]interface{}{"imageURL": map[string]interface{}{"urlList": []interface{}{"https://cdn.example.com/1.jpg"}}},
				map[string]interface{}{"imageURL": map[string]interface{}{"urlList": []interface{}{"https://cdn.example.com/2.jpg"}}},
			},
		},
		"music": map[string]interface{}{"playUrl": "https://cdn.example.com/m.mp3"},
	}
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rehydrationPageHTML(item)))
	}))
	defer relay.Close()

	router := newRouterWithService(t, registryWithTikTok(t, relay.URL))
	rec := postDownload(t, router, testAPIKey, `{"platform":"tiktok","url":"https://www.tiktok.com/@alice/video/1234567890"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var envelope struct {
		Data struct {
			Formats []struct {
				Type string `json:"type"`
				URL  string `json:"url"`
			} `json:"formats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	want := []struct{ typ, index string }{
		{"video", "0"},
		{"image", "0"},
		{"image", "1"},
		{"audio", "0"},
	}
	if len(envelope.Data.Formats) != len(want) {
		t.Fatalf("len(data.formats) = %d, want %d", len(envelope.Data.Formats), len(want))
	}
	for i, w := range want {
		f := envelope.Data.Formats[i]
		if f.Type != w.typ {
			t.Errorf("formats[%d].type = %q, want %q", i, f.Type, w.typ)
		}
		u, err := url.Parse(f.URL)
		if err != nil {
			t.Fatalf("formats[%d].url parse: %v", i, err)
		}
		if u.Path != "/v1/downloads/proxy" {
			t.Errorf("formats[%d] path = %q, want /v1/downloads/proxy", i, u.Path)
		}
		if got := u.Query().Get("url"); got != "https://www.tiktok.com/@alice/video/1234567890" {
			t.Errorf("formats[%d] url param = %q", i, got)
		}
		if got := u.Query().Get("type"); got != w.typ {
			t.Errorf("formats[%d] type param = %q, want %q", i, got, w.typ)
		}
		if got := u.Query().Get("index"); got != w.index {
			t.Errorf("formats[%d] index param = %q, want %q", i, got, w.index)
		}
	}
}

func TestDownloadTikTokUsesConfiguredPublicBaseURL(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(tiktokRehydrationHTML("Reel", "alice", "https://cdn.example.com/t.jpg", "https://cdn.example.com/v.mp4")))
	}))
	defer relay.Close()

	router := newRouterWithServiceAndBase(t, registryWithTikTok(t, relay.URL), "https://api.dnld.app/")
	rec := postDownload(t, router, testAPIKey, `{"platform":"tiktok","url":"https://www.tiktok.com/@alice/video/1234567890"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var envelope struct {
		Data struct {
			Formats []struct {
				URL string `json:"url"`
			} `json:"formats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(envelope.Data.Formats) == 0 {
		t.Fatal("no formats returned")
	}
	u, err := url.Parse(envelope.Data.Formats[0].URL)
	if err != nil {
		t.Fatalf("parse format url: %v", err)
	}
	if got := u.Scheme + "://" + u.Host; got != "https://api.dnld.app" {
		t.Errorf("format url base = %q, want https://api.dnld.app", got)
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

func TestDownloadProxyStreamsMedia(t *testing.T) {
	var gotReferer, gotOrigin string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "media") {
			gotReferer = r.Header.Get("Referer")
			gotOrigin = r.Header.Get("Origin")
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Content-Length", "4")
			_, _ = w.Write([]byte("DATA"))
			return
		}
		_, _ = w.Write([]byte(tiktokRehydrationHTML("Reel", "alice", "https://cdn.example.com/t.jpg", "http://"+r.Host+"/media.mp4")))
	}))
	defer relay.Close()

	u, _ := url.Parse(relay.URL)
	registry := downloader.NewRegistry()
	if err := registry.Register(tiktok.NewWithConfig(tiktok.Config{
		RelayBaseURL:      relay.URL,
		OfficialBaseURL:   relay.URL,
		AllowedMediaHosts: []string{u.Hostname()},
	})); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	router := newRouterWithService(t, downloader.NewService(registry))

	req := httptest.NewRequest(http.MethodGet, "/v1/downloads/proxy?url="+url.QueryEscape("https://www.tiktok.com/@alice/video/1234567890")+"&type=video", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	if rec.Body.String() != "DATA" {
		t.Errorf("body = %q, want DATA", rec.Body.String())
	}
	if gotReferer != "https://www.tiktok.com/" {
		t.Errorf("upstream Referer = %q, want https://www.tiktok.com/", gotReferer)
	}
	if gotOrigin != "https://www.tiktok.com" {
		t.Errorf("upstream Origin = %q, want https://www.tiktok.com", gotOrigin)
	}
}

func tiktokRehydrationHTML(desc, author, cover, videoURL string) string {
	return rehydrationPageHTML(map[string]interface{}{
		"id":     "1234567890",
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
	})
}

func rehydrationPageHTML(item map[string]interface{}) string {
	root := map[string]interface{}{
		"__DEFAULT_SCOPE__": map[string]interface{}{
			"webapp.video-detail": map[string]interface{}{
				"itemInfo": map[string]interface{}{
					"itemStruct": item,
				},
			},
		},
	}
	b, _ := json.Marshal(root)
	return `<script id="__UNIVERSAL_DATA_FOR_REHYDRATION__" type="application/json">` + string(b) + `</script>` + strings.Repeat("x", 300)
}
