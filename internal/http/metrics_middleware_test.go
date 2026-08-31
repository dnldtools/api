package http

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"rest-api/internal/auth"
	"rest-api/internal/metrics"
)

type fakeMetricsRepo struct {
	mu     sync.Mutex
	events []metrics.Event
}

func (f *fakeMetricsRepo) Insert(_ context.Context, e metrics.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

func (f *fakeMetricsRepo) Aggregate(context.Context, time.Time, time.Time) (metrics.Stats, error) {
	return metrics.Stats{}, nil
}

func (f *fakeMetricsRepo) snapshot() []metrics.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]metrics.Event, len(f.events))
	copy(out, f.events)
	return out
}

func TestMetricsMiddlewareRecordsEvent(t *testing.T) {
	repo := &fakeMetricsRepo{}
	svc := metrics.New(repo, nil, slog.Default(), 8)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setPlatform(r.Context(), "facebook")
		w.WriteHeader(http.StatusOK)
	})

	errHandler := NewErrorHandler(slog.Default())
	h := chain(
		handler,
		requestIDMiddleware,
		metricsMiddleware(svc),
		authMiddleware(&fakeAuthenticator{}, errHandler),
		contentTypeMiddleware,
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	h.ServeHTTP(rec, req)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	svc.Close(ctx)

	events := repo.snapshot()
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(events))
	}

	e := events[0]
	if e.StatusCode != http.StatusOK || !e.Success || e.Failed {
		t.Errorf("status/success/failed = %d/%v/%v, want 200/true/false", e.StatusCode, e.Success, e.Failed)
	}
	if e.Endpoint != "/v1/downloads" {
		t.Errorf("endpoint = %q, want /v1/downloads", e.Endpoint)
	}
	if e.Platform != "facebook" {
		t.Errorf("platform = %q, want facebook", e.Platform)
	}
	if !e.ValidAPIKey || e.InvalidAPIKey || e.APIKeyMissing {
		t.Errorf("valid/invalid/missing api key = %v/%v/%v, want true/false/false", e.ValidAPIKey, e.InvalidAPIKey, e.APIKeyMissing)
	}
	if e.ClientID != auth.HashKey(testAPIKey) {
		t.Errorf("client ID = %q, want hash of %q", e.ClientID, testAPIKey)
	}
	if e.AccountID != testIdentity.AccountID {
		t.Errorf("account ID = %d, want %d", e.AccountID, testIdentity.AccountID)
	}
	if e.RequestID == "" {
		t.Error("request ID should be recorded")
	}
	if e.Duration < 0 {
		t.Errorf("duration should be non-negative, got %v", e.Duration)
	}
	if e.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
}

func TestMetricsMiddlewareNoopWhenServiceNil(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	h := chain(handler, metricsMiddleware(nil), contentTypeMiddleware)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
}
