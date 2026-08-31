package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rest-api/internal/downloader"
	"rest-api/internal/health"
	"rest-api/internal/ratelimit"
)

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()

	registry := downloader.NewRegistry()
	svc := downloader.NewService(registry)

	return NewRouter(Dependencies{
		PrettyJSON:  false,
		Logger:      slog.Default(),
		Health:      health.New("test"),
		Downloader:  svc,
		Auth:        &fakeAuthenticator{},
		Keys:        &fakeKeyManager{},
		RateLimiter: ratelimit.NewMemoryLimiter(),
		Quota:       &fakeQuota{},
	})
}

func TestRouterInitializesAndServesHealth(t *testing.T) {
	router := newTestRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if rid := rec.Header().Get("X-Request-ID"); rid == "" {
		t.Error("X-Request-ID header should be set")
	}

	var body struct {
		Success   bool   `json:"success"`
		RequestID string `json:"request_id"`
		Data      struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.Success {
		t.Error("success = false, want true")
	}
	if body.Data.Status != "ok" {
		t.Errorf("data.status = %q, want ok", body.Data.Status)
	}
	if body.RequestID == "" {
		t.Error("request_id should be present in the JSON body")
	}
}

func TestRouterUsesVersionedRoutesWithoutAPIPrefix(t *testing.T) {
	router := newTestRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /v1/downloads status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	for _, path := range []string{"/api/v1/downloads"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}
}

func TestRouterUnknownRouteReturnsJSONError(t *testing.T) {
	router := newTestRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Success {
		t.Error("success = true, want false")
	}
	if body.Error.Code != "NOT_FOUND" {
		t.Errorf("error.code = %q, want NOT_FOUND", body.Error.Code)
	}
}

func TestRouterMiddlewareChainRecoversFromPanic(t *testing.T) {

	panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	router := chain(
		panicHandler,
		recoverMiddleware(NewErrorHandler(slog.Default())),
		requestIDMiddleware,
		loggingMiddleware(slog.Default()),
		contentTypeMiddleware,
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if rid := rec.Header().Get("X-Request-ID"); rid == "" {
		t.Error("X-Request-ID header should be set")
	}

	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Success {
		t.Error("success = true, want false")
	}
	if body.Error.Code != "INTERNAL_ERROR" {
		t.Errorf("error.code = %q, want INTERNAL_ERROR", body.Error.Code)
	}

	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("panic detail leaked into the response")
	}
}
