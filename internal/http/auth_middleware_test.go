package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"rest-api/internal/auth"
	apperrors "rest-api/internal/errors"
)

func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return envelope.Error.Code
}

func TestAuthMiddlewareMissingKey(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	h := chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("handler should not run for a missing key")
		}),
		authMiddleware(&fakeAuthenticator{}, errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "API_KEY_MISSING" {
		t.Errorf("error.code = %q, want API_KEY_MISSING", got)
	}
}

func TestAuthMiddlewareInvalidKey(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	h := chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("handler should not run for an invalid key")
		}),
		authMiddleware(&fakeAuthenticator{}, errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "API_KEY_INVALID" {
		t.Errorf("error.code = %q, want API_KEY_INVALID", got)
	}
}

func TestAuthMiddlewareExpiredKey(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	fake := &fakeAuthenticator{fn: func(context.Context, string) (*auth.Identity, error) {
		return nil, apperrors.APIKeyExpired("api key expired")
	}}
	h := chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("handler should not run for an expired key")
		}),
		authMiddleware(fake, errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", "expired-key")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "API_KEY_EXPIRED" {
		t.Errorf("error.code = %q, want API_KEY_EXPIRED", got)
	}
}

func TestAuthMiddlewareRevokedKey(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	fake := &fakeAuthenticator{fn: func(context.Context, string) (*auth.Identity, error) {
		return nil, apperrors.APIKeyRevoked("api key revoked")
	}}
	h := chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("handler should not run for a revoked key")
		}),
		authMiddleware(fake, errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", "revoked-key")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "API_KEY_REVOKED" {
		t.Errorf("error.code = %q, want API_KEY_REVOKED", got)
	}
}

func TestAuthMiddlewareSuspendedAccount(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	fake := &fakeAuthenticator{fn: func(context.Context, string) (*auth.Identity, error) {
		return nil, apperrors.AccountSuspended("account suspended")
	}}
	h := chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("handler should not run for a suspended account")
		}),
		authMiddleware(fake, errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", "suspended-key")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "ACCOUNT_SUSPENDED" {
		t.Errorf("error.code = %q, want ACCOUNT_SUSPENDED", got)
	}
}

func TestAuthMiddlewareDisabledAccount(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	fake := &fakeAuthenticator{fn: func(context.Context, string) (*auth.Identity, error) {
		return nil, apperrors.AccountDisabled("account disabled")
	}}
	h := chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("handler should not run for a disabled account")
		}),
		authMiddleware(fake, errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", "disabled-key")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "ACCOUNT_DISABLED" {
		t.Errorf("error.code = %q, want ACCOUNT_DISABLED", got)
	}
}

func TestAuthMiddlewareValidKeySetsIdentity(t *testing.T) {
	errHandler := NewErrorHandler(slog.Default())
	var got *auth.Identity
	h := chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := identityFromContext(r.Context())
			if !ok {
				t.Error("identity should be present in context")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			got = id
			w.WriteHeader(http.StatusOK)
		}),
		authMiddleware(&fakeAuthenticator{}, errHandler),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got == nil {
		t.Fatal("identity was not captured")
	}
	if got.AccountID != testIdentity.AccountID {
		t.Errorf("account ID = %d, want %d", got.AccountID, testIdentity.AccountID)
	}
	if got.Plan != testIdentity.Plan {
		t.Errorf("plan = %q, want %q", got.Plan, testIdentity.Plan)
	}
	if got.KeyHash != auth.HashKey(testAPIKey) {
		t.Errorf("key hash = %q, want hash of test key", got.KeyHash)
	}
}
