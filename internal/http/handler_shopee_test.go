package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/shopee"
)

const testShopeeURL = "https://shopee.co.id/product/12345/67890"

func registryWithShopee(t *testing.T, cfg shopee.Config) *downloader.Service {
	t.Helper()
	registry := downloader.NewRegistry()
	for _, p := range []downloader.Provider{shopee.NewWithConfig(cfg)} {
		if err := registry.Register(p); err != nil {
			t.Fatalf("register provider: %v", err)
		}
	}
	return downloader.NewService(registry)
}

func TestDownloadShopeeProviderThroughHandler(t *testing.T) {
	capture := make(chan string, 1)

	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte(shopeeNextDataHTML("Shopee Reel", "alice", "https://cdn.example.com/cover.jpg", "https://cdn.example.com/wm.mp4")))
	}))
	defer official.Close()

	var fakeProxy *httptest.Server
	fakeProxy = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Host {
		case "probe.invalid":
			_, _ = w.Write([]byte(`{"origin":"1.2.3.4"}`))
		case "extract.invalid":
			_ = r.ParseForm()
			capture <- r.FormValue("url")
			_, _ = w.Write([]byte(`{"success":true,"data":{"no_watermark":"https://cdn.example.com/nw.mp4"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer fakeProxy.Close()

	proxyPort := strings.TrimPrefix(fakeProxy.URL, "http://")
	proxyList := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(shopeeProxyListHTML(proxyPort)))
	}))
	defer proxyList.Close()

	router := newRouterWithService(t, registryWithShopee(t, shopee.Config{
		ProxyListURL:   proxyList.URL,
		ProbeURL:       "http://probe.invalid/ip",
		ExtractURL:     "http://extract.invalid/api/extract",
		MaxValidate:    1,
		MaxLiveExtract: 1,
		ProbeBatch:     1,
		HTTPClient:     &http.Client{Timeout: 30 * time.Second, Transport: shopeeHostRewrite(t, official.URL)},
	}))

	rec := postDownload(t, router, testAPIKey, `{"platform":"shopee","url":"`+testShopeeURL+`"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := <-capture; got != testShopeeURL {
		t.Errorf("extract form url = %q, want %q", got, testShopeeURL)
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
			Metadata map[string]string `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !envelope.Success {
		t.Error("success = false, want true")
	}
	if envelope.Data.Platform != "shopee" {
		t.Errorf("data.platform = %q, want shopee", envelope.Data.Platform)
	}
	if envelope.Data.URL != testShopeeURL {
		t.Errorf("data.url = %q, want %q", envelope.Data.URL, testShopeeURL)
	}
	if envelope.Data.Title != "Shopee Reel" {
		t.Errorf("data.title = %q, want Shopee Reel", envelope.Data.Title)
	}
	if len(envelope.Data.Formats) != 1 {
		t.Fatalf("len(data.formats) = %d, want 1", len(envelope.Data.Formats))
	}
	if envelope.Data.Formats[0].URL != "https://cdn.example.com/nw.mp4" {
		t.Errorf("data.formats[0].url = %q, want nw.mp4", envelope.Data.Formats[0].URL)
	}
	if got := envelope.Data.Metadata["author"]; got != "alice" {
		t.Errorf("data.metadata.author = %q, want alice", got)
	}
}

func TestDownloadShopeeMediaNotFoundThroughHandler(t *testing.T) {
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte(shopeeNextDataHTML("No video", "alice", "https://cdn.example.com/c.jpg", "")))
	}))
	defer official.Close()

	proxyList := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body></body></html>"))
	}))
	defer proxyList.Close()

	router := newRouterWithService(t, registryWithShopee(t, shopee.Config{
		ProxyListURL:   proxyList.URL,
		MaxValidate:    1,
		MaxLiveExtract: 1,
		ProbeBatch:     1,
		HTTPClient:     &http.Client{Timeout: 30 * time.Second, Transport: shopeeHostRewrite(t, official.URL)},
	}))

	rec := postDownload(t, router, testAPIKey, `{"platform":"shopee","url":"`+testShopeeURL+`"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if got := errCode(t, rec.Body.Bytes()); got != "MEDIA_NOT_FOUND" {
		t.Errorf("error.code = %q, want MEDIA_NOT_FOUND", got)
	}
}

func TestDownloadShopeeUpstreamUnavailableThroughHandler(t *testing.T) {
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer official.Close()

	proxyList := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer proxyList.Close()

	router := newRouterWithService(t, registryWithShopee(t, shopee.Config{
		ProxyListURL:   proxyList.URL,
		MaxValidate:    1,
		MaxLiveExtract: 1,
		ProbeBatch:     1,
		HTTPClient:     &http.Client{Timeout: 30 * time.Second, Transport: shopeeHostRewrite(t, official.URL)},
	}))

	rec := postDownload(t, router, testAPIKey, `{"platform":"shopee","url":"`+testShopeeURL+`"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if got := errCode(t, rec.Body.Bytes()); got != "PROVIDER_UNAVAILABLE" {
		t.Errorf("error.code = %q, want PROVIDER_UNAVAILABLE", got)
	}
}

func shopeeNextDataHTML(title, author, cover, watermark string) string {
	next := map[string]interface{}{
		"props": map[string]interface{}{
			"pageProps": map[string]interface{}{
				"mediaInfo": map[string]interface{}{
					"userInfo": map[string]interface{}{"videoUserName": author},
					"count":    map[string]interface{}{"likeCount": float64(10), "commentCount": float64(3)},
					"video": map[string]interface{}{
						"caption":           title,
						"watermarkCoverUrl": cover,
						"watermarkVideoUrl": watermark,
					},
				},
			},
		},
	}
	b, _ := json.Marshal(next)
	return `<script id="__NEXT_DATA__" type="application/json">` + string(b) + `</script>` + strings.Repeat("x", 200)
}

func shopeeProxyListHTML(proxyPort string) string {
	return `<table><tbody>
			<tr><td>HTTP</td><td>South Korea</td><td><code>` + proxyPort + `</code></td><td>✅</td></tr>
		</tbody></table>`
}

func shopeeHostRewrite(t *testing.T, target string) http.RoundTripper {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	return &shopeeRewrite{target: u}
}

type shopeeRewrite struct {
	target *url.URL
}

func (rt *shopeeRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if strings.Contains(host, "shopee.") || strings.Contains(host, "shp.ee") {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = rt.target.Scheme
		clone.URL.Host = rt.target.Host
		clone.Host = rt.target.Host
		return http.DefaultTransport.RoundTrip(clone)
	}
	return http.DefaultTransport.RoundTrip(req)
}
