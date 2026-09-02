package savefrom

import (
	"context"
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

func padded(js string) string {
	return "/*" + strings.Repeat("padding", 40) + "*/" + js
}

func TestContract(t *testing.T) {
	p := New()
	if p.Name() != "savefrom" {
		t.Errorf("Name() = %q, want savefrom", p.Name())
	}
	if p.Platform() != downloader.PlatformSavefrom {
		t.Errorf("Platform() = %q, want %q", p.Platform(), downloader.PlatformSavefrom)
	}
	if p.Type() != downloader.ProviderExternalAPI {
		t.Errorf("Type() = %q, want external_api", p.Type())
	}
}

func TestMatchesURL(t *testing.T) {
	p := New()
	cases := []struct {
		in   string
		want bool
	}{
		{"https://example.com/video", true},
		{"http://example.com/video", true},
		{"https://youtu.be/abc", true},
		{"", false},
		{"example.com/video", false},
		{"ftp://example.com/video", false},
		{"mailto:user@example.com", false},
	}
	for _, tc := range cases {
		if got := p.MatchesURL(tc.in); got != tc.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestResolveInvalidURL(t *testing.T) {
	p := New()
	if _, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: ""}); !stderrors.Is(err, downloader.ErrInvalidURL) {
		t.Errorf("Resolve(empty) error = %v, want ErrInvalidURL", err)
	}
	if _, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "ftp://example.com/v"}); !stderrors.Is(err, downloader.ErrInvalidURL) {
		t.Errorf("Resolve(ftp) error = %v, want ErrInvalidURL", err)
	}
}

func TestSha256Hex(t *testing.T) {
	if got := sha256Hex("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("sha256Hex(abc) = %q", got)
	}
}

func TestBuildForm(t *testing.T) {
	form := buildForm("https://example.com/video", Profile{Origin: "https://id.savefrom.net", Lang: "id", Country: "id"})

	if got := form.Get("sf_url"); got != "https://example.com/video" {
		t.Errorf("sf_url = %q", got)
	}
	if got := form.Get("new"); got != "2" {
		t.Errorf("new = %q", got)
	}
	if got := form.Get("_ts"); got != fixedTS {
		t.Errorf("_ts = %q, want %q", got, fixedTS)
	}
	if got := form.Get("_x"); got != "1" {
		t.Errorf("_x = %q", got)
	}
	if got := form.Get("sf-nomad"); got != "1" {
		t.Errorf("sf-nomad = %q", got)
	}
	if got := form.Get("os"); got != "Windows" {
		t.Errorf("os = %q", got)
	}
	if got := form.Get("browser"); got != "Chrome" {
		t.Errorf("browser = %q", got)
	}

	ts := form.Get("ts")
	if ts == "" {
		t.Fatal("ts is empty")
	}
	wantSig := sha256Hex("https://example.com/video" + ts + secret)
	if got := form.Get("_s"); got != wantSig {
		t.Errorf("_s = %q, want %q", got, wantSig)
	}
	if got := form.Get("_s"); len(got) != 64 {
		t.Errorf("_s length = %d, want 64", len(got))
	}
}

