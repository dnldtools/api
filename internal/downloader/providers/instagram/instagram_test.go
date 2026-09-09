package instagram

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

const testPostURL = "https://www.instagram.com/p/abc12/"

const testStoryURL = "https://www.instagram.com/stories/alice/123/"

func TestProviderContract(t *testing.T) {
	p := New()
	if p.Name() != "instagram" {
		t.Errorf("Name() = %q, want instagram", p.Name())
	}
	if p.Platform() != downloader.PlatformInstagram {
		t.Errorf("Platform() = %q, want instagram", p.Platform())
	}
	if p.Type() != downloader.ProviderExternalAPI {
		t.Errorf("Type() = %q, want external_api", p.Type())
	}
}

func TestMatchesURL(t *testing.T) {
	p := New()
	for _, u := range []string{
		"https://www.instagram.com/p/abc/",
		"https://instagram.com/reel/abc/",
		"http://instagr.am/p/abc/",
	} {
		if !p.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false, want true", u)
		}
	}
	if p.MatchesURL("https://www.facebook.com/reel/123") {
		t.Error("MatchesURL(facebook) = true, want false")
	}
}

func TestResolveEmptyURL(t *testing.T) {
	p := New()
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: ""})
	if !stderrors.Is(err, downloader.ErrInvalidURL) {
		t.Fatalf("error = %v, want ErrInvalidURL", err)
	}
}

func TestResolveNonInstagramURL(t *testing.T) {
	p := New()
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://www.facebook.com/reel/123"})
	if !stderrors.Is(err, downloader.ErrInvalidURL) {
		t.Fatalf("error = %v, want ErrInvalidURL", err)
	}
}

