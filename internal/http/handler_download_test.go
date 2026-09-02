package http

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers"
	"rest-api/internal/ratelimit"
)

func newRouterWithService(t *testing.T, svc *downloader.Service) http.Handler {
	t.Helper()
	return newRouterWithServiceAndBase(t, svc, "")
}

func newRouterWithServiceAndBase(t *testing.T, svc *downloader.Service, base string) http.Handler {
	t.Helper()
	return NewRouter(Dependencies{
		PrettyJSON:    false,
		Logger:        slog.Default(),
		Downloader:    svc,
		PublicBaseURL: base,
		Auth:          &fakeAuthenticator{},
		RateLimiter:   ratelimit.NewMemoryLimiter(),
		Quota:         &fakeQuota{},
	})
}

type stubProvider struct {
	platform downloader.Platform
	resolve  func(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error)
	matches  func(url string) bool
}

func (p *stubProvider) Name() string                  { return string(p.platform) }
func (p *stubProvider) Platform() downloader.Platform { return p.platform }
func (p *stubProvider) Type() downloader.ProviderType { return downloader.ProviderNative }
func (p *stubProvider) Resolve(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	return p.resolve(ctx, req)
}

func (p *stubProvider) MatchesURL(url string) bool {
	if p.matches == nil {
		return false
	}
	return p.matches(url)
}

func downloaderRegistry(t *testing.T) *downloader.Service {
	t.Helper()
	registry := downloader.NewRegistry()
	if err := providers.RegisterAll(registry); err != nil {
		t.Fatalf("register providers: %v", err)
	}
	return downloader.NewService(registry)
}

func postDownload(t *testing.T, h http.Handler, apiKey, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDownloadValidKeyReturnsNotImplemented(t *testing.T) {
	registry := downloader.NewRegistry()
	if err := registry.Register(&stubProvider{
		platform: "not-implemented",
		resolve: func(context.Context, downloader.DownloadRequest) (*downloader.DownloadResult, error) {
			return nil, downloader.ErrNotImplemented
		},
	}); err != nil {
		t.Fatalf("register stub provider: %v", err)
	}
	router := newRouterWithService(t, downloader.NewService(registry))

	rec := postDownload(t, router, testAPIKey, `{"platform":"not-implemented","url":"https://x"}`)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "NOT_IMPLEMENTED" {
		t.Errorf("error.code = %q, want NOT_IMPLEMENTED", got)
	}
}

func TestDownloadInvalidJSON(t *testing.T) {
	router := newRouterWithService(t, downloaderRegistry(t))

	rec := postDownload(t, router, testAPIKey, `{"platform":`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "INVALID_JSON" {
		t.Errorf("error.code = %q, want INVALID_JSON", got)
	}
}

func TestDownloadValidationErrors(t *testing.T) {
	router := newRouterWithService(t, downloaderRegistry(t))

	cases := []struct {
		name string
		body string
		code string
	}{

		{"missing url", `{"platform":"facebook"}`, "INVALID_URL"},
		{"unsupported platform", `{"platform":"myspace","url":"https://x"}`, "UNSUPPORTED_PLATFORM"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postDownload(t, router, testAPIKey, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			if got := errCode(t, rec.Body.Bytes()); got != tc.code {
				t.Errorf("error.code = %q, want %q", got, tc.code)
			}
		})
	}
}

func TestDownloadContextCanceledMapsToInternal(t *testing.T) {
	registry := downloader.NewRegistry()
	if err := registry.Register(&stubProvider{
		platform: "cancel-test",
		resolve: func(ctx context.Context, _ downloader.DownloadRequest) (*downloader.DownloadResult, error) {

			ctx, cancel := context.WithCancel(ctx)
			cancel()
			return nil, ctx.Err()
		},
	}); err != nil {
		t.Fatalf("register stub provider: %v", err)
	}
	router := newRouterWithService(t, downloader.NewService(registry))

	rec := postDownload(t, router, testAPIKey, `{"platform":"cancel-test","url":"https://x"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "INTERNAL_ERROR" {
		t.Errorf("error.code = %q, want INTERNAL_ERROR", got)
	}
}

func TestDownloadDeadlineExceededMapsToTimeout(t *testing.T) {
	registry := downloader.NewRegistry()
	if err := registry.Register(&stubProvider{
		platform: "timeout-test",
		resolve: func(ctx context.Context, _ downloader.DownloadRequest) (*downloader.DownloadResult, error) {
			ctx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer cancel()
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}); err != nil {
		t.Fatalf("register stub provider: %v", err)
	}
	router := newRouterWithService(t, downloader.NewService(registry))

	rec := postDownload(t, router, testAPIKey, `{"platform":"timeout-test","url":"https://x"}`)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "TIMEOUT" {
		t.Errorf("error.code = %q, want TIMEOUT", got)
	}

	var envelope struct {
		Error struct {
			Retryable bool `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !envelope.Error.Retryable {
		t.Error("timeout error should be retryable")
	}
}

func TestDownloadAutoDetectsPlatform(t *testing.T) {
	registry := downloader.NewRegistry()
	if err := registry.Register(&stubProvider{
		platform: "detected-platform",
		matches:  func(url string) bool { return strings.HasPrefix(url, "https://detected.example/") },
		resolve: func(_ context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
			return &downloader.DownloadResult{
				Platform: req.Platform,
				URL:      req.URL,
				Title:    "Detected",
			}, nil
		},
	}); err != nil {
		t.Fatalf("register stub provider: %v", err)
	}
	router := newRouterWithService(t, downloader.NewService(registry))

	rec := postDownload(t, router, testAPIKey, `{"url":"https://detected.example/video/1"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Platform string `json:"platform"`
			Title    string `json:"title"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if envelope.Data.Platform != "detected-platform" {
		t.Errorf("data.platform = %q, want detected-platform", envelope.Data.Platform)
	}
	if envelope.Data.Title != "Detected" {
		t.Errorf("data.title = %q, want Detected", envelope.Data.Title)
	}
}

func TestDownloadInternalErrorDoesNotLeak(t *testing.T) {
	const secret = "db-password=hunter2"

	registry := downloader.NewRegistry()
	if err := registry.Register(&stubProvider{
		platform: "leak-test",
		resolve: func(context.Context, downloader.DownloadRequest) (*downloader.DownloadResult, error) {
			return nil, stderrors.New(secret)
		},
	}); err != nil {
		t.Fatalf("register stub provider: %v", err)
	}
	router := newRouterWithService(t, downloader.NewService(registry))

	rec := postDownload(t, router, testAPIKey, `{"platform":"leak-test","url":"https://x"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("response leaked internal error detail: %s", rec.Body.String())
	}
	if got := errCode(t, rec.Body.Bytes()); got != "INTERNAL_ERROR" {
		t.Errorf("error.code = %q, want INTERNAL_ERROR", got)
	}
}
