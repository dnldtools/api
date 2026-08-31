package shopee

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"rest-api/internal/downloader"
)

const testShopeeURL = "https://shopee.co.id/product/12345/67890"

func TestProviderContract(t *testing.T) {
	p := New()
	if p.Name() != "shopee" {
		t.Errorf("Name() = %q, want shopee", p.Name())
	}
	if p.Platform() != downloader.PlatformShopee {
		t.Errorf("Platform() = %q, want shopee", p.Platform())
	}
	if p.Type() != downloader.ProviderExternalAPI {
		t.Errorf("Type() = %q, want external_api", p.Type())
	}
}

func TestMatchesURL(t *testing.T) {
	p := New()
	for _, u := range []string{
		"https://shopee.co.id/product/123/456",
		"https://shopee.com.my/product/123/456",
		"https://shopee.ph/product/123/456",
		"https://shopee.sg/product/123/456",
		"https://shp.ee/abc123",
		"http://shopee.co.id/product/123/456",
	} {
		if !p.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false, want true", u)
		}
	}
	for _, u := range []string{
		"https://www.tiktok.com/@alice/video/123",
		"https://www.instagram.com/p/abc/",
		"https://example.com/shopee.co.id/",
	} {
		if p.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true, want false", u)
		}
	}
}

func TestResolveEmptyURL(t *testing.T) {
	p := New()
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: ""})
	if !stderrors.Is(err, downloader.ErrInvalidURL) {
		t.Fatalf("error = %v, want ErrInvalidURL", err)
	}
}

func TestResolveNonShopeeURL(t *testing.T) {
	p := New()
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://www.tiktok.com/@alice/video/123"})
	if !stderrors.Is(err, downloader.ErrInvalidURL) {
		t.Fatalf("error = %v, want ErrInvalidURL", err)
	}
}

func TestResolveNowatermarkMergesOfficial(t *testing.T) {
	p, capture := newShopeeEnv(t)

	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testShopeeURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got := capture.url(); got != testShopeeURL {
		t.Errorf("extract form url = %q, want %q", got, testShopeeURL)
	}
	if result.Platform != downloader.PlatformShopee {
		t.Errorf("Platform = %q, want shopee", result.Platform)
	}
	if result.URL != testShopeeURL {
		t.Errorf("URL = %q, want %q", result.URL, testShopeeURL)
	}
	if result.Title != "Shopee Product" {
		t.Errorf("Title = %q, want Shopee Product (merged from official)", result.Title)
	}
	if result.Thumbnail != "https://cdn.example.com/cover.jpg" {
		t.Errorf("Thumbnail = %q, want cover.jpg (merged from official)", result.Thumbnail)
	}
	if result.Type != downloader.MediaVideo {
		t.Errorf("Type = %q, want video", result.Type)
	}
	if len(result.Formats) != 2 {
		t.Fatalf("len(Formats) = %d, want 2", len(result.Formats))
	}
	if result.Formats[0].Quality != "no_watermark" || result.Formats[0].URL != "https://cdn.example.com/nw.mp4" {
		t.Errorf("Formats[0] = %+v, want no_watermark nw.mp4", result.Formats[0])
	}
	if result.Formats[1].Quality != "watermark" || result.Formats[1].URL != "https://cdn.example.com/wm2.mp4" {
		t.Errorf("Formats[1] = %+v, want watermark wm2.mp4", result.Formats[1])
	}
	if got := result.Metadata["author"]; got != "alice" {
		t.Errorf("Metadata author = %q, want alice", got)
	}
	if got := result.Metadata["like_count"]; got != "10" {
		t.Errorf("Metadata like_count = %q, want 10", got)
	}
}

