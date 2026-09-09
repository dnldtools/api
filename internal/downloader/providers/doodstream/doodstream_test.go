package doodstream

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rest-api/internal/downloader"
)

func httptestHost(raw string) string {
	u := strings.TrimPrefix(strings.TrimPrefix(raw, "http://"), "https://")
	if i := strings.IndexByte(u, ':'); i >= 0 {
		return u[:i]
	}
	if i := strings.IndexByte(u, '/'); i >= 0 {
		return u[:i]
	}
	return u
}

func TestProviderContract(t *testing.T) {
	p := New()
	if p.Name() != "doodstream" || p.Platform() != downloader.PlatformDoodstream {
		t.Fatalf("contract %s %s", p.Name(), p.Platform())
	}
}

func TestMatchesURL(t *testing.T) {
	p := New()
	if !p.MatchesURL("https://playmogo.com/d/r9pfpgvacdpr") {
		t.Error("playmogo")
	}
	if !p.MatchesURL("https://dood.la/e/abc123") {
		t.Error("dood.la")
	}
	if p.MatchesURL("r9pfpgvacdpr") {
		t.Error("bare code must not auto-claim")
	}
	if p.MatchesURL("https://uc-share.com/s/x") {
		t.Error("uc-share")
	}
}

func TestResolveEmptyURL(t *testing.T) {
	_, err := New().Resolve(context.Background(), downloader.DownloadRequest{})
	if !stderrors.Is(err, downloader.ErrInvalidURL) {
		t.Fatalf("%v", err)
	}
}

func TestResolveFromFixtureHost(t *testing.T) {
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/d/"):
			_, _ = w.Write([]byte(`<html><head><title>Cool Clip - DoodStream</title><meta property="og:title" content="Cool Clip"><meta property="og:image" content="https://th.example/t.jpg"></head><body>12:34</body></html>`))
		case strings.HasPrefix(r.URL.Path, "/e/"):
			_, _ = w.Write([]byte(`<html><script>x("/pass_md5/aabbccddee")</script></html>`))
		case strings.HasPrefix(r.URL.Path, "/pass_md5/"):
			_, _ = w.Write([]byte(base + "/cdn/video.mp4"))
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base = srv.URL

	host := httptestHost(srv.URL)
	doodHosts[host] = struct{}{}
	t.Cleanup(func() { delete(doodHosts, host) })

	p := NewWithConfig(Config{HTTPClient: srv.Client(), ChromeBin: "/no-such-chrome"})
	res, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: srv.URL + "/d/r9pfpgvacdpr"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Title != "Cool Clip" {
		t.Errorf("title %q", res.Title)
	}
	if len(res.Formats) != 1 {
		t.Fatalf("formats %d", len(res.Formats))
	}
	if !strings.Contains(res.Formats[0].URL, "/cdn/video.mp4") {
		t.Errorf("url %q", res.Formats[0].URL)
	}
	if !strings.Contains(res.Formats[0].URL, "token=aabbccddee") {
		t.Errorf("missing token in %q", res.Formats[0].URL)
	}
	if res.Metadata["file_code"] != "r9pfpgvacdpr" {
		t.Errorf("file_code %q", res.Metadata["file_code"])
	}
}

func TestResolveCloudflareUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<title>Just a moment...</title>`))
	}))
	defer srv.Close()
	host := httptestHost(srv.URL)
	doodHosts[host] = struct{}{}
	t.Cleanup(func() { delete(doodHosts, host) })

	p := NewWithConfig(Config{HTTPClient: srv.Client(), ChromeBin: "/no-such-chrome"})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: srv.URL + "/d/abc"})
	if !stderrors.Is(err, downloader.ErrProviderUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
