package tiktok

import (
	"context"
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

	p := NewWithConfig(Config{RelayBaseURL: relay.URL, OfficialBaseURL: relay.URL})
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
			"id":     "1234567890",
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

	p := NewWithConfig(Config{RelayBaseURL: relay.URL, OfficialBaseURL: relay.URL})
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

	p := NewWithConfig(Config{RelayBaseURL: relay.URL, OfficialBaseURL: relay.URL})
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

	p := NewWithConfig(Config{RelayBaseURL: relay.URL, OfficialBaseURL: relay.URL, ResolveBaseURL: relay.URL})
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

	p := NewWithConfig(Config{RelayBaseURL: relay.URL, OfficialBaseURL: relay.URL, ResolveBaseURL: relay.URL})
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

	p := NewWithConfig(Config{RelayBaseURL: relay.URL, OfficialBaseURL: relay.URL})
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

	p := NewWithConfig(Config{RelayBaseURL: relay.URL, OfficialBaseURL: relay.URL})
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

	p := NewWithConfig(Config{RelayBaseURL: relay.URL, OfficialBaseURL: relay.URL})
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

	p := NewWithConfig(Config{RelayBaseURL: srv.URL, OfficialBaseURL: srv.URL, Timeout: 50 * time.Millisecond})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testVideoURL})
	if !stderrors.Is(err, downloader.ErrProviderTimeout) {
		t.Fatalf("error = %v, want ErrProviderTimeout", err)
	}
}

func TestOfficialPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://www.tiktok.com/@alice/video/1234567890?is_from_webapp=1", "/@alice/video/1234567890"},
		{"https://www.tiktok.com/@alice/video/7123456789012345678", "/@alice/video/7123456789012345678"},
		{"7123456789012345678", "/@i/video/7123456789012345678"},
	}
	for _, tc := range cases {
		if got := officialPath(tc.in); got != tc.want {
			t.Errorf("officialPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
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
		"id":     "1234567890",
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

func TestStreamMediaRejectsDisallowedHosts(t *testing.T) {
	p := NewWithConfig(DefaultConfig())

	allowed := []string{
		"https://v16m.tiktokcdn-us.com/video.mp4",
		"https://v16.tokcdn.com/video.mp4",
		"https://p16-common-sign.tiktokcdn-us.com/x.jpg",
		"https://snapcdn.app/v.mp4",
		"https://tik-cdn.com/v.mp4",
		"https://www.tiktok.com/api/v1/video/redirect/",
		"https://sf16-muse-va.ibytedtos.com/v.mp4",
	}
	for _, u := range allowed {
		if !p.allowedMediaHost(u) {
			t.Errorf("allowedMediaHost(%q) = false, want true", u)
		}
	}

	rejected := []string{
		"https://evil.com/v.mp4",
		"https://tiktokcdn.com.evil.com/v.mp4",
		"https://eviltiktokcdn.com/v.mp4",
		"ftp://v16.tokcdn.com/v.mp4",
		"",
		"not-a-url",
	}
	for _, u := range rejected {
		if p.allowedMediaHost(u) {
			t.Errorf("allowedMediaHost(%q) = true, want false", u)
		}
	}
}

func TestStreamMediaSendsHeadersAndStreamsBody(t *testing.T) {
	var gotUA, gotReferer, gotOrigin string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotReferer = r.Header.Get("Referer")
		gotOrigin = r.Header.Get("Origin")
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("streamed-bytes"))
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	p := NewWithConfig(Config{AllowedMediaHosts: []string{u.Hostname()}})

	stream, err := p.StreamMedia(context.Background(), srv.URL+"/media.mp4")
	if err != nil {
		t.Fatalf("StreamMedia() error = %v", err)
	}
	defer stream.Body.Close()

	if gotUA != officialUserAgent {
		t.Errorf("User-Agent = %q, want %q", gotUA, officialUserAgent)
	}
	if gotReferer != "https://www.tiktok.com/" {
		t.Errorf("Referer = %q, want https://www.tiktok.com/", gotReferer)
	}
	if gotOrigin != "https://www.tiktok.com" {
		t.Errorf("Origin = %q, want https://www.tiktok.com", gotOrigin)
	}
	if stream.ContentType != "video/mp4" {
		t.Errorf("ContentType = %q, want video/mp4", stream.ContentType)
	}
	body, _ := io.ReadAll(stream.Body)
	if string(body) != "streamed-bytes" {
		t.Errorf("body = %q, want streamed-bytes", string(body))
	}
}

func TestResolveNativeSSR(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rehydrationPage(videoItem("Native title", "https://cdn.example.com/native.mp4"))))
	}))
	defer relay.Close()

	p := NewWithConfig(Config{NativeEnabled: true, OfficialBaseURL: relay.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testVideoURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Platform != downloader.PlatformTikTok {
		t.Errorf("Platform = %q, want tiktok", result.Platform)
	}
	if result.Title != "Native title" {
		t.Errorf("Title = %q, want Native title", result.Title)
	}
	if result.Type != downloader.MediaVideo {
		t.Errorf("Type = %q, want video", result.Type)
	}
	if len(result.Formats) != 1 || result.Formats[0].URL != "https://cdn.example.com/native.mp4" {
		t.Fatalf("Formats = %+v, want native.mp4", result.Formats)
	}
	if result.Metadata["source"] != "tiktok_native_ssr" {
		t.Errorf("Metadata source = %q, want tiktok_native_ssr", result.Metadata["source"])
	}
	if result.Metadata["id"] != "1234567890" {
		t.Errorf("Metadata id = %q, want 1234567890", result.Metadata["id"])
	}
	if result.Metadata["author"] != "alice" {
		t.Errorf("Metadata author = %q, want alice", result.Metadata["author"])
	}
}

func TestResolveTikwmFallback(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("no rehydration here ", 40)))
	}))
	defer relay.Close()

	tikwm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/" {
			t.Errorf("TikWM path = %q, want /api/", r.URL.Path)
		}
		payload := `{"code":0,"msg":"success","data":{` +
			`"id":"1234567890","title":"TikWM Video",` +
			`"author":{"id":"u1","unique_id":"alice","nickname":"Alice","avatar":"https://av.example/a.jpg"},` +
			`"music_info":{"id":"m1","title":"Song","author":"Artist","play":"https://tikwm.example/m.mp3"},` +
			`"digg_count":100,"comment_count":5,"share_count":2,"play_count":1000,"collect_count":7,` +
			`"duration":15,"cover":"https://cv.example/c.jpg","hdplay":"https://cdn.example/hd.mp4","images":[]}}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer tikwm.Close()

	p := NewWithConfig(Config{OfficialBaseURL: relay.URL, TikwmEnabled: true, TikwmBaseURL: tikwm.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testVideoURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Title != "TikWM Video" {
		t.Errorf("Title = %q, want TikWM Video", result.Title)
	}
	if len(result.Formats) != 2 {
		t.Fatalf("len(Formats) = %d, want 2 (video + audio)", len(result.Formats))
	}
	if result.Formats[0].Type != downloader.MediaVideo || result.Formats[0].URL != "https://cdn.example/hd.mp4" {
		t.Errorf("Formats[0] = %+v, want video hd.mp4", result.Formats[0])
	}
	if result.Formats[1].Type != downloader.MediaAudio || result.Formats[1].URL != "https://tikwm.example/m.mp3" {
		t.Errorf("Formats[1] = %+v, want audio m.mp3", result.Formats[1])
	}
	if result.DurationMs != 15000 {
		t.Errorf("DurationMs = %d, want 15000", result.DurationMs)
	}
	if result.Metadata["author"] != "alice" {
		t.Errorf("Metadata author = %q, want alice", result.Metadata["author"])
	}
	if result.Metadata["likes"] != "100" {
		t.Errorf("Metadata likes = %q, want 100", result.Metadata["likes"])
	}
	if result.Metadata["music_title"] != "Song" {
		t.Errorf("Metadata music_title = %q, want Song", result.Metadata["music_title"])
	}
}

func TestPickBestPlayURLPrefersHighestBitrate(t *testing.T) {
	video := map[string]interface{}{
		"bitrateInfo": []interface{}{
			map[string]interface{}{
				"Bitrate":  float64(500000),
				"PlayAddr": map[string]interface{}{"UrlList": []interface{}{"https://cdn.example.com/low.mp4"}},
			},
			map[string]interface{}{
				"Bitrate":  float64(5000000),
				"PlayAddr": map[string]interface{}{"UrlList": []interface{}{"https://cdn.example.com/high.mp4"}},
			},
			map[string]interface{}{
				"Bitrate":  float64(2000000),
				"PlayAddr": map[string]interface{}{"UrlList": []interface{}{"https://cdn.example.com/mid.mp4"}},
			},
		},
	}
	if got := pickBestPlayURL(video); got != "https://cdn.example.com/high.mp4" {
		t.Errorf("pickBestPlayURL() = %q, want high.mp4", got)
	}
}

func TestParseShortDramaURL(t *testing.T) {
	cases := []struct {
		in      string
		dramaID string
		episode int
	}{
		{"https://www.tiktok.com/shortdrama/episode/7660053695581361172/1", "7660053695581361172", 1},
		{"https://www.tiktok.com/shortdrama/episode/7660053695581361172/85", "7660053695581361172", 85},
		{"https://www.tiktok.com/shortdrama/detail/7660053695581361172", "7660053695581361172", 0},
		{"https://www.tiktok.com/shortdrama/7660053695581361172", "7660053695581361172", 0},
		{"https://www.tiktok.com/@alice/video/123", "", 0},
	}
	for _, c := range cases {
		id, ep := parseShortDramaURL(c.in)
		if id != c.dramaID || ep != c.episode {
			t.Errorf("parseShortDramaURL(%q) = (%q, %d), want (%q, %d)", c.in, id, ep, c.dramaID, c.episode)
		}
	}
	if !isShortDramaURL("https://www.tiktok.com/shortdrama/episode/7660053695581361172/1") {
		t.Error("isShortDramaURL() = false, want true")
	}
	if isShortDramaURL("https://www.tiktok.com/@alice/video/123") {
		t.Error("isShortDramaURL() = true, want false")
	}
}

func TestResolveShortDramaEpisode(t *testing.T) {
	var gotDramaID, gotCursor string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/drama/episode/item_list/":
			gotDramaID = r.URL.Query().Get("dramaID")
			gotCursor = r.URL.Query().Get("cursor")
			_, _ = w.Write([]byte(`{"statusCode":0,"cursor":"1","hasMore":true,"totalEpisodeCount":"85","itemList":[{"id":"1234567890","desc":"My Drama","author":{"id":"1","uniqueId":"alice","nickname":"Alice"},"video":{"cover":"https://cdn.example.com/cover.jpg"},"dramaInfo":{"authorUID":"1","description":"Synopsis","cover":{"urlList":["https://cdn.example.com/drama.jpg"]},"DramaVideoData":{"EpisodeNumber":1,"IsPreview":true}}}]}`))
		case strings.HasPrefix(r.URL.Path, "/@alice/video/"):
			_, _ = w.Write([]byte(rehydrationPage(videoItem("Hello world", "https://cdn.example.com/v.mp4"))))
		default:
			http.NotFound(w, r)
		}
	}))
	defer relay.Close()

	p := NewWithConfig(Config{RelayBaseURL: relay.URL, OfficialBaseURL: relay.URL, DramaAPIBase: relay.URL, NativeEnabled: false, TikwmEnabled: false, SnapXEnabled: false})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://www.tiktok.com/shortdrama/episode/7660053695581361172/1"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if gotDramaID != "7660053695581361172" {
		t.Errorf("dramaID query = %q, want 7660053695581361172", gotDramaID)
	}
	if gotCursor != "0" {
		t.Errorf("cursor query = %q, want 0", gotCursor)
	}
	if result.Platform != downloader.PlatformTikTok {
		t.Errorf("Platform = %q, want tiktok", result.Platform)
	}
	if result.Title != "My Drama - Episode 1" {
		t.Errorf("Title = %q, want My Drama - Episode 1", result.Title)
	}
	if result.Thumbnail != "https://cdn.example.com/cover.jpg" {
		t.Errorf("Thumbnail = %q, want cover.jpg", result.Thumbnail)
	}
	if len(result.Formats) != 1 || result.Formats[0].URL != "https://cdn.example.com/v.mp4" {
		t.Fatalf("Formats = %+v, want single v.mp4", result.Formats)
	}
	if result.Metadata["drama_id"] != "7660053695581361172" {
		t.Errorf("Metadata drama_id = %q, want 7660053695581361172", result.Metadata["drama_id"])
	}
	if result.Metadata["drama_episode"] != "1" {
		t.Errorf("Metadata drama_episode = %q, want 1", result.Metadata["drama_episode"])
	}
	if result.Metadata["drama_episode_count"] != "85" {
		t.Errorf("Metadata drama_episode_count = %q, want 85", result.Metadata["drama_episode_count"])
	}
	if result.Metadata["drama_creator"] != "Alice" {
		t.Errorf("Metadata drama_creator = %q, want Alice", result.Metadata["drama_creator"])
	}
	if result.Metadata["source"] != "tiktok_shortdrama" {
		t.Errorf("Metadata source = %q, want tiktok_shortdrama", result.Metadata["source"])
	}
}