func TestResolveNowatermarkWithoutOfficial(t *testing.T) {
	capture := &extractCapture{}

	var fakeProxy *httptest.Server
	fakeProxy = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Host {
		case "probe.invalid":
			_, _ = w.Write([]byte(`{"origin":"1.2.3.4"}`))
		case "extract.invalid":
			_ = r.ParseForm()
			capture.set(r.FormValue("url"))
			_, _ = w.Write([]byte(`{"success":true,"data":{"no_watermark":"https://cdn.example.com/nw.mp4"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer fakeProxy.Close()

	proxyPort := strings.TrimPrefix(fakeProxy.URL, "http://")
	proxyList := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(proxyListHTML(proxyPort)))
	}))
	defer proxyList.Close()

	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer official.Close()

	p := NewWithConfig(Config{
		ProxyListURL:   proxyList.URL,
		ProbeURL:       "http://probe.invalid/ip",
		ExtractURL:     "http://extract.invalid/api/extract",
		MaxValidate:    1,
		MaxLiveExtract: 1,
		ProbeBatch:     1,
		HTTPClient:     &http.Client{Timeout: 30 * time.Second, Transport: shopeeRewriteTransport(t, official.URL)},
	})

	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testShopeeURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Title != "" {
		t.Errorf("Title = %q, want empty (no official metadata)", result.Title)
	}
	if result.Metadata != nil {
		t.Errorf("Metadata = %v, want nil", result.Metadata)
	}
	if len(result.Formats) != 1 || result.Formats[0].URL != "https://cdn.example.com/nw.mp4" {
		t.Fatalf("Formats = %+v, want nw.mp4", result.Formats)
	}
}

func TestResolveOfficialFallback(t *testing.T) {
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte(nextDataHTML("Watermarked", "bob", "https://cdn.example.com/c.jpg", "https://cdn.example.com/wm.mp4")))
	}))
	defer official.Close()

	proxyList := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body></body></html>"))
	}))
	defer proxyList.Close()

	p := NewWithConfig(Config{
		ProxyListURL:   proxyList.URL,
		MaxValidate:    1,
		MaxLiveExtract: 1,
		ProbeBatch:     1,
		HTTPClient:     &http.Client{Timeout: 30 * time.Second, Transport: shopeeRewriteTransport(t, official.URL)},
	})

	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testShopeeURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Title != "Watermarked" {
		t.Errorf("Title = %q, want Watermarked", result.Title)
	}
	if len(result.Formats) != 1 || result.Formats[0].Quality != "watermark" || result.Formats[0].URL != "https://cdn.example.com/wm.mp4" {
		t.Fatalf("Formats = %+v, want watermark wm.mp4", result.Formats)
	}
	if got := result.Metadata["author"]; got != "bob" {
		t.Errorf("Metadata author = %q, want bob", got)
	}
}

func TestResolveBothFailMediaNotFound(t *testing.T) {
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte(nextDataHTML("No video", "bob", "https://cdn.example.com/c.jpg", "")))
	}))
	defer official.Close()

	proxyList := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body></body></html>"))
	}))
	defer proxyList.Close()

	p := NewWithConfig(Config{
		ProxyListURL:   proxyList.URL,
		MaxValidate:    1,
		MaxLiveExtract: 1,
		ProbeBatch:     1,
		HTTPClient:     &http.Client{Timeout: 30 * time.Second, Transport: shopeeRewriteTransport(t, official.URL)},
	})

	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testShopeeURL})
	if !stderrors.Is(err, downloader.ErrMediaNotFound) {
		t.Fatalf("error = %v, want ErrMediaNotFound", err)
	}
}

func TestResolveLocationRedirectAndRedir(t *testing.T) {
	var gotPath string
	var mu sync.Mutex

	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/first":
			http.Redirect(w, r, "https://shopee.co.id/redir?redir="+url.QueryEscape("https://shopee.co.id/video/123"), http.StatusFound)
		case r.Method == http.MethodHead && r.URL.Path == "/redir":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet:
			mu.Lock()
			gotPath = r.URL.Path
			mu.Unlock()
			_, _ = w.Write([]byte(nextDataHTML("Redir video", "carol", "https://cdn.example.com/r.jpg", "https://cdn.example.com/r.mp4")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer official.Close()

	p := NewWithConfig(Config{
		HTTPClient: &http.Client{Timeout: 30 * time.Second, Transport: shopeeRewriteTransport(t, official.URL)},
	})

	result, err := p.queryOfficial(context.Background(), "https://shopee.co.id/first")
	if err != nil {
		t.Fatalf("queryOfficial() error = %v", err)
	}

	mu.Lock()
	path := gotPath
	mu.Unlock()
	if path != "/video/123" {
		t.Errorf("page GET path = %q, want /video/123 (redir target)", path)
	}
	if result.Title != "Redir video" {
		t.Errorf("Title = %q, want Redir video", result.Title)
	}
	if len(result.Formats) != 1 || result.Formats[0].URL != "https://cdn.example.com/r.mp4" {
		t.Fatalf("Formats = %+v, want r.mp4", result.Formats)
	}
}

func TestResolveTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{
		HTTPClient: &http.Client{Timeout: 50 * time.Millisecond, Transport: shopeeRewriteTransport(t, srv.URL)},
	})

	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testShopeeURL})
	if !stderrors.Is(err, downloader.ErrProviderTimeout) {
		t.Fatalf("error = %v, want ErrProviderTimeout", err)
	}
}

func TestPickMedia(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]interface{}
		want int
	}{
		{
			name: "data with no watermark and watermark",
			raw: map[string]interface{}{
				"data": map[string]interface{}{
					"no_watermark": "https://cdn.example.com/nw.mp4",
					"watermark":    "https://cdn.example.com/wm.mp4",
					"cover":        "https://cdn.example.com/c.jpg",
				},
			},
			want: 3,
		},
		{
			name: "videos array with objects and strings",
			raw: map[string]interface{}{
				"data": map[string]interface{}{
					"videos": []interface{}{
						map[string]interface{}{"quality": "720p", "url": "https://cdn.example.com/v1.mp4"},
						"https://cdn.example.com/v2.mp4",
					},
				},
			},
			want: 2,
		},
		{
			name: "result url",
			raw: map[string]interface{}{
				"result": map[string]interface{}{"url": "https://cdn.example.com/x.mp4"},
			},
			want: 1,
		},
		{
			name: "empty data",
			raw:  map[string]interface{}{"data": map[string]interface{}{}},
			want: 0,
		},
		{
			name: "nil raw",
			raw:  nil,
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickMedia(tc.raw); len(got) != tc.want {
				t.Errorf("pickMedia() len = %d, want %d (%+v)", len(got), tc.want, got)
			}
		})
	}
}

func TestParseNextData(t *testing.T) {
	m := parseNextData(nextDataHTML("T", "a", "https://c", "https://w"))
	if m == nil {
		t.Fatal("parseNextData() = nil, want map")
	}
	if nestedStr(m, "props", "pageProps", "mediaInfo", "video", "caption") != "T" {
		t.Errorf("parsed caption = %q, want T", nestedStr(m, "props", "pageProps", "mediaInfo", "video", "caption"))
	}
	if parseNextData("<html><body>no next data</body></html>") != nil {
		t.Error("parseNextData(missing) = non-nil, want nil")
	}
}

func TestQueryParam(t *testing.T) {
	got := queryParam("https://shopee.co.id/r?redir="+url.QueryEscape("https://shopee.co.id/video/1"), "redir")
	if got != "https://shopee.co.id/video/1" {
		t.Errorf("queryParam(redir) = %q, want decoded redir", got)
	}
	if got := queryParam("https://shopee.co.id/product/1", "redir"); got != "" {
		t.Errorf("queryParam(no redir) = %q, want empty", got)
	}
}

type extractCapture struct {
	mu  sync.Mutex
	val string
}

func (c *extractCapture) set(v string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.val = v
}

func (c *extractCapture) url() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.val
}

func newShopeeEnv(t *testing.T) (*Provider, *extractCapture) {
	t.Helper()

	capture := &extractCapture{}

	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte(nextDataHTML("Shopee Product", "alice", "https://cdn.example.com/cover.jpg", "https://cdn.example.com/wm.mp4")))
	}))
	t.Cleanup(official.Close)

	var fakeProxy *httptest.Server
	fakeProxy = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Host {
		case "probe.invalid":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"origin":"1.2.3.4"}`))
		case "extract.invalid":
			_ = r.ParseForm()
			capture.set(r.FormValue("url"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":{"no_watermark":"https://cdn.example.com/nw.mp4","watermark":"https://cdn.example.com/wm2.mp4"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fakeProxy.Close)

	proxyPort := strings.TrimPrefix(fakeProxy.URL, "http://")

	proxyList := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(proxyListHTML(proxyPort)))
	}))
	t.Cleanup(proxyList.Close)

	p := NewWithConfig(Config{
		ProxyListURL:   proxyList.URL,
		ProbeURL:       "http://probe.invalid/ip",
		ExtractURL:     "http://extract.invalid/api/extract",
		MaxValidate:    2,
		MaxLiveExtract: 1,
		ProbeBatch:     2,
		HTTPClient:     &http.Client{Timeout: 30 * time.Second, Transport: shopeeRewriteTransport(t, official.URL)},
	})

	return p, capture
}

func nextDataHTML(title, author, cover, watermark string) string {
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

func proxyListHTML(proxyPort string) string {
	return `<table><tbody>
			<tr><td>HTTP</td><td>South Korea</td><td><code>` + proxyPort + `</code></td><td>✅</td></tr>
			<tr><td>SOCKS</td><td>Thailand</td><td><code>1.0.0.1:8080</code></td><td>✅</td></tr>
		</tbody></table>`
}

func shopeeRewriteTransport(t *testing.T, target string) http.RoundTripper {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	return &rewriteTransport{target: u}
}

type rewriteTransport struct {
	target *url.URL
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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