func TestYouTubeID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://youtu.be/abc123", "abc123"},
		{"https://www.youtube.com/watch?v=xyz", "xyz"},
		{"https://www.youtube.com/shorts/s1", "s1"},
		{"https://www.youtube.com/embed/e1", "e1"},
		{"https://www.youtube.com/live/l1", "l1"},
		{"https://example.com/video", ""},
		{"https://youtu.be/", ""},
	}
	for _, tc := range cases {
		if got := youtubeID(tc.in); got != tc.want {
			t.Errorf("youtubeID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestURLVariants(t *testing.T) {
	got := urlVariants("https://www.youtube.com/watch?v=xyz")
	if len(got) != 3 {
		t.Fatalf("urlVariants(youtube) len = %d, want 3: %v", len(got), got)
	}
	want := []string{
		"https://www.youtube.com/watch?v=xyz",
		"https://youtu.be/xyz",
		"https://www.youtube.com/watch?v=xyz&hl=en",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("urlVariants[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	if got := urlVariants("https://example.com/v"); len(got) != 1 || got[0] != "https://example.com/v" {
		t.Errorf("urlVariants(non-youtube) = %v", got)
	}
}

func TestExtractJSONFromDecoded(t *testing.T) {
	t.Run("show wraps result in array", func(t *testing.T) {
		decoded := "window.parent.sf.videoResult.show([1,2,3]);"
		got, err := extractJSONFromDecoded(decoded)
		if err != nil {
			t.Fatal(err)
		}
		outer, ok := got.([]interface{})
		if !ok || len(outer) != 1 {
			t.Fatalf("expected outer array of len 1, got %#v", got)
		}
		inner, ok := outer[0].([]interface{})
		if !ok || len(inner) != 3 {
			t.Fatalf("expected inner array of len 3, got %#v", outer[0])
		}
	})

	t.Run("showRows returns array directly", func(t *testing.T) {
		decoded := `window.parent.sf.videoResult.showRows([{"u":1}]);`
		got, err := extractJSONFromDecoded(decoded)
		if err != nil {
			t.Fatal(err)
		}
		arr, ok := got.([]interface{})
		if !ok || len(arr) != 1 {
			t.Fatalf("expected array of len 1, got %#v", got)
		}
		m := asMap(arr[0])
		if m == nil || m["u"] != float64(1) {
			t.Fatalf("expected [{u:1}], got %#v", arr)
		}
	})

	t.Run("showRows fallback uses ); when no enableElement", func(t *testing.T) {
		// The array contains the delimiter `],"` inside a string value,
		// but no enableElement marker, so the `);` fallback must be used.
		decoded := `window.parent.sf.videoResult.showRows(["a]","b"]);`
		got, err := extractJSONFromDecoded(decoded)
		if err != nil {
			t.Fatal(err)
		}
		arr, ok := got.([]interface{})
		if !ok || len(arr) != 2 {
			t.Fatalf("expected array of len 2, got %#v", got)
		}
		if arr[0] != "a]" || arr[1] != "b" {
			t.Fatalf("expected [\"a]\",\"b\"], got %#v", arr)
		}
	})

	t.Run("no marker", func(t *testing.T) {
		got, err := extractJSONFromDecoded("no relevant marker here")
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %#v", got)
		}
	})
}

func TestPickDirectFiles(t *testing.T) {
	item := map[string]interface{}{
		"url": []interface{}{
			map[string]interface{}{"url": "https://cdn.example.com/v.mp4", "quality": "720p", "ext": "mp4", "type": "mp4", "filesize": "12345"},
			map[string]interface{}{"url": "https://cdn.example.com/v.mp4", "quality": "720p"}, // duplicate
			map[string]interface{}{"url": "#", "quality": "bad"},                              // placeholder
			map[string]interface{}{"url": "https://cdn.example.com/novideo.mp4", "no_audio": true, "ext": "mp4", "type": "mp4"},
			map[string]interface{}{"url": "https://cdn.example.com/audio.m4a", "type": "audio", "ext": "m4a"},
			map[string]interface{}{"url": "https://du.sf-converter.com/convert?payload=abc", "quality": "1080p", "ext": "mp4", "type": "mp4"},
			map[string]interface{}{"url": "https://cdn.example.com/proxy.mp4", "quality": "proxy"}, // proxy filtered below via converter proxy check
			map[string]interface{}{"url": "not-a-url"},
		},
	}

	// The proxy entry needs an sf-converter.com/proxy URL to be filtered.
	item["url"] = append(sliceOf(item["url"]), map[string]interface{}{"url": "https://sf-converter.com/proxy/x", "quality": "proxy"})

	files := pickDirectFiles(item)
	if len(files) != 5 {
		t.Fatalf("pickDirectFiles len = %d, want 5: %+v", len(files), files)
	}

	byURL := make(map[string]mediaFile, len(files))
	for _, f := range files {
		byURL[f.URL] = f
	}

	direct := byURL["https://cdn.example.com/v.mp4"]
	if direct.Kind != "direct" || !direct.HasAudio || direct.Filesize != "12345" {
		t.Errorf("direct file = %+v", direct)
	}
	novideo := byURL["https://cdn.example.com/novideo.mp4"]
	if novideo.Kind != "video-only" || novideo.HasAudio {
		t.Errorf("video-only file = %+v", novideo)
	}
	audio := byURL["https://cdn.example.com/audio.m4a"]
	if audio.Kind != "audio" {
		t.Errorf("audio file = %+v", audio)
	}
	conv := byURL["https://du.sf-converter.com/convert?payload=abc"]
	if conv.Kind != "converter" || conv.Quality != "1080p" {
		t.Errorf("converter file = %+v", conv)
	}
}

func TestEvaluatePackedShow(t *testing.T) {
	p := New()
	packed := padded(`window.parent.sf.videoResult.show({"id":"v1","thumb":"https://img/t.jpg","meta":{"title":"T","source":"s","duration":125},"url":[{"url":"https://cdn/v.mp4","quality":"720p","ext":"mp4","type":"mp4"}]});`)

	got, err := p.evaluatePacked(packed)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := got.([]interface{})
	if !ok || len(arr) != 1 {
		t.Fatalf("expected array of len 1, got %#v", got)
	}
	m := asMap(arr[0])
	if m == nil || m["id"] != "v1" {
		t.Fatalf("expected item with id v1, got %#v", arr[0])
	}
}

func TestEvaluatePackedDecodedScript(t *testing.T) {
	p := New()
	raw := `window.parent.sf.videoResult.show([{"url":"https://x/v.mp4"}]);`
	packed := `decodeURIComponent("` + escapeURIComponent(raw) + `");`

	got, err := p.evaluatePacked(packed)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := got.([]interface{})
	if !ok || len(arr) != 1 {
		t.Fatalf("expected outer array len 1, got %#v", got)
	}
	inner, ok := arr[0].([]interface{})
	if !ok || len(inner) != 1 {
		t.Fatalf("expected inner array len 1, got %#v", arr[0])
	}
	if m := asMap(inner[0]); m == nil || m["url"] != "https://x/v.mp4" {
		t.Fatalf("expected [{url}], got %#v", inner)
	}
}

func TestEvaluatePackedEmpty(t *testing.T) {
	p := New()
	packed := padded(`window.parent.sf.result.showEmptyResult({"html":"no media"});`)
	_, err := p.evaluatePacked(packed)
	if !stderrors.Is(err, errEmptyResult) {
		t.Fatalf("expected errEmptyResult, got %v", err)
	}
}

func TestEvaluatePackedNoResult(t *testing.T) {
	p := New()
	packed := padded(`var x = 1; x = x + 1;`)
	_, err := p.evaluatePacked(packed)
	if !stderrors.Is(err, downloader.ErrProviderInvalidResponse) {
		t.Fatalf("expected ErrProviderInvalidResponse, got %v", err)
	}
}

func TestTaskIDFromLocation(t *testing.T) {
	cases := []struct {
		location string
		base     string
		want     string
	}{
		{"/tasks/abc?t=abc", "https://du.sf-converter.com", "abc"},
		{"https://du.sf-converter.com/tasks/def?t=def", "https://du.sf-converter.com", "def"},
		{"", "https://du.sf-converter.com", ""},
		{"/tasks/none", "https://du.sf-converter.com", ""},
	}
	for _, tc := range cases {
		if got := taskIDFromLocation(tc.location, tc.base); got != tc.want {
			t.Errorf("taskIDFromLocation(%q) = %q, want %q", tc.location, got, tc.want)
		}
	}
}

func TestFetchPackedForm(t *testing.T) {
	var mu sync.Mutex
	var got url.Values

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		got = r.Form
		mu.Unlock()
		_, _ = w.Write([]byte(padded(`window.parent.sf.videoResult.show([]);`)))
	}))
	defer server.Close()

	p := NewWithConfig(Config{Endpoints: []string{server.URL + "/savefrom.php"}, MaxAttempts: 1})
	_, err := p.fetchPacked(context.Background(), "https://example.com/v", server.URL+"/savefrom.php", Profile{Origin: "https://id.savefrom.net", Lang: "id", Country: "id"})
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got.Get("sf_url") != "https://example.com/v" {
		t.Errorf("sf_url = %q", got.Get("sf_url"))
	}
	if got.Get("_ts") != fixedTS {
		t.Errorf("_ts = %q", got.Get("_ts"))
	}
	if want := sha256Hex("https://example.com/v" + got.Get("ts") + secret); got.Get("_s") != want {
		t.Errorf("_s mismatch")
	}
}

