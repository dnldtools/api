package ninexbuddy

import (
	"context"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"rest-api/internal/downloader"
)

const testURL = "https://vimeo.com/347119375"

func TestProviderContract(t *testing.T) {
	p := New()
	if p.Name() != "9xbuddy" {
		t.Errorf("Name() = %q, want 9xbuddy", p.Name())
	}
	if p.Platform() != downloader.Platform9xbuddy {
		t.Errorf("Platform() = %q, want 9xbuddy", p.Platform())
	}
	if p.Type() != downloader.ProviderExternalAPI {
		t.Errorf("Type() = %q, want external_api", p.Type())
	}
}

func TestMatchesURL(t *testing.T) {
	p := New()
	for _, u := range []string{
		"https://vimeo.com/347119375",
		"http://example.com/video",
		"https://dailymotion.com/video/xyz",
		"https://pornhub.com/view_video.php?viewkey=abc",
	} {
		if !p.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false, want true", u)
		}
	}
	for _, u := range []string{
		"",
		"example.com/video",
		"ftp://example.com/file",
		"mailto:user@example.com",
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

func TestResolveNonHTTPURL(t *testing.T) {
	p := New()
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "ftp://example.com/file"})
	if !stderrors.Is(err, downloader.ErrInvalidURL) {
		t.Fatalf("error = %v, want ErrInvalidURL", err)
	}
}

func TestEncode64Decode64(t *testing.T) {
	if got := encode64("hello"); got != "aGVsbG8=" {
		t.Errorf("encode64(hello) = %q, want aGVsbG8=", got)
	}
	if got := string(decode64("aGVsbG8=")); got != "hello" {
		t.Errorf("decode64(aGVsbG8=) = %q, want hello", got)
	}
	if got := decode64("!!!!"); got != nil {
		t.Errorf("decode64(!!!!) = %v, want nil", got)
	}
	if got := decode64("aGVsbG8"); got != nil { // length not a multiple of 4
		t.Errorf("decode64(aGVsbG8) = %v, want nil", got)
	}
}

func TestEncryptDecryptVector(t *testing.T) {
	if got := encrypt("hello", "key"); got != "4dDR5do=" {
		t.Errorf("encrypt(hello, key) = %q, want 4dDR5do=", got)
	}
	if got := decrypt("4dDR5do=", "key"); got != "hello" {
		t.Errorf("decrypt(4dDR5do=, key) = %q, want hello", got)
	}
}

func TestSecretConstants(t *testing.T) {
	if got := sorryMate(); got != "SORRY_MATE" {
		t.Errorf("sorryMate() = %q, want SORRY_MATE", got)
	}
	if got := secretPhrase(); got != "SORRY_MATE_IM_NOT_GONNA_TELL_YOU" {
		t.Errorf("secretPhrase() = %q, want SORRY_MATE_IM_NOT_GONNA_TELL_YOU", got)
	}
}

func TestMakeAuthTokenVector(t *testing.T) {
	p := New()
	s := &session{
		init:     map[string]interface{}{"ua": "uaHEAD", "appVersion": "1.2.3"},
		cssHash:  "abc123",
		hostname: "9xbuddy.site",
	}
	const want = "mquUpsfG2mGlmtfHlGVjlMXDpXR3ecTXtIKEg7zBrnSGdsKrrpKAgLfBqIKAf6TBtXh+fcK7sIiqk9jGxaxjY5bV1pehXpSQk2FlYpGUj2Y="
	if got := p.makeAuthToken(s); got != want {
		t.Errorf("makeAuthToken() = %q, want %q", got, want)
	}
}

