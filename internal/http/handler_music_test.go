package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "rest-api/internal/errors"
	"rest-api/internal/music"
	"rest-api/internal/ratelimit"
)

func postMusic(t *testing.T, h http.Handler, path, apiKey, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func musicRouter(t *testing.T, withMusic bool) http.Handler {
	t.Helper()
	deps := Dependencies{
		Logger:      slog.Default(),
		Downloader:  downloaderRegistry(t),
		Auth:        &fakeAuthenticator{},
		RateLimiter: ratelimit.NewMemoryLimiter(),
		Quota:       &fakeQuota{},
	}
	if withMusic {
		svc, err := music.New(music.Config{Root: "C:\\nonexistent-music-root"}, nil, nil, nil)
		if err != nil {
			t.Fatalf("music.New: %v", err)
		}
		deps.Music = svc
	}
	return NewRouter(deps)
}

func TestMusicRoutesNotRegisteredWithoutService(t *testing.T) {
	router := musicRouter(t, false)
	for _, path := range []string{"/v1/music/info", "/v1/music/download"} {
		rec := postMusic(t, router, path, testAPIKey, `{"url":"https://soundcloud.com/a/b"}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s without music service = %d, want 404", path, rec.Code)
		}
	}
}

func TestMusicRoutesRegisteredWithService(t *testing.T) {
	router := musicRouter(t, true)
	cases := []struct {
		path string
		body string
		want int
	}{
		{"/v1/music/info", `{"url":"https://example.com/x"}`, http.StatusBadRequest},
		{"/v1/music/download", `{"url":"https://soundcloud.com/a/b"}`, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		rec := postMusic(t, router, tc.path, testAPIKey, tc.body)
		if rec.Code != tc.want {
			t.Errorf("POST %s with music service = %d, want %d", tc.path, rec.Code, tc.want)
		}
	}
}

func TestMusicResolveValidation(t *testing.T) {
	router := musicRouter(t, true)

	rec := postMusic(t, router, "/v1/music/info", testAPIKey, `{"url":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing url = %d, want 400", rec.Code)
	}

	rec = postMusic(t, router, "/v1/music/info", testAPIKey, `not-json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json = %d, want 400", rec.Code)
	}

	rec = postMusic(t, router, "/v1/music/info", testAPIKey, `{"url":"https://example.com/x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unsupported platform = %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "UNSUPPORTED_PLATFORM" {
		t.Errorf("code = %q, want UNSUPPORTED_PLATFORM", env.Error.Code)
	}
}

func TestMusicDownloadRequiresR2(t *testing.T) {
	router := musicRouter(t, true)

	rec := postMusic(t, router, "/v1/music/download", testAPIKey, `{"url":"https://soundcloud.com/a/b"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("r2 missing = %d, want 503 body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "PROVIDER_UNAVAILABLE" {
		t.Errorf("code = %q, want PROVIDER_UNAVAILABLE", env.Error.Code)
	}
}

func TestMapErrorMusic(t *testing.T) {
	cases := []struct {
		err  error
		code string
		http int
	}{
		{music.ErrUnsupportedPlatform, "UNSUPPORTED_PLATFORM", http.StatusBadRequest},
		{music.ErrR2Required, "PROVIDER_UNAVAILABLE", http.StatusServiceUnavailable},
		{music.ErrDownloadFailed, "MEDIA_NOT_FOUND", http.StatusNotFound},
	}
	for _, tc := range cases {
		appErr := mapError(tc.err)
		if appErr == nil {
			t.Errorf("mapError(%v) = nil", tc.err)
			continue
		}
		if appErr.Status != tc.http {
			t.Errorf("mapError(%v).Status = %d, want %d", tc.err, appErr.Status, tc.http)
		}
		if appErr.Code != apperrors.Code(tc.code) {
			t.Errorf("mapError(%v).Code = %q, want %q", tc.err, appErr.Code, tc.code)
		}
	}
}
