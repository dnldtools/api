package http

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rest-api/internal/plans"
	"rest-api/internal/ratelimit"
)

func TestRateLimitMiddlewareAllows(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	limiter := &fakeLimiter{result: ratelimit.Result{Allowed: true}}
	called := false

	h := chain(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}),
		authMiddleware(&fakeAuthenticator{}, errHandler),
		rateLimitMiddleware(limiter, plans.Defaults(), errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !called {
		t.Error("handler should have been called")
	}
}

func TestRateLimitMiddlewareDenies(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	limiter := &fakeLimiter{result: ratelimit.Result{Allowed: false, RetryAfter: 30 * time.Second}}

	h := chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("handler should not run when rate limited")
		}),
		authMiddleware(&fakeAuthenticator{}, errHandler),
		rateLimitMiddleware(limiter, plans.Defaults(), errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "RATE_LIMIT_EXCEEDED" {
		t.Errorf("error.code = %q, want RATE_LIMIT_EXCEEDED", got)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After = %q, want a positive value", ra)
	}
}

func TestRateLimitMiddlewareErrorFailsOpen(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	limiter := &fakeLimiter{err: ratelimit.ErrUnavailable}
	called := false

	h := chain(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}),
		authMiddleware(&fakeAuthenticator{}, errHandler),
		rateLimitMiddleware(limiter, plans.Defaults(), errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (fail-open)", rec.Code, http.StatusOK)
	}
	if !called {
		t.Error("handler should have been called when the limiter fails")
	}
}
