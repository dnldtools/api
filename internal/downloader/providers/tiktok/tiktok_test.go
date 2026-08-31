package tiktok

import (
	"context"
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rest-api/internal/downloader"
)

const testVideoURL = "https://www.tiktok.com/@alice/video/1234567890"

const testShortURL = "https://vm.tiktok.com/abc123/"

func TestProviderContract(t *testing.T) {
	p := New()
	if p.Name() != "tiktok" {
		t.Errorf("Name() = %q, want tiktok", p.Name())
	}
	if p.Platform() != downloader.PlatformTikTok {
		t.Errorf("Platform() = %q, want tiktok", p.Platform())
	}
	if p.Type() != downloader.ProviderExternalAPI {
		t.Errorf("Type() = %q, want external_api", p.Type())
	}
}

func TestMatchesURL(t *testing.T) {
	p := New()
	for _, u := range []string{
		"https://www.tiktok.com/@alice/video/123",
		"https://tiktok.com/@alice/video/123",
		"https://vm.tiktok.com/abc/",
		"https://www.douyin.com/video/123",
		"https://v.douyin.com/abc/",
		"https://www.iesdouyin.com/share/video/123",
	} {
		if !p.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false, want true", u)
		}
	}
	for _, u := range []string{
		"https://www.instagram.com/p/abc/",
		"https://www.facebook.com/reel/123",
		"https://example.com/tiktok.com/",
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

func TestResolveNonTikTokURL(t *testing.T) {
	p := New()
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://www.instagram.com/p/abc/"})
	if !stderrors.Is(err, downloader.ErrInvalidURL) {
		t.Fatalf("error = %v, want ErrInvalidURL", err)
	}
}

func TestResolveOfficialVideo(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rehydrationPage(videoItem("Hello world", "https://cdn.example.com/v.mp4"))))
	}))
	defer relay.Close()

	p := NewWithConfig(Config{RelayBaseURL: relay.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testVideoURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Platform != downloader.PlatformTikTok {
		t.Errorf("Platform = %q, want tiktok", result.Platform)
	}
	if result.URL != testVideoURL {
		t.Errorf("URL = %q, want %q", result.URL, testVideoURL)
	}
	if result.Title != "Hello world" {
		t.Errorf("Title = %q, want Hello world", result.Title)
	}
	if result.Thumbnail != "https://cdn.example.com/cover.jpg" {
		t.Errorf("Thumbnail = %q, want cover.jpg", result.Thumbnail)
	}
	if result.Type != downloader.MediaVideo {
		t.Errorf("Type = %q, want video", result.Type)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d, want 1", len(result.Formats))
	}
	if result.Formats[0].URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("Formats[0].URL = %q, want v.mp4", result.Formats[0].URL)
	}
	if got := result.Metadata["author"]; got != "alice" {
		t.Errorf("Metadata author = %q, want alice", got)
	}
}

func TestResolveOfficialPhotoPost(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rehydrationPage(map[string]interface{}{
			"desc":   "Slideshow",
			"author": map[string]interface{}{"nickname": "Alice"},
			"imagePost": map[string]interface{}{
				"images": []interface{}{
					map[string]interface{}{
						"imageURL": map[string]interface{}{
							"urlList": []interface{}{"https://cdn.example.com/p1.jpg"},
						},
					},
					map[string]interface{}{
						"imageURL": map[string]interface{}{
							"urlList": []interface{}{"https://cdn.example.com/p2.jpg"},
						},
					},
				},
			},
		})))
	}))
	defer relay.Close()

	p := NewWithConfig(Config{RelayBaseURL: relay.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testVideoURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(result.Formats) != 2 {
		t.Fatalf("len(Formats) = %d, want 2", len(result.Formats))
	}
	if result.Formats[0].Type != downloader.MediaImage || result.Formats[0].URL != "https://cdn.example.com/p1.jpg" {
		t.Errorf("Formats[0] = %+v, want image p1.jpg", result.Formats[0])
	}
	if result.Formats[1].Type != downloader.MediaImage || result.Formats[1].URL != "https://cdn.example.com/p2.jpg" {
		t.Errorf("Formats[1] = %+v, want image p2.jpg", result.Formats[1])
	}
	if result.Type != downloader.MediaImage {
		t.Errorf("Type = %q, want image", result.Type)
	}
	if result.Thumbnail != "https://cdn.example.com/p1.jpg" {
		t.Errorf("Thumbnail = %q, want p1.jpg (images[0] fallback)", result.Thumbnail)
	}
	if got := result.Metadata["author"]; got != "Alice" {
		t.Errorf("Metadata author = %q, want Alice", got)
	}
}

func TestResolveSnaptik(t *testing.T) {
	token := fakeJWT(`{"url":"https://cdn.example.com/v.mp4"}`)

	var got struct {
		q    string
		lang string
	}

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "snaptik.net") {
			_ = r.ParseForm()
			got.q = r.FormValue("q")
			got.lang = r.FormValue("lang")

			inner := `<h3>Fallback title</h3><img src="https://snapcdn.app/cover.jpg"><a href="https://snapcdn.app/v1?token=` + token + `">Download MP4 HD</a>`
			payload, _ := json.Marshal(map[string]interface{}{
				"status":     "ok",
				"statusCode": float64(200),
				"data":       inner,
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("no rehydration here ", 40)))
	}))
	defer relay.Close()

	p := NewWithConfig(Config{RelayBaseURL: relay.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testVideoURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got.q != testVideoURL {
		t.Errorf("form q = %q, want %q", got.q, testVideoURL)
	}
	if got.lang != "en" {
		t.Errorf("form lang = %q, want en", got.lang)
	}

	if result.Title != "Fallback title" {
		t.Errorf("Title = %q, want Fallback title", result.Title)
	}
	if result.Thumbnail != "https://snapcdn.app/cover.jpg" {
		t.Errorf("Thumbnail = %q, want cover.jpg", result.Thumbnail)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d, want 1", len(result.Formats))
	}
	if result.Formats[0].Type != downloader.MediaVideo || result.Formats[0].URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("Formats[0] = %+v, want video v.mp4", result.Formats[0])
	}
	if got := result.Metadata["author"]; got != "alice" {
		t.Errorf("Metadata author = %q, want alice", got)
	}
}