func TestEncodeURIComponent(t *testing.T) {
	cases := map[string]string{
		"https://example.com/a b&c=d": "https%3A%2F%2Fexample.com%2Fa%20b%26c%3Dd",
		"https://example.com/~!*'()":  "https%3A%2F%2Fexample.com%2F~!*'()",
	}
	for in, want := range cases {
		if got := encodeURIComponent(in); got != want {
			t.Errorf("encodeURIComponent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecodeBuddyURLVector(t *testing.T) {
	p := New()
	s := &session{cssHash: "9xbuddy", hostname: "example.com", accessToken: ""}
	// key = sorryMate() + "11" + "9xbuddy" + "" -> "SORRY_MATE119xbuddy"
	// Build a hex-encoded buddy URL from a known plain URL.
	key := sorryMate() + strconv.Itoa(len(s.hostname)) + s.cssHash + s.accessToken
	enc := encrypt("https://example.com/v.mp4", key)
	reversed := reverseString(enc)
	hexEncoded := hex.EncodeToString([]byte(reversed))

	if got := p.decodeBuddyURL(hexEncoded, s); got != "https://example.com/v.mp4" {
		t.Errorf("decodeBuddyURL() = %q, want https://example.com/v.mp4", got)
	}

	// Non-hex and plain http(s) values pass through untouched.
	if got := p.decodeBuddyURL("https://example.com/direct.mp4", s); got != "https://example.com/direct.mp4" {
		t.Errorf("decodeBuddyURL(http) = %q, want unchanged", got)
	}
	if got := p.decodeBuddyURL("not-hex-value", s); got != "not-hex-value" {
		t.Errorf("decodeBuddyURL(non-hex) = %q, want unchanged", got)
	}
	if got := p.decodeBuddyURL("//cdn.example.com/x.mp4", s); got != "https://cdn.example.com/x.mp4" {
		t.Errorf("decodeBuddyURL(protocol-relative) = %q, want https://cdn.example.com/x.mp4", got)
	}
}

func TestDecodeTree(t *testing.T) {
	p := New()
	s := &session{cssHash: "9xbuddy", hostname: "example.com", accessToken: "tok"}
	key := sorryMate() + strconv.Itoa(len(s.hostname)) + s.cssHash + s.accessToken
	enc := encrypt("https://cdn.example.com/v.mp4", key)
	hexEncoded := hex.EncodeToString([]byte(reverseString(enc)))

	data := map[string]interface{}{
		"url": hexEncoded,
		"response": map[string]interface{}{
			"formats": []interface{}{
				map[string]interface{}{"url": hexEncoded},
				map[string]interface{}{"url": "https://cdn.example.com/direct.mp4"},
			},
		},
	}

	p.decodeTree(data, s)

	if got := str(data["url"]); got != "https://cdn.example.com/v.mp4" {
		t.Errorf("top-level url = %q, want decoded", got)
	}
	resp := asMap(data["response"])
	formats := sliceOf(resp["formats"])
	if got := str(asMap(formats[0])["url"]); got != "https://cdn.example.com/v.mp4" {
		t.Errorf("formats[0].url = %q, want decoded", got)
	}
	if got := str(asMap(formats[0])["url_encoded"]); got != hexEncoded {
		t.Errorf("formats[0].url_encoded = %q, want original hex", got)
	}
	if got := str(asMap(formats[1])["url"]); got != "https://cdn.example.com/direct.mp4" {
		t.Errorf("formats[1].url = %q, want unchanged", got)
	}
}

func TestParseDownloadTicket(t *testing.T) {
	cases := []struct {
		in     string
		uid    string
		url    string
		wantOK bool
	}{
		{"/download/uid1/some-url", "uid1", "some-url", true},
		{"https://ab.9xbud.com/download/uid2/a/b", "uid2", "a/b", true},
		{"https://cdn.example.com/v.mp4", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		uid, u, ok := parseDownloadTicket(tc.in)
		if ok != tc.wantOK || uid != tc.uid || u != tc.url {
			t.Errorf("parseDownloadTicket(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.in, uid, u, ok, tc.uid, tc.url, tc.wantOK)
		}
	}
}

func TestResolveFullFlow(t *testing.T) {
	var got struct {
		tokenAuth     string
		tokenAccess   string
		extractAuth   string
		extractAccess string
		extractURL    string
		extractEngine string
		downloadUID   string
		downloadURL   string
		downloadMode  string
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			_, _ = w.Write([]byte(siteHTML("abc123")))
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			got.tokenAuth = r.Header.Get("x-auth-token")
			got.tokenAccess = r.Header.Get("x-access-token")
			_, _ = w.Write([]byte(`{"access_token":"atok123"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/extract":
			got.extractAuth = r.Header.Get("x-auth-token")
			got.extractAccess = r.Header.Get("x-access-token")
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			got.extractURL = str(body["url"])
			got.extractEngine = str(body["searchEngine"])
			_, _ = w.Write([]byte(`{"response":{"title":"Sample Video","thumbnail":"https://cdn.example.com/t.jpg","formats":[{"type":"video","quality":"1080p","ext":"mp4","url":"/download/uid1/ticket-value"}]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/download":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			got.downloadUID = str(body["uid"])
			got.downloadURL = str(body["url"])
			got.downloadMode = str(body["mode"])
			_, _ = w.Write([]byte(`{"url":"https://cdn.example.com/v.mp4","size":1234}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewWithConfig(Config{SiteURL: srv.URL, APIURL: srv.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if result.Platform != downloader.Platform9xbuddy {
		t.Errorf("Platform = %q, want 9xbuddy", result.Platform)
	}
	if result.URL != testURL {
		t.Errorf("URL = %q, want %q", result.URL, testURL)
	}
	if result.Title != "Sample Video" {
		t.Errorf("Title = %q, want Sample Video", result.Title)
	}
	if result.Thumbnail != "https://cdn.example.com/t.jpg" {
		t.Errorf("Thumbnail = %q, want t.jpg", result.Thumbnail)
	}
	if result.Type != downloader.MediaVideo {
		t.Errorf("Type = %q, want video", result.Type)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d, want 1", len(result.Formats))
	}
	f := result.Formats[0]
	if f.URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("Formats[0].URL = %q, want v.mp4", f.URL)
	}
	if f.Quality != "1080p" || f.Ext != "mp4" || f.Size != 1234 {
		t.Errorf("Formats[0] = %+v, want quality 1080p / ext mp4 / size 1234", f)
	}

	host := uHost(t, srv.URL)
	wantAuth := p.makeAuthToken(&session{init: map[string]interface{}{"ua": "uaHEAD", "appVersion": "1.2.3"}, cssHash: "abc123", hostname: host})

	if got.tokenAuth == "" {
		t.Error("token request missing x-auth-token")
	}
	if got.tokenAccess != "" {
		t.Errorf("token request x-access-token = %q, want empty", got.tokenAccess)
	}
	if got.extractAuth != wantAuth {
		t.Errorf("extract x-auth-token = %q, want %q", got.extractAuth, wantAuth)
	}
	if got.extractAccess != "atok123" {
		t.Errorf("extract x-access-token = %q, want atok123", got.extractAccess)
	}
	if got.extractURL != "https%3A%2F%2Fvimeo.com%2F347119375" {
		t.Errorf("extract url = %q, want encoded vimeo url", got.extractURL)
	}
	if got.extractEngine != "yt" {
		t.Errorf("extract searchEngine = %q, want yt", got.extractEngine)
	}
	if got.downloadUID != "uid1" || got.downloadURL != "ticket-value" || got.downloadMode != "inspect" {
		t.Errorf("download body = (uid=%q, url=%q, mode=%q), want (uid1, ticket-value, inspect)", got.downloadUID, got.downloadURL, got.downloadMode)
	}
}

func TestResolveDirectFormatNoTicket(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(siteHTML("abc123")))
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"tok"}`))
		case "/extract":
			_, _ = w.Write([]byte(`{"response":{"formats":[{"type":"audio","quality":"128kbps","ext":"m4a","url":"https://cdn.example.com/audio.m4a"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewWithConfig(Config{SiteURL: srv.URL, APIURL: srv.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(result.Formats) != 1 || result.Formats[0].URL != "https://cdn.example.com/audio.m4a" {
		t.Fatalf("Formats = %+v, want direct audio url", result.Formats)
	}
	if result.Formats[0].Type != downloader.MediaAudio {
		t.Errorf("Formats[0].Type = %q, want audio", result.Formats[0].Type)
	}
	if result.Type != downloader.MediaAudio {
		t.Errorf("Type = %q, want audio", result.Type)
	}
}

func TestResolveTokenMissingIsInvalidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(siteHTML("abc123")))
		case "/token":
			_, _ = w.Write([]byte(`{"status":false,"message":"BLOCKED"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewWithConfig(Config{SiteURL: srv.URL, APIURL: srv.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testURL})
	if !stderrors.Is(err, downloader.ErrProviderInvalidResponse) {
		t.Fatalf("error = %v, want ErrProviderInvalidResponse", err)
	}
}

func TestResolveExtractMessageIsInvalidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(siteHTML("abc123")))
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"tok"}`))
		case "/extract":
			_, _ = w.Write([]byte(`{"status":false,"message":"BLOCKED"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewWithConfig(Config{SiteURL: srv.URL, APIURL: srv.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testURL})
	if !stderrors.Is(err, downloader.ErrProviderInvalidResponse) {
		t.Fatalf("error = %v, want ErrProviderInvalidResponse", err)
	}
}

func TestResolveEmptyFormatsIsMediaNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(siteHTML("abc123")))
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"tok"}`))
		case "/extract":
			_, _ = w.Write([]byte(`{"response":{"formats":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewWithConfig(Config{SiteURL: srv.URL, APIURL: srv.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testURL})
	if !stderrors.Is(err, downloader.ErrMediaNotFound) {
		t.Fatalf("error = %v, want ErrMediaNotFound", err)
	}
}

func TestResolveSite5xxIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := NewWithConfig(Config{SiteURL: srv.URL, APIURL: srv.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testURL})
	if !stderrors.Is(err, downloader.ErrProviderUnavailable) {
		t.Fatalf("error = %v, want ErrProviderUnavailable", err)
	}
}

func TestResolveTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(siteHTML("abc123")))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{SiteURL: srv.URL, APIURL: srv.URL, Timeout: 50 * time.Millisecond})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testURL})
	if !stderrors.Is(err, downloader.ErrProviderTimeout) {
		t.Fatalf("error = %v, want ErrProviderTimeout", err)
	}
}

func siteHTML(cssHash string) string {
	return `<html><head><link rel="stylesheet" href="/build/assets/main.` + cssHash + `.css"></head><body><script>window.__INIT__ = {"ua":"uaHEAD","appVersion":"1.2.3"};</script></body></html>`
}

func uHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return u.Hostname()
}
