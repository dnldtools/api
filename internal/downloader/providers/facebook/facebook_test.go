package facebook

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rest-api/internal/downloader"
)

const testFacebookURL = "https://www.facebook.com/reel/123"

func TestProviderContract(t *testing.T) {
	p := New()
	if p.Name() != "facebook" {
		t.Errorf("Name() = %q, want facebook", p.Name())
	}
	if p.Platform() != downloader.PlatformFacebook {
		t.Errorf("Platform() = %q, want facebook", p.Platform())
	}
	if p.Type() != downloader.ProviderExternalAPI {
		t.Errorf("Type() = %q, want external_api", p.Type())
	}
}

func TestResolveEmptyURL(t *testing.T) {
	p := New()
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: ""})
	if !stderrors.Is(err, downloader.ErrInvalidURL) {
		t.Fatalf("error = %v, want ErrInvalidURL", err)
	}
}

func TestResolveJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Video &amp; Title","thumbnail":"https://th.example/t.jpg","downloads":[{"quality":"HD","type":"video","url":"https://cdn.example.com/v.mp4"}]}`))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL})

	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testFacebookURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Platform != downloader.PlatformFacebook {
		t.Errorf("Platform = %q, want facebook", result.Platform)
	}
	if result.URL != testFacebookURL {
		t.Errorf("URL = %q, want %q", result.URL, testFacebookURL)
	}
	if result.Title != "Video & Title" {
		t.Errorf("Title = %q, want decoded title", result.Title)
	}
	if result.Thumbnail != "https://th.example/t.jpg" {
		t.Errorf("Thumbnail = %q, want https://th.example/t.jpg", result.Thumbnail)
	}
	if result.Type != downloader.MediaVideo {
		t.Errorf("Type = %q, want video", result.Type)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d, want 1", len(result.Formats))
	}
	f := result.Formats[0]
	if f.Type != downloader.MediaVideo {
		t.Errorf("Format.Type = %q, want video", f.Type)
	}
	if f.Quality != "HD" {
		t.Errorf("Format.Quality = %q, want HD", f.Quality)
	}
	if f.URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("Format.URL = %q, want https://cdn.example.com/v.mp4", f.URL)
	}
}

func TestResolveHTMLResponse(t *testing.T) {
	htmlBody := `<html><body>
<h3 class="result-title">Video <b>Title</b> &amp; More</h3>
<div class="result-thumbnail"><img src="https://th.example/t.jpg"></div>
<div class="text-sm font-bold">1080p</div>
<div class="text-xs">video</div>
<a href="https://cdn.example.com/v.mp4">Download</a>
<div class="text-sm font-bold">720p</div>
<div class="text-xs">(mp4)</div>
<a href="https://cdn.example.com/v2.mp4&amp;x=1">Download</a>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL})

	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testFacebookURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Title != "Video Title & More" {
		t.Errorf("Title = %q, want \"Video Title & More\"", result.Title)
	}
	if result.Thumbnail != "https://th.example/t.jpg" {
		t.Errorf("Thumbnail = %q, want https://th.example/t.jpg", result.Thumbnail)
	}
	if len(result.Formats) != 2 {
		t.Fatalf("len(Formats) = %d, want 2", len(result.Formats))
	}
	if result.Formats[0].Quality != "1080p" || result.Formats[0].URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("Formats[0] = %+v, want 1080p / v.mp4", result.Formats[0])
	}
	if result.Formats[1].Quality != "720p" || result.Formats[1].URL != "https://cdn.example.com/v2.mp4&x=1" {
		t.Errorf("Formats[1] = %+v, want 720p / decoded v2.mp4&x=1", result.Formats[1])
	}
	if result.Type != downloader.MediaVideo {
		t.Errorf("Type = %q, want video (all formats share the same kind)", result.Type)
	}
}

func TestResolveNormalizesAudioType(t *testing.T) {
	htmlBody := `<html><body>
<h3 class="result-title">Audio Only</h3>
<div class="text-sm">High</div>
<div class="text-xs">(mp3)</div>
<a href="https://cdn.example.com/a.mp3">Download</a>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer srv.Close()

	result, err := NewWithConfig(Config{BaseURL: srv.URL}).Resolve(context.Background(), downloader.DownloadRequest{URL: testFacebookURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d, want 1", len(result.Formats))
	}
	if result.Formats[0].Type != downloader.MediaAudio {
		t.Errorf("Format.Type = %q, want audio", result.Formats[0].Type)
	}
	if result.Type != downloader.MediaAudio {
		t.Errorf("Type = %q, want audio", result.Type)
	}
}

func TestResolveMalformedResponseIsInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("this is neither json nor html"))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testFacebookURL})
	if !stderrors.Is(err, downloader.ErrProviderInvalidResponse) {
		t.Fatalf("error = %v, want ErrProviderInvalidResponse", err)
	}
}

func TestResolveMalformedJSONIsInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"title": broken`))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testFacebookURL})
	if !stderrors.Is(err, downloader.ErrProviderInvalidResponse) {
		t.Fatalf("error = %v, want ErrProviderInvalidResponse", err)
	}
}

func TestResolveEmptyDownloadsIsMediaNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><h3 class="result-title">Only Title</h3></html>`))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testFacebookURL})
	if !stderrors.Is(err, downloader.ErrMediaNotFound) {
		t.Fatalf("error = %v, want ErrMediaNotFound", err)
	}
}

func TestResolveFiltersGoogleAndAppleURLs(t *testing.T) {
	htmlBody := `<html><body>
<h3 class="result-title">Filtered</h3>
<div class="text-sm">HD</div><div class="text-xs">video</div><a href="https://play.google.com/store/apps/details?id=x">g</a>
<div class="text-sm">HD</div><div class="text-xs">video</div><a href="https://apps.apple.com/app/id123">a</a>
<div class="text-sm">SD</div><div class="text-xs">video</div><a href="https://cdn.example.com/v.mp4">ok</a>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer srv.Close()

	result, err := NewWithConfig(Config{BaseURL: srv.URL}).Resolve(context.Background(), downloader.DownloadRequest{URL: testFacebookURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d, want 1 (Google/Apple filtered out)", len(result.Formats))
	}
	if result.Formats[0].URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("Formats[0].URL = %q, want https://cdn.example.com/v.mp4", result.Formats[0].URL)
	}
}

func TestResolveUpstream4xxIsInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testFacebookURL})
	if !stderrors.Is(err, downloader.ErrProviderInvalidResponse) {
		t.Fatalf("error = %v, want ErrProviderInvalidResponse", err)
	}
}

func TestResolveUpstream5xxIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testFacebookURL})
	if !stderrors.Is(err, downloader.ErrProviderUnavailable) {
		t.Fatalf("error = %v, want ErrProviderUnavailable", err)
	}
}

func TestResolveUpstreamUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close()

	p := NewWithConfig(Config{BaseURL: addr, Timeout: 2 * time.Second})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testFacebookURL})
	if !stderrors.Is(err, downloader.ErrProviderUnavailable) {
		t.Fatalf("error = %v, want ErrProviderUnavailable", err)
	}
}

func TestResolveTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`<html><h3 class="result-title">T</h3></html>`))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL, Timeout: 50 * time.Millisecond})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testFacebookURL})
	if !stderrors.Is(err, downloader.ErrProviderTimeout) {
		t.Fatalf("error = %v, want ErrProviderTimeout", err)
	}
}

func TestResolveContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`<html><h3 class="result-title">T</h3></html>`))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Resolve(ctx, downloader.DownloadRequest{URL: testFacebookURL})
	if !stderrors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestResolveSendsScriptHeadersAndForm(t *testing.T) {
	type captured struct {
		method       string
		path         string
		userAgent    string
		contentType  string
		hxCurrentURL string
		hxRequest    string
		hxTarget     string
		hxTrigger    string
		origin       string
		referer      string
		formID       string
		formLocale   string
	}
	var got captured

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = captured{
			method:       r.Method,
			path:         r.URL.Path,
			userAgent:    r.Header.Get("User-Agent"),
			contentType:  r.Header.Get("Content-Type"),
			hxCurrentURL: r.Header.Get("Hx-Current-Url"),
			hxRequest:    r.Header.Get("Hx-Request"),
			hxTarget:     r.Header.Get("Hx-Target"),
			hxTrigger:    r.Header.Get("Hx-Trigger"),
			origin:       r.Header.Get("Origin"),
			referer:      r.Header.Get("Referer"),
			formID:       r.FormValue("id"),
			formLocale:   r.FormValue("locale"),
		}
		_, _ = w.Write([]byte(`<html><h3 class="result-title">T</h3><div class="text-sm">HD</div><div class="text-xs">video</div><a href="https://cdn.example.com/v.mp4">x</a></html>`))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL})
	if _, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testFacebookURL}); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/process" {
		t.Errorf("path = %q, want /process", got.path)
	}
	if got.userAgent != defaultUserAgent {
		t.Errorf("User-Agent = %q, want defaultUserAgent", got.userAgent)
	}
	if got.contentType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", got.contentType)
	}
	if got.hxCurrentURL != srv.URL+"/id" {
		t.Errorf("Hx-Current-Url = %q, want %q", got.hxCurrentURL, srv.URL+"/id")
	}
	if got.hxRequest != "true" {
		t.Errorf("Hx-Request = %q, want true", got.hxRequest)
	}
	if got.hxTarget != "target" {
		t.Errorf("Hx-Target = %q, want target", got.hxTarget)
	}
	if got.hxTrigger != "form" {
		t.Errorf("Hx-Trigger = %q, want form", got.hxTrigger)
	}
	if got.origin != srv.URL {
		t.Errorf("Origin = %q, want %q", got.origin, srv.URL)
	}
	if got.referer != srv.URL+"/id" {
		t.Errorf("Referer = %q, want %q", got.referer, srv.URL+"/id")
	}
	if got.formID != testFacebookURL {
		t.Errorf("form id = %q, want %q", got.formID, testFacebookURL)
	}
	if got.formLocale != "id" {
		t.Errorf("form locale = %q, want id", got.formLocale)
	}
}

func TestResolveNativeVideo(t *testing.T) {
	htmlBody := `<html><head>
<meta property="og:title" content="My &amp; Reel Title">
<meta property="og:description" content="A cool reel">
<meta property="og:image" content="https://scontent.example/t.jpg">
<script>window.data = {"browser_native_hd_url":"https:\/\/video.example\/hd.mp4","browser_native_sd_url":"https:\/\/video.example\/sd.mp4"}</script>
</head></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{NativeEnabled: true, NativeBaseURL: srv.URL})

	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://www.facebook.com/watch/?v=123"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Platform != downloader.PlatformFacebook {
		t.Errorf("Platform = %q, want facebook", result.Platform)
	}
	if result.Title != "My & Reel Title" {
		t.Errorf("Title = %q, want decoded title", result.Title)
	}
	if result.Thumbnail != "https://scontent.example/t.jpg" {
		t.Errorf("Thumbnail = %q, want og image", result.Thumbnail)
	}
	if result.Type != downloader.MediaVideo {
		t.Errorf("Type = %q, want video", result.Type)
	}
	if len(result.Formats) != 2 {
		t.Fatalf("len(Formats) = %d, want 2", len(result.Formats))
	}
	if result.Formats[0].URL != "https://video.example/hd.mp4" || result.Formats[0].Quality != "hd" {
		t.Errorf("Formats[0] = %+v, want hd mp4", result.Formats[0])
	}
	if result.Formats[1].URL != "https://video.example/sd.mp4" || result.Formats[1].Quality != "sd" {
		t.Errorf("Formats[1] = %+v, want sd mp4", result.Formats[1])
	}
	if result.Metadata["source"] != "facebook_native" {
		t.Errorf("Metadata source = %q, want facebook_native", result.Metadata["source"])
	}
	if result.Metadata["description"] != "A cool reel" {
		t.Errorf("Metadata description = %q, want A cool reel", result.Metadata["description"])
	}
}

func TestResolveNativeImage(t *testing.T) {
	htmlBody := `<html><head>
<meta property="og:title" content="Photo Post">
<meta property="og:image" content="https://scontent.example/thumb.jpg">
<script>{"image":{"uri":"https:\/\/scontent.fbcdn.net\/v\/t51.29350-15\/12345_67890.jpg"}}</script>
</head></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{NativeEnabled: true, NativeBaseURL: srv.URL})

	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://www.facebook.com/photo/?fbid=1"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Type != downloader.MediaImage {
		t.Errorf("Type = %q, want image", result.Type)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d, want 1", len(result.Formats))
	}
	if result.Formats[0].URL != "https://scontent.fbcdn.net/v/t51.29350-15/12345_67890.jpg" {
		t.Errorf("Format URL = %q, want fbcdn hd image", result.Formats[0].URL)
	}
	if result.Thumbnail != "https://scontent.example/thumb.jpg" {
		t.Errorf("Thumbnail = %q, want og image", result.Thumbnail)
	}
}
