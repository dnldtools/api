package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"rest-api/internal/plans"
	"rest-api/internal/quota"
)

func TestQuotaMiddlewareDenies(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	q := &fakeQuota{checkFn: func(context.Context, int64, plans.Plan) (quota.Result, error) {
		return quota.Result{Allowed: false, DailyRemaining: 0}, nil
	}}

	h := chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("handler should not run when quota is exceeded")
		}),
		authMiddleware(&fakeAuthenticator{}, errHandler),
		quotaMiddleware(q, errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "QUOTA_EXCEEDED" {
		t.Errorf("error.code = %q, want QUOTA_EXCEEDED", got)
	}
}

func TestQuotaMiddlewareRecordsAfterHandler(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	q := &fakeQuota{}

	h := chain(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		authMiddleware(&fakeAuthenticator{}, errHandler),
		quotaMiddleware(q, errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	recorded := q.snapshot()
	if len(recorded) != 1 {
		t.Fatalf("recorded %d quota calls, want 1", len(recorded))
	}
	if recorded[0].accountID != testIdentity.AccountID {
		t.Errorf("account ID = %d, want %d", recorded[0].accountID, testIdentity.AccountID)
	}
	if recorded[0].plan != testIdentity.Plan {
		t.Errorf("plan = %q, want %q", recorded[0].plan, testIdentity.Plan)
	}
	if recorded[0].statusCode != http.StatusOK {
		t.Errorf("status code = %d, want %d", recorded[0].statusCode, http.StatusOK)
	}
}

func TestQuotaMiddlewareCheckErrorIsInternal(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	q := &fakeQuota{checkFn: func(context.Context, int64, plans.Plan) (quota.Result, error) {
		return quota.Result{}, errors.New("db down")
	}}

	h := chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("handler should not run when the quota check errors")
		}),
		authMiddleware(&fakeAuthenticator{}, errHandler),
		quotaMiddleware(q, errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "INTERNAL_ERROR" {
		t.Errorf("error.code = %q, want INTERNAL_ERROR", got)
	}
}