func TestBuildMetadataRichOfficialFields(t *testing.T) {
	post := map[string]interface{}{
		"shortcode":          "abc12",
		"media_type":         float64(2),
		"product_type":       "clips",
		"video_duration":     12.5,
		"video_view_count":   float64(9876),
		"taken_at_timestamp": float64(1700000000),
		"dimensions":         map[string]interface{}{"width": float64(1080), "height": float64(1920)},
		"owner": map[string]interface{}{
			"id":              "123",
			"username":        "alice",
			"full_name":       "Alice Doe",
			"profile_pic_url": "https://cdn.example.com/avatar.jpg",
			"is_verified":     true,
		},
		"caption":                 "Hello world",
		"edge_media_preview_like": map[string]interface{}{"count": float64(42)},
		"edge_media_to_comment":   map[string]interface{}{"count": float64(7)},
		"location":                map[string]interface{}{"name": "Jakarta"},
	}

	md := buildMetadata(post)
	checks := map[string]string{
		"author":             "alice",
		"author_name":        "Alice Doe",
		"author_id":          "123",
		"author_profile_pic": "https://cdn.example.com/avatar.jpg",
		"author_verified":    "true",
		"caption":            "Hello world",
		"likes":              "42",
		"comments":           "7",
		"views":              "9876",
		"width":              "1080",
		"height":             "1920",
		"media_type":         "video",
		"product_type":       "clips",
		"location":           "Jakarta",
		"shortcode":          "abc12",
		"taken_at":           "2023-11-14T22:13:20Z",
	}
	for k, want := range checks {
		if got := md[k]; got != want {
			t.Errorf("metadata[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestBuildResultSetsDurationMs(t *testing.T) {
	post := map[string]interface{}{"video_duration": 12.5}
	items := []mediaItem{{kind: "video", url: "https://cdn.example.com/v.mp4"}}

	result := New().buildResult(post, items)

	if result.DurationMs != 12500 {
		t.Errorf("DurationMs = %d, want 12500", result.DurationMs)
	}
	if got := result.Metadata["author"]; got != "" {
		t.Errorf("Metadata author = %q, want empty (no owner in post)", got)
	}
}

func TestResolveOfficialEmbeddedVideo(t *testing.T) {
	htmlBody := `<html><head><script type="application/json">{"xig_polaris_media":{"code":"abc12","caption":"Hello world","user":{"username":"alice"},"media_type":2,"video_url":"https:\/\/cdn.example.com\/v.mp4","display_url":"https:\/\/cdn.example.com\/t.jpg"}}</script></head><body></body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{RelayBaseURL: srv.URL, SnapinstaBaseURL: srv.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testPostURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Platform != downloader.PlatformInstagram {
		t.Errorf("Platform = %q, want instagram", result.Platform)
	}
	if result.URL != testPostURL {
		t.Errorf("URL = %q, want %q", result.URL, testPostURL)
	}
	if result.Title != "Hello world" {
		t.Errorf("Title = %q, want Hello world", result.Title)
	}
	if result.Thumbnail != "https://cdn.example.com/t.jpg" {
		t.Errorf("Thumbnail = %q, want https://cdn.example.com/t.jpg", result.Thumbnail)
	}
	if result.Type != downloader.MediaVideo {
		t.Errorf("Type = %q, want video", result.Type)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d, want 1", len(result.Formats))
	}
	if result.Formats[0].URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("Formats[0].URL = %q, want https://cdn.example.com/v.mp4", result.Formats[0].URL)
	}
	if got := result.Metadata["author"]; got != "alice" {
		t.Errorf("Metadata author = %q, want alice", got)
	}
}

func TestResolveOfficialEmbedFallback(t *testing.T) {
	pageBody := strings.Repeat("x", 400) + `"code":"abc12"`

	embedBody := `<html><script type="text/javascript">window.__additionalDataLoaded('extra', {"shortcode_media":{"media_type":2,"video_url":"https://cdn.example.com/v.mp4","display_url":"https://cdn.example.com/t.jpg","owner":{"username":"alice"},"caption":"Embed cap"}});</script></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "embed") {
			_, _ = w.Write([]byte(embedBody))
			return
		}
		_, _ = w.Write([]byte(pageBody))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{RelayBaseURL: srv.URL, SnapinstaBaseURL: srv.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testPostURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Title != "Embed cap" {
		t.Errorf("Title = %q, want Embed cap", result.Title)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d, want 1", len(result.Formats))
	}
	if result.Formats[0].Type != downloader.MediaVideo || result.Formats[0].URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("Formats[0] = %+v, want video / v.mp4", result.Formats[0])
	}
	if got := result.Metadata["author"]; got != "alice" {
		t.Errorf("Metadata author = %q, want alice", got)
	}
}

func TestResolveOfficialGraphQLFallback(t *testing.T) {
	pageBody := strings.Repeat("x", 400) + `"code":"abc12"`

	gqlBody := `{"data":{"xdt_shortcode_media":{"media_type":1,"display_url":"https:\/\/cdn.example.com\/photo.jpg","edge_media_to_caption":{"edges":[{"node":{"text":"Cap"}}]}}}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "graphql") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(gqlBody))
			return
		}
		_, _ = w.Write([]byte(pageBody))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{RelayBaseURL: srv.URL, SnapinstaBaseURL: srv.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testPostURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Title != "Cap" {
		t.Errorf("Title = %q, want Cap", result.Title)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d, want 1", len(result.Formats))
	}
	if result.Formats[0].Type != downloader.MediaImage || result.Formats[0].URL != "https://cdn.example.com/photo.jpg" {
		t.Errorf("Formats[0] = %+v, want image / photo.jpg", result.Formats[0])
	}
	if result.Type != downloader.MediaImage {
		t.Errorf("Type = %q, want image", result.Type)
	}
}

func TestResolveSnapinstaFlow(t *testing.T) {
	thumb := "https://d.rapidcdn.app/thumb?token=" + fakeJWT(`{"url":"https://cdn.example.com/t.jpg"}`)
	video := "https://d.rapidcdn.app/v2?token=" + fakeJWT(`{"url":"https://cdn.example.com/v.mp4","filename":"clip.mp4"}`)
	decoded := thumb + "\n" + video

	var got struct {
		url    string
		action string
		lang   string
		token  string
	}

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer relay.Close()

	snap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><input type="hidden" name="token" value="tok123"></html>`))
		case "/action2.php":
			_ = r.ParseForm()
			got.url = r.FormValue("url")
			got.action = r.FormValue("action")
			got.lang = r.FormValue("lang")
			got.token = r.FormValue("token")
			_, _ = w.Write([]byte(evalPayload(decoded)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer snap.Close()

	p := NewWithConfig(Config{RelayBaseURL: relay.URL, SnapinstaBaseURL: snap.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testPostURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got.url != testPostURL {
		t.Errorf("form url = %q, want %q", got.url, testPostURL)
	}
	if got.action != "post" {
		t.Errorf("form action = %q, want post", got.action)
	}
	if got.lang != "en" {
		t.Errorf("form lang = %q, want en", got.lang)
	}
	if got.token != "tok123" {
		t.Errorf("form token = %q, want tok123", got.token)
	}

	if result.Thumbnail != "https://cdn.example.com/t.jpg" {
		t.Errorf("Thumbnail = %q, want t.jpg", result.Thumbnail)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d, want 1", len(result.Formats))
	}
	if result.Formats[0].URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("Formats[0].URL = %q, want v.mp4", result.Formats[0].URL)
	}
	if result.Formats[0].Type != downloader.MediaVideo {
		t.Errorf("Formats[0].Type = %q, want video", result.Formats[0].Type)
	}
	if result.Type != downloader.MediaVideo {
		t.Errorf("Type = %q, want video", result.Type)
	}
}

func TestResolveStorySkipsOfficial(t *testing.T) {
	var relayHits int
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayHits++
		http.NotFound(w, r)
	}))
	defer relay.Close()

	snapsave := httptest.NewServer(http.NotFoundHandler())
	defer snapsave.Close()

	thumb := "https://d.rapidcdn.app/thumb?token=" + fakeJWT(`{"url":"https://cdn.example.com/s.jpg"}`)
	snap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte(`<html><input type="hidden" name="token" value="t"></html>`))
			return
		}
		_, _ = w.Write([]byte(evalPayload(thumb)))
	}))
	defer snap.Close()

	p := NewWithConfig(Config{
		RelayBaseURL:      relay.URL,
		SnapinstaBaseURL:  snap.URL,
		SnapsaveHomeURL:   snapsave.URL,
		SnapsaveActionURL: snapsave.URL,
	})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testStoryURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(result.Formats) != 1 || result.Formats[0].URL != "https://cdn.example.com/s.jpg" {
		t.Fatalf("Formats = %+v, want s.jpg", result.Formats)
	}
	if relayHits != 0 {
		t.Errorf("relay hits = %d, want 0 (story must skip official)", relayHits)
	}
}