func TestReadSSEUntilDone(t *testing.T) {
	t.Run("done", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("content-type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"status\":\"processing\"}\n\ndata: {\"status\":\"finished\",\"downloadUrl\":\"https://cdn/x.mp4\",\"quality\":\"1080p\"}\n\n"))
		}))
		defer server.Close()

		p := NewWithConfig(Config{ConverterOrigin: server.URL})
		job, err := p.readSSEUntilDone(context.Background(), "t1")
		if err != nil {
			t.Fatal(err)
		}
		if job["downloadUrl"] != "https://cdn/x.mp4" {
			t.Errorf("downloadUrl = %v", job["downloadUrl"])
		}
	})

	t.Run("failed", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("content-type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"status\":\"failed\",\"error\":\"boom\"}\n\n"))
		}))
		defer server.Close()

		p := NewWithConfig(Config{ConverterOrigin: server.URL})
		_, err := p.readSSEUntilDone(context.Background(), "t1")
		if !stderrors.Is(err, downloader.ErrProviderInvalidResponse) {
			t.Fatalf("expected ErrProviderInvalidResponse, got %v", err)
		}
	})
}

func TestResolveConverter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/convert":
			w.Header().Set("location", "/tasks/t2?t=t2")
			w.WriteHeader(http.StatusFound)
		case "/tasks/t2":
			w.Header().Set("content-type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"status\":\"finished\",\"downloadUrl\":\"https://cdn/x.mp4\",\"quality\":\"1080p\"}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p := NewWithConfig(Config{ConverterOrigin: server.URL})
	job, err := p.resolveConverter(context.Background(), server.URL+"/convert?payload=x")
	if err != nil {
		t.Fatal(err)
	}
	if job.URL != "https://cdn/x.mp4" {
		t.Errorf("URL = %q", job.URL)
	}
	if job.TaskID != "t2" {
		t.Errorf("TaskID = %q, want t2", job.TaskID)
	}
	if job.Quality != "1080p" {
		t.Errorf("Quality = %q", job.Quality)
	}
}

func TestResolveFullFlowDirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/savefrom.php" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(padded(`window.parent.sf.videoResult.show({"id":"v1","thumb":"https://img/t.jpg","meta":{"title":"T","source":"s","duration":125},"url":[{"url":"https://cdn.example.com/v.mp4","quality":"720p","ext":"mp4","type":"mp4","filesize":"12345"}]});`)))
	}))
	defer server.Close()

	p := NewWithConfig(Config{Endpoints: []string{server.URL + "/savefrom.php"}, MaxAttempts: 1})

	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://example.com/video/1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Platform != downloader.PlatformSavefrom {
		t.Errorf("Platform = %q", result.Platform)
	}
	if result.URL != "https://example.com/video/1" {
		t.Errorf("URL = %q", result.URL)
	}
	if result.Title != "T" {
		t.Errorf("Title = %q", result.Title)
	}
	if result.Thumbnail != "https://img/t.jpg" {
		t.Errorf("Thumbnail = %q", result.Thumbnail)
	}
	if result.DurationMs != 125000 {
		t.Errorf("DurationMs = %d", result.DurationMs)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("Formats len = %d", len(result.Formats))
	}
	f := result.Formats[0]
	if f.Type != downloader.MediaVideo || f.URL != "https://cdn.example.com/v.mp4" || f.Quality != "720p" || f.Ext != "mp4" || f.Size != 12345 {
		t.Errorf("Format = %+v", f)
	}
	if result.Type != downloader.MediaVideo {
		t.Errorf("Type = %q", result.Type)
	}
}

func TestResolveFullFlowWithConverter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/savefrom.php":
			_, _ = w.Write([]byte(padded(`window.parent.sf.videoResult.show({"id":"v1","thumb":"","meta":{},"url":[{"url":"https://du.sf-converter.com/convert?payload=abc","quality":"1080p","ext":"mp4","type":"mp4"}]});`)))
		case "/convert":
			w.Header().Set("location", "/tasks/ct?t=ct")
			w.WriteHeader(http.StatusFound)
		case "/tasks/ct":
			w.Header().Set("content-type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"status\":\"finished\",\"downloadUrl\":\"https://cdn.example.com/final.mp4\",\"quality\":\"1080p\"}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	to, _ := url.Parse(server.URL)
	p := NewWithConfig(Config{
		Endpoints:       []string{server.URL + "/savefrom.php"},
		ConverterOrigin: server.URL,
		MaxAttempts:     1,
		HTTPClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &hostRewriteTransport{from: "du.sf-converter.com", to: to},
		},
	})

	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://example.com/video/1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("Formats len = %d", len(result.Formats))
	}
	if result.Formats[0].URL != "https://cdn.example.com/final.mp4" {
		t.Errorf("Format URL = %q", result.Formats[0].URL)
	}
	if result.Formats[0].Quality != "1080p" {
		t.Errorf("Format Quality = %q", result.Formats[0].Quality)
	}
}

func TestResolveWorkerUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	p := NewWithConfig(Config{Endpoints: []string{server.URL + "/savefrom.php"}, MaxAttempts: 1})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://example.com/video/1"})
	if !stderrors.Is(err, downloader.ErrProviderUnavailable) {
		t.Fatalf("expected ErrProviderUnavailable, got %v", err)
	}
}

func TestResolveShortResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("short"))
	}))
	defer server.Close()

	p := NewWithConfig(Config{Endpoints: []string{server.URL + "/savefrom.php"}, MaxAttempts: 1})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://example.com/video/1"})
	if !stderrors.Is(err, downloader.ErrProviderInvalidResponse) {
		t.Fatalf("expected ErrProviderInvalidResponse, got %v", err)
	}
}

func TestResolveEmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(padded(`window.parent.sf.result.showEmptyResult({"html":"no media"});`)))
	}))
	defer server.Close()

	p := NewWithConfig(Config{Endpoints: []string{server.URL + "/savefrom.php"}, MaxAttempts: 1})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://example.com/video/1"})
	if !stderrors.Is(err, downloader.ErrMediaNotFound) {
		t.Fatalf("expected ErrMediaNotFound, got %v", err)
	}
}

func escapeURIComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

type hostRewriteTransport struct {
	base http.RoundTripper
	from string
	to   *url.URL
}

func (t *hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Hostname() == t.from {
		req.URL.Scheme = t.to.Scheme
		req.URL.Host = t.to.Host
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}
