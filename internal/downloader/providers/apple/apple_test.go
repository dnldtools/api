package apple

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

const testAppleURL = "https://music.apple.com/id/album/rodecia-single/6790279558"

func TestProviderContract(t *testing.T) {
	p := New()
	if p.Name() != "apple" {
		t.Errorf("Name() = %q, want apple", p.Name())
	}
	if p.Platform() != downloader.PlatformApple {
		t.Errorf("Platform() = %q, want apple", p.Platform())
	}
	if p.Type() != downloader.ProviderExternalAPI {
		t.Errorf("Type() = %q, want external_api", p.Type())
	}
}

func TestMatchesURL(t *testing.T) {
	p := New()
	cases := []struct {
		url  string
		want bool
	}{
		{testAppleURL, true},
		{"https://music.apple.com/gb/song/hollow-eyes/1883055986", true},
		{"https://itunes.apple.com/us/album/x/123", true},
		{"https://geo.music.apple.com/id/album/x/1", true},
		{"https://www.facebook.com/reel/1", false},
		{"not-a-url", false},
	}
	for _, tc := range cases {
		if got := p.MatchesURL(tc.url); got != tc.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tc.url, got, tc.want)
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

func TestResolveAaplAlbum(t *testing.T) {
	var sawSWD bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" || r.URL.Path == "":
			w.Header().Set("Set-Cookie", "PHPSESSID=abc; Path=/")
			_, _ = w.Write([]byte("<html>ok</html>"))
		case strings.HasPrefix(r.URL.Path, "/api/pl.php"):
			_, _ = w.Write([]byte(`{"album_details":{"album":"Rodecia - Single","artist":"Kangen Band","thumb":"https://th.example/t.jpg","count":1,"0":{"link":"https://music.apple.com/id/album/rodecia/6790279558?i=6790279559","name":"Rodecia","artist":"Kangen Band","duration":"4m 11s","thumb":"https://th.example/t.jpg","album":"Rodecia - Single"}}}`))
		case r.URL.Path == "/api/composer/swd.php":
			sawSWD = true
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			if r.Form.Get("song_name") != "Rodecia" {
				t.Errorf("song_name = %q", r.Form.Get("song_name"))
			}
			if r.Form.Get("quality") != "m4a" {
				t.Errorf("quality = %q", r.Form.Get("quality"))
			}
			_, _ = w.Write([]byte(`{"status":"success","dlink":"https://mymp3.xyz/phmp4?fname=Rodecia-Kangen%20Band.m4a"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewWithConfig(Config{AaplBaseURL: srv.URL, AplmateBaseURL: srv.URL + "/missing-aplmate"})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testAppleURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !sawSWD {
		t.Fatal("expected swd.php call")
	}
	if result.Platform != downloader.PlatformApple {
		t.Errorf("Platform = %q", result.Platform)
	}
	if result.Type != downloader.MediaAudio {
		t.Errorf("Type = %q", result.Type)
	}
	if result.Title != "Kangen Band — Rodecia - Single" {
		t.Errorf("Title = %q", result.Title)
	}
	if result.DurationMs != 251000 {
		t.Errorf("DurationMs = %d, want 251000", result.DurationMs)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d", len(result.Formats))
	}
	if result.Formats[0].URL != "https://mymp3.xyz/phmp4?fname=Rodecia-Kangen%20Band.m4a" {
		t.Errorf("format url = %q", result.Formats[0].URL)
	}
	if result.Formats[0].Ext != "m4a" {
		t.Errorf("ext = %q", result.Formats[0].Ext)
	}
	if result.Metadata["source"] != "aapl" {
		t.Errorf("source = %q", result.Metadata["source"])
	}
}

func TestResolveAaplForbiddenFallsBackToAplmate(t *testing.T) {
	aapl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		_, _ = w.Write([]byte(`{"error":"403 Forbidden"}`))
	}))
	defer aapl.Close()

	payload, _ := json.Marshal(map[string]string{
		"name":   "Rodecia",
		"artist": "Kangen Band",
		"album":  "Rodecia - Single",
		"cover":  "https://th.example/t.jpg",
		"surl":   "https://music.apple.com/x",
	})
	html := `<form name="submitapurl"><input name="data" value="` + base64.StdEncoding.EncodeToString(payload) + `"><input name="base" value="https://music.apple.com/x"><input name="token" value="trk"></form>`
	actionBody, _ := json.Marshal(map[string]any{"success": true, "html": html})

	aplmate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte("ok"))
		case "/action/userverify":
			_, _ = w.Write([]byte(`{"success":true,"token":"tok123"}`))
		case "/action":
			_, _ = w.Write(actionBody)
		case "/action/track":
			_, _ = w.Write([]byte(`{"data":"<a href=\"https://cdndl.aplmate.com/mp3?token=abc\">mp3</a>"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer aplmate.Close()

	p := NewWithConfig(Config{AaplBaseURL: aapl.URL, AplmateBaseURL: aplmate.URL})
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testAppleURL})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(result.Formats) != 1 {
		t.Fatalf("len(Formats) = %d want 1 body title=%q", len(result.Formats), result.Title)
	}
	if !strings.Contains(result.Formats[0].URL, "cdndl.aplmate.com") {
		t.Errorf("format url = %q", result.Formats[0].URL)
	}
}

func TestResolveBothFailMediaNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		if strings.Contains(r.URL.Path, "pl.php") {
			_, _ = w.Write([]byte(`{"album_details":{"album":"x","artist":"y","count":0}}`))
			return
		}
		if r.URL.Path == "/action/userverify" {
			_, _ = w.Write([]byte(`{"success":true,"token":"t"}`))
			return
		}
		if r.URL.Path == "/action" {
			_, _ = w.Write([]byte(`{"success":true,"html":"<div>no tracks</div>"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := NewWithConfig(Config{AaplBaseURL: srv.URL, AplmateBaseURL: srv.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testAppleURL})
	if !stderrors.Is(err, downloader.ErrMediaNotFound) && !stderrors.Is(err, downloader.ErrProviderInvalidResponse) {
		t.Fatalf("error = %v, want media not found or invalid", err)
	}
}

func TestResolveUpstreamUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := NewWithConfig(Config{AaplBaseURL: srv.URL, AplmateBaseURL: srv.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testAppleURL})
	if !stderrors.Is(err, downloader.ErrProviderUnavailable) {
		t.Fatalf("error = %v, want ErrProviderUnavailable", err)
	}
}

func TestResolveTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{
		AaplBaseURL:    srv.URL,
		AplmateBaseURL: srv.URL,
		Timeout:        20 * time.Millisecond,
	})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testAppleURL})
	if !stderrors.Is(err, downloader.ErrProviderTimeout) && !stderrors.Is(err, downloader.ErrProviderUnavailable) {
		t.Fatalf("error = %v, want timeout/unavailable", err)
	}
}

func TestParseDurationMs(t *testing.T) {
	cases := map[string]int64{
		"4m 11s":   251000,
		"1h 2m 3s": 3723000,
		"3:21":     201000,
		"90":       90000,
		"":         0,
	}
	for in, want := range cases {
		if got := parseDurationMs(in); got != want {
			t.Errorf("parseDurationMs(%q) = %d, want %d", in, got, want)
		}
	}
}
