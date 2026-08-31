package http

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidRequestID(t *testing.T) {
	valid := []string{
		"abc123",
		"ABC-def_ghi.jkl",
		"req-4f2a9c1e8b3d4e5f",
		"1",
	}
	for _, id := range valid {
		if !validRequestID(id) {
			t.Errorf("validRequestID(%q) = false, want true", id)
		}
	}

	invalid := []string{
		"",
		"has space",
		"has\nnewline",
		"has\r\nheader-injection",
		"with/slash",
		"with;colon",
		strings.Repeat("a", maxRequestIDLength+1),
		"tick`tick",
	}
	for _, id := range invalid {
		if validRequestID(id) {
			t.Errorf("validRequestID(%q) = true, want false", id)
		}
	}
}

func TestRequestIDMiddlewareGeneratesWhenMissing(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestIDFromContext(r.Context())
		if id == "" {
			t.Error("request ID should be present in context")
		}
		if !validRequestID(id) {
			t.Errorf("generated request ID %q is not valid", id)
		}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	requestIDMiddleware(next).ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got == "" {
		t.Error("X-Request-ID response header should be set")
	}
}

func TestRequestIDMiddlewareReusesValidHeader(t *testing.T) {
	const want = "client-supplied-123"

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := requestIDFromContext(r.Context()); got != want {
			t.Errorf("context request ID = %q, want %q", got, want)
		}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-ID", want)
	requestIDMiddleware(next).ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != want {
		t.Errorf("X-Request-ID header = %q, want %q", got, want)
	}
}

func TestRequestIDMiddlewareRejectsInvalidHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestIDFromContext(r.Context())
		if id == "" || id == "injected\nSet-Cookie: evil=1" {
			t.Errorf("invalid header should have been replaced, got %q", id)
		}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-ID", "injected\nSet-Cookie: evil=1")
	requestIDMiddleware(next).ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got == "injected\nSet-Cookie: evil=1" {
		t.Error("invalid X-Request-ID header should be replaced in the response")
	}
}

func TestContentTypeMiddlewareSetsJSON(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	contentTypeMiddleware(next).ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json; charset=utf-8")
	}
}

func TestLoggingMiddlewarePropagatesStartTime(t *testing.T) {
	var gotStart bool

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, ok := startTimeFromContext(r.Context())
		if !ok || start.IsZero() {
			t.Error("start time should be present in context")
		}
		gotStart = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	loggingMiddleware(slog.Default())(next).ServeHTTP(rec, req)

	if !gotStart {
		t.Error("handler was not invoked")
	}
}
