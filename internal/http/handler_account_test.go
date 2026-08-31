package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rest-api/internal/auth"
	"rest-api/internal/downloader"
	"rest-api/internal/plans"
	"rest-api/internal/quota"
	"rest-api/internal/ratelimit"
)

func newAccountRouter(t *testing.T, keys auth.KeyManager, quotaSvc quota.Service) http.Handler {
	t.Helper()

	registry := downloader.NewRegistry()
	svc := downloader.NewService(registry)

	if keys == nil {
		keys = &fakeKeyManager{}
	}
	if quotaSvc == nil {
		quotaSvc = &fakeQuota{}
	}

	return NewRouter(Dependencies{
		PrettyJSON:  false,
		Logger:      slog.Default(),
		Downloader:  svc,
		Auth:        &fakeAuthenticator{},
		Keys:        keys,
		RateLimiter: ratelimit.NewMemoryLimiter(),
		Quota:       quotaSvc,
		Plans:       plans.Defaults(),
	})
}

func doRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder, dst interface{}) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(dst); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

func TestAccountEndpointRequiresAuth(t *testing.T) {
	h := newAccountRouter(t, nil, nil)
	rec := doRequest(t, h, http.MethodGet, "/v1/account", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAccountEndpointReturnsIdentity(t *testing.T) {
	h := newAccountRouter(t, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Success bool `json:"success"`
		Data    struct {
			AccountID int64  `json:"account_id"`
			Name      string `json:"name"`
			Role      string `json:"role"`
			Plan      string `json:"plan"`
			APIKeyID  int64  `json:"api_key_id"`
		} `json:"data"`
	}
	decodeEnvelope(t, rec, &body)
	if !body.Success {
		t.Error("success = false, want true")
	}
	if body.Data.AccountID != 42 {
		t.Errorf("account_id = %d, want 42", body.Data.AccountID)
	}
	if body.Data.Plan != "pro" {
		t.Errorf("plan = %q, want pro", body.Data.Plan)
	}
}

func TestUsageEndpointReturnsQuotaAndPolicy(t *testing.T) {
	h := newAccountRouter(t, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Data struct {
			Plan              string `json:"plan"`
			RateLimit         int    `json:"rate_limit"`
			RateWindowSeconds int64  `json:"rate_window_seconds"`
			Daily             struct {
				Quota     int64 `json:"quota"`
				Remaining int64 `json:"remaining"`
			} `json:"daily"`
		} `json:"data"`
	}
	decodeEnvelope(t, rec, &body)

	if body.Data.Plan != "pro" {
		t.Errorf("plan = %q, want pro", body.Data.Plan)
	}
	if body.Data.RateLimit != 600 {
		t.Errorf("rate_limit = %d, want 600", body.Data.RateLimit)
	}
	if body.Data.RateWindowSeconds != 60 {
		t.Errorf("rate_window_seconds = %d, want 60", body.Data.RateWindowSeconds)
	}
	if body.Data.Daily.Quota != -1 {
		t.Errorf("daily quota = %d, want -1 (fakeQuota)", body.Data.Daily.Quota)
	}
}

func TestListKeysEndpointReturnsSummaries(t *testing.T) {
	now := time.Now().UTC()
	keys := &fakeKeyManager{keys: []auth.APIKey{
		{ID: 1, Name: "a", KeyHash: "0123456789abcdef", Status: auth.KeyStatusActive, CreatedAt: now},
		{ID: 2, Name: "b", KeyHash: "fedcba9876543210", Status: auth.KeyStatusRevoked, CreatedAt: now},
	}}
	h := newAccountRouter(t, keys, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/keys", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Data struct {
			Keys []struct {
				ID          int64  `json:"id"`
				Status      string `json:"status"`
				Fingerprint string `json:"fingerprint"`
			} `json:"keys"`
		} `json:"data"`
	}
	decodeEnvelope(t, rec, &body)

	if len(body.Data.Keys) != 2 {
		t.Fatalf("keys = %d, want 2", len(body.Data.Keys))
	}
	if body.Data.Keys[0].Fingerprint != "0123456789ab" {
		t.Errorf("fingerprint = %q, want 0123456789ab", body.Data.Keys[0].Fingerprint)
	}
	if body.Data.Keys[1].Status != "revoked" {
		t.Errorf("status = %q, want revoked", body.Data.Keys[1].Status)
	}
}

func TestCreateKeyEndpointReturnsRawKey(t *testing.T) {
	h := newAccountRouter(t, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/keys", strings.NewReader(`{"name":"my-app"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	var body struct {
		Data struct {
			ID     int64  `json:"id"`
			Name   string `json:"name"`
			APIKey string `json:"api_key"`
			Status string `json:"status"`
		} `json:"data"`
	}
	decodeEnvelope(t, rec, &body)

	if body.Data.Name != "my-app" {
		t.Errorf("name = %q, want my-app", body.Data.Name)
	}
	if !strings.HasPrefix(body.Data.APIKey, "ra_") {
		t.Errorf("api_key %q must start with ra_", body.Data.APIKey)
	}
	if body.Data.Status != "active" {
		t.Errorf("status = %q, want active", body.Data.Status)
	}
}

func TestCreateKeyEndpointValidation(t *testing.T) {
	h := newAccountRouter(t, nil, nil)

	for name, tc := range map[string]struct {
		body string
		code string
	}{
		"missing name":  {`{"expires_in":"720h"}`, "MISSING_PARAMETER"},
		"invalid json":  {`{`, "INVALID_JSON"},
		"bad expires":   {`{"name":"x","expires_in":"nope"}`, "INVALID_REQUEST"},
		"unknown field": {`{"name":"x","extra":1}`, "INVALID_JSON"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/keys", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", testAPIKey)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
			continue
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		decodeEnvelope(t, rec, &body)
		if body.Error.Code != tc.code {
			t.Errorf("%s: code = %q, want %q", name, body.Error.Code, tc.code)
		}
	}
}

func TestRevokeKeyEndpoint(t *testing.T) {
	keys := &fakeKeyManager{}
	h := newAccountRouter(t, keys, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/keys/7/revoke", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(keys.revokedIDs) != 1 || keys.revokedIDs[0] != 7 {
		t.Errorf("revoked ids = %v, want [7]", keys.revokedIDs)
	}
}

func TestRevokeKeyEndpointNotFound(t *testing.T) {
	keys := &fakeKeyManager{revokeErr: auth.ErrKeyNotFound}
	h := newAccountRouter(t, keys, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/keys/7/revoke", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRevokeKeyEndpointInvalidID(t *testing.T) {
	h := newAccountRouter(t, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/keys/abc/revoke", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