func TestResolveShortLink(t *testing.T) {
	var officialHits int

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "vm.tiktok.com") {
			_, _ = w.Write([]byte(`<html><head><link rel="canonical" href="https://www.tiktok.com/@alice/video/1234567890"></head></html>`))
			return
		}
		if strings.Contains(r.URL.Path, "snaptik.net") {
			payload, _ := json.Marshal(map[string]interface{}{
				"status":     "error",
				"statusCode": float64(500),
				"msg":        "fail",
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		officialHits++
		_, _ = w.Write([]byte(rehydrationPage(videoItem("Resolved video", "https://cdn.example.com/r.mp4"))))
	}))
	defer relay.Close()

	p := NewWithConfig(Config{RelayBaseURL: relay.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testShortURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.URL != "https://www.tiktok.com/@alice/video/1234567890" {
		t.Errorf("URL = %q, want resolved canonical URL", result.URL)
	}
	if result.Title != "Resolved video" {
		t.Errorf("Title = %q, want Resolved video", result.Title)
	}
	if len(result.Formats) != 1 || result.Formats[0].URL != "https://cdn.example.com/r.mp4" {
		t.Fatalf("Formats = %+v, want r.mp4", result.Formats)
	}
	if officialHits != 1 {
		t.Errorf("official hits = %d, want 1", officialHits)
	}
}

func TestResolveShortLinkNonMediaPage(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><link rel="canonical" href="https://www.tiktok.com/foryou"></head></html>`))
	}))
	defer relay.Close()

	p := NewWithConfig(Config{RelayBaseURL: relay.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testShortURL})
	if !stderrors.Is(err, downloader.ErrMediaNotFound) {
		t.Fatalf("error = %v, want ErrMediaNotFound", err)
	}
}

func TestResolveSnaptikNoMediaIsMediaNotFound(t *testing.T) {
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

	p := NewWithConfig(Config{RelayBaseURL: relay.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testVideoURL})
	if !stderrors.Is(err, downloader.ErrMediaNotFound) {
		t.Fatalf("error = %v, want ErrMediaNotFound", err)
	}
}

func TestResolveSnaptikProfileURLIsMediaNotFound(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "snaptik.net") {
			payload, _ := json.Marshal(map[string]interface{}{
				"status":     "error",
				"msg":        "snaptikpro.net required",
				"statusCode": float64(326),
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("no rehydration here ", 40)))
	}))
	defer relay.Close()

	p := NewWithConfig(Config{RelayBaseURL: relay.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testVideoURL})
	if !stderrors.Is(err, downloader.ErrMediaNotFound) {
		t.Fatalf("error = %v, want ErrMediaNotFound", err)
	}
}

func TestResolveSnaptikErrorIsInvalidResponse(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "snaptik.net") {
			payload, _ := json.Marshal(map[string]interface{}{
				"status":     "error",
				"msg":        "something failed",
				"statusCode": float64(500),
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("no rehydration here ", 40)))
	}))
	defer relay.Close()

	p := NewWithConfig(Config{RelayBaseURL: relay.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testVideoURL})
	if !stderrors.Is(err, downloader.ErrProviderInvalidResponse) {
		t.Fatalf("error = %v, want ErrProviderInvalidResponse", err)
	}
}

func TestResolveTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`<html></html>`))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{RelayBaseURL: srv.URL, Timeout: 50 * time.Millisecond})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testVideoURL})
	if !stderrors.Is(err, downloader.ErrProviderTimeout) {
		t.Fatalf("error = %v, want ErrProviderTimeout", err)
	}
}

func TestIsShortLink(t *testing.T) {
	for _, u := range []string{
		"https://vm.tiktok.com/abc/",
		"https://vt.tiktok.com/abc/",
		"https://m.tiktok.com/abc/",
		"https://v.douyin.com/abc/",
		"http://vm.tiktok.com/abc/",
	} {
		if !isShortLink(u) {
			t.Errorf("isShortLink(%q) = false, want true", u)
		}
	}
	for _, u := range []string{
		"https://www.tiktok.com/@alice/video/123",
		"https://tiktok.com/",
		"https://example.com/vm.tiktok.com/",
	} {
		if isShortLink(u) {
			t.Errorf("isShortLink(%q) = true, want false", u)
		}
	}
}

func TestJWTURL(t *testing.T) {
	token := fakeJWT(`{"url":"https://cdn.example.com/v.mp4"}`)
	if got := jwtURL("https://snapcdn.app/v1?token=" + token); got != "https://cdn.example.com/v.mp4" {
		t.Errorf("jwtURL(token) = %q, want decoded url", got)
	}
	if got := jwtURL("https://snapcdn.app/v1?token=not.a.jwt"); got != "https://snapcdn.app/v1?token=not.a.jwt" {
		t.Errorf("jwtURL(invalid token) = %q, want unchanged", got)
	}
	if got := jwtURL("https://snapcdn.app/v1"); got != "https://snapcdn.app/v1" {
		t.Errorf("jwtURL(no token) = %q, want unchanged", got)
	}
}

func TestJWTDecode(t *testing.T) {
	payload := fakeJWT(`{"url":"https://cdn.example.com/a.mp4"}`)
	m := jwtPayload(payload)
	if got := str(m["url"]); got != "https://cdn.example.com/a.mp4" {
		t.Errorf("jwtPayload url = %q, want https://cdn.example.com/a.mp4", got)
	}
	if m := jwtPayload("nope"); m != nil {
		t.Errorf("jwtPayload(invalid) = %v, want nil", m)
	}
}

func TestPickPlayURL(t *testing.T) {
	cases := []struct {
		name  string
		video map[string]interface{}
		want  string
	}{
		{
			name: "bitrate list first url",
			video: map[string]interface{}{
				"bitrateInfo": []interface{}{
					map[string]interface{}{
						"PlayAddr": map[string]interface{}{"UrlList": []interface{}{"https://cdn.example.com/a.mp4", "https://cdn.example.com/b.mp4"}},
					},
				},
			},
			want: "https://cdn.example.com/a.mp4",
		},
		{
			name:  "play addr string",
			video: map[string]interface{}{"playAddr": "https://cdn.example.com/p.mp4"},
			want:  "https://cdn.example.com/p.mp4",
		},
		{
			name:  "download addr string",
			video: map[string]interface{}{"downloadAddr": "https://cdn.example.com/d.mp4"},
			want:  "https://cdn.example.com/d.mp4",
		},
		{
			name:  "empty",
			video: map[string]interface{}{},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickPlayURL(tc.video); got != tc.want {
				t.Errorf("pickPlayURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func rehydrationPage(item map[string]interface{}) string {
	root := map[string]interface{}{
		"__DEFAULT_SCOPE__": map[string]interface{}{
			"webapp.video-detail": map[string]interface{}{
				"itemInfo": map[string]interface{}{"itemStruct": item},
			},
		},
	}
	b, _ := json.Marshal(root)
	return `<script id="__UNIVERSAL_DATA_FOR_REHYDRATION__" type="application/json">` + string(b) + `</script>` + strings.Repeat("x", 300)
}

func videoItem(desc, url string) map[string]interface{} {
	return map[string]interface{}{
		"desc":   desc,
		"author": map[string]interface{}{"uniqueId": "alice"},
		"video": map[string]interface{}{
			"cover": "https://cdn.example.com/cover.jpg",
			"bitrateInfo": []interface{}{
				map[string]interface{}{
					"PlayAddr": map[string]interface{}{"UrlList": []interface{}{url}},
				},
			},
		},
	}
}

func fakeJWT(payload string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return header + "." + body + ".sig"
}