func TestResolveStoryUsesSnapsaveFirst(t *testing.T) {
	var relayHits int
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayHits++
		http.NotFound(w, r)
	}))
	defer relay.Close()

	thumb := "https://d.rapidcdn.app/thumb?token=" + fakeJWT(`{"url":"https://cdn.example.com/s.jpg"}`)
	snapsave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(evalPayload(thumb)))
	}))
	defer snapsave.Close()

	snap := httptest.NewServer(http.NotFoundHandler()) // must not be reached
	defer snap.Close()

	p := NewWithConfig(Config{
		RelayBaseURL:      relay.URL,
		SnapinstaBaseURL:  snap.URL,
		SnapsaveHomeURL:   snapsave.URL,
		SnapsaveActionURL: snapsave.URL,
	})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testStoryURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(result.Formats) != 1 || result.Formats[0].URL != "https://cdn.example.com/s.jpg" {
		t.Fatalf("Formats = %+v, want s.jpg", result.Formats)
	}
	if relayHits != 0 {
		t.Errorf("relay hits = %d, want 0 (story must skip official)", relayHits)
	}
}

func TestResolveMediaInstasaveFallback(t *testing.T) {
	token := fakeJWT(`{"url":"https://cdn.example.com/v.mp4"}`)
	thumbToken := fakeJWT(`{"url":"https://cdn.example.com/t.jpg"}`)
	body := strings.Repeat("x", 500) +
		`<a href="https://cdn.instasave.website/?token=` + token + `"></a>` +
		`<img src="https://cdn.instasave.website/?token=` + thumbToken + `">`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "api.instasave.website") {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	snap := httptest.NewServer(http.NotFoundHandler())
	defer snap.Close()

	p := NewWithConfig(Config{RelayBaseURL: srv.URL, SnapinstaBaseURL: snap.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testPostURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(result.Formats) != 1 || result.Formats[0].URL != "https://cdn.example.com/v.mp4" {
		t.Fatalf("Formats = %+v, want v.mp4", result.Formats)
	}
	if result.Thumbnail != "https://cdn.example.com/t.jpg" {
		t.Errorf("Thumbnail = %q, want t.jpg", result.Thumbnail)
	}
}

func TestDecodeHexEscapes(t *testing.T) {
	if got := decodeHexEscapes(`a\x41b\x2Fc\x3Dd`); got != "aAb/c=d" {
		t.Errorf("decodeHexEscapes() = %q, want aAb/c=d", got)
	}
	if got := decodeHexEscapes(`\xZZ`); got != `\xZZ` {
		t.Errorf("decodeHexEscapes(invalid) = %q, want unchanged", got)
	}
}

func TestResolveSnapinstaEmptyIsMediaNotFound(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer relay.Close()

	snap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte(`<html><input type="hidden" name="token" value="t"></html>`))
			return
		}
		_, _ = w.Write([]byte(evalPayload(strings.Repeat("no tokens ", 20))))
	}))
	defer snap.Close()

	p := NewWithConfig(Config{RelayBaseURL: relay.URL, SnapinstaBaseURL: snap.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testPostURL})
	if !stderrors.Is(err, downloader.ErrMediaNotFound) {
		t.Fatalf("error = %v, want ErrMediaNotFound", err)
	}
}

func TestResolveSnapinstaHome5xxIsUnavailable(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer relay.Close()

	snap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer snap.Close()

	p := NewWithConfig(Config{RelayBaseURL: relay.URL, SnapinstaBaseURL: snap.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testPostURL})
	if !stderrors.Is(err, downloader.ErrProviderUnavailable) {
		t.Fatalf("error = %v, want ErrProviderUnavailable", err)
	}
}

func TestResolveTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`<html></html>`))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{RelayBaseURL: srv.URL, Timeout: 50 * time.Millisecond})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testPostURL})
	if !stderrors.Is(err, downloader.ErrProviderTimeout) {
		t.Fatalf("error = %v, want ErrProviderTimeout", err)
	}
}

func TestShortcodeOf(t *testing.T) {
	cases := map[string]string{
		"https://www.instagram.com/p/ABC123xyz/":      "ABC123xyz",
		"https://www.instagram.com/reel/AbC_12-34/":   "AbC_12-34",
		"https://www.instagram.com/tv/xyzzy1/":        "xyzzy1",
		"https://www.instagram.com/p/AbCdEf12/?utm=1": "AbCdEf12",
		"https://www.instagram.com/explore/tags/cat/": "",
		"https://www.instagram.com/alice/":            "",
	}
	for u, want := range cases {
		if got := shortcodeOf(u); got != want {
			t.Errorf("shortcodeOf(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestIsStory(t *testing.T) {
	if !isStory(testStoryURL) {
		t.Errorf("isStory(%q) = false, want true", testStoryURL)
	}
	if isStory(testPostURL) {
		t.Errorf("isStory(%q) = true, want false", testPostURL)
	}
}

func TestUnescapeURL(t *testing.T) {
	cases := map[string]string{
		`https://x.com/a\u0026b=c`: "https://x.com/a&b=c",
		`https://x.com/a\u00253Db`: "https://x.com/a=b",
		`https:\/\/x.com\/a`:       "https://x.com/a",
	}
	for in, want := range cases {
		if got := unescapeURL(in); got != want {
			t.Errorf("unescapeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecodeSnapinsta(t *testing.T) {
	if got := decodeSnapinsta(evalPayload("hello decoded")); got != "hello decoded" {
		t.Errorf("decodeSnapinsta() = %q, want hello decoded", got)
	}
	if got := decodeSnapinsta("no eval here"); got != "" {
		t.Errorf("decodeSnapinsta(non-eval) = %q, want empty", got)
	}
}

func TestItemsFromSnapinsta(t *testing.T) {
	thumb := "https://d.rapidcdn.app/thumb?token=" + fakeJWT(`{"url":"https://cdn.example.com/t.jpg"}`)
	video := "https://d.rapidcdn.app/v2?token=" + fakeJWT(`{"url":"https://cdn.example.com/v.mp4","filename":"clip.mp4"}`)

	items := itemsFromSnapinsta(thumb + "\n" + video)
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].kind != "video" || items[0].url != "https://cdn.example.com/v.mp4" || items[0].thumb != "https://cdn.example.com/t.jpg" {
		t.Errorf("items[0] = %+v, want video / v.mp4 / t.jpg", items[0])
	}
}

func TestItemsFromSnapinstaIgnoresInvalidToken(t *testing.T) {
	items := itemsFromSnapinsta("https://d.rapidcdn.app/v2?token=not.a.jwt")
	if len(items) != 0 {
		t.Fatalf("len(items) = %d, want 0", len(items))
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

func fakeJWT(payload string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return header + "." + body + ".sig"
}

func evalPayload(inner string) string {
	b, _ := json.Marshal(inner)
	return `eval(function(h,u,n,t,e,r){return ` + string(b) + `;}('x','y'))`
}

func TestQueryOfficialSendsInstagramCookie(t *testing.T) {
	htmlBody := `<html><head><script type="application/json">{"xig_polaris_media":{"code":"abc12","caption":"Hi","user":{"username":"alice"},"media_type":2,"video_url":"https://cdn.example.com/v.mp4","display_url":"https://cdn.example.com/t.jpg"}}</script></head></html>`

	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{RelayBaseURL: srv.URL, InstagramCookie: "sessionid=abc; csrftoken=xyz"})
	if _, err := p.queryOfficial(context.Background(), testPostURL, "abc12"); err != nil {
		t.Fatalf("queryOfficial() error = %v", err)
	}
	if gotCookie != "sessionid=abc; csrftoken=xyz" {
		t.Errorf("Cookie header = %q, want configured cookie", gotCookie)
	}
}

func TestCookieValue(t *testing.T) {
	cases := map[string]string{
		"csrftoken":  "xyz",
		"ds_user_id": "123",
		"missing":    "",
	}
	header := "sessionid=abc; csrftoken=xyz; ds_user_id=123"
	for name, want := range cases {
		if got := cookieValue(header, name); got != want {
			t.Errorf("cookieValue(%q) = %q, want %q", name, got, want)
		}
	}
}
