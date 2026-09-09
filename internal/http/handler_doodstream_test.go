package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/doodstream"
)

func TestDownloadDoodstreamThroughHandler(t *testing.T) {
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/d/"):
			_, _ = w.Write([]byte(`<html><head><title>Clip - DoodStream</title><meta property="og:title" content="Clip"></head></html>`))
		case strings.HasPrefix(r.URL.Path, "/e/"):
			_, _ = w.Write([]byte(`<script>("/pass_md5/tokentoken")</script>`))
		case strings.HasPrefix(r.URL.Path, "/pass_md5/"):
			_, _ = w.Write([]byte(base + "/v.mp4"))
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base = srv.URL

	p := doodstream.NewWithConfig(doodstream.Config{HTTPClient: srv.Client(), ChromeBin: "/no-such-chrome"})
	// Allow httptest host via explicit platform — Resolve still validates host.
	// Register a wrapper? Use playmogo URL and custom Transport that rewrites host.
	rt := rewriteHostRoundTripper{host: strings.TrimPrefix(srv.URL, "http://"), base: srv.Client().Transport}
	client := &http.Client{Transport: rt}
	p = doodstream.NewWithConfig(doodstream.Config{HTTPClient: client, ChromeBin: "/no-such-chrome"})

	reg := downloader.NewRegistry()
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	router := newRouterWithService(t, downloader.NewService(reg))
	rec := postDownload(t, router, testAPIKey, `{"platform":"doodstream","url":"https://playmogo.com/d/r9pfpgvacdpr"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Platform string `json:"platform"`
			Formats  []struct {
				URL string `json:"url"`
			} `json:"formats"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if !env.Success || env.Data.Platform != "doodstream" || len(env.Data.Formats) != 1 {
		t.Fatalf("%s", rec.Body.String())
	}
}

type rewriteHostRoundTripper struct {
	host string
	base http.RoundTripper
}

func (r rewriteHostRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = r.host
	clone.Host = r.host
	rt := r.base
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(clone)
}
