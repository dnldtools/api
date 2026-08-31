package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rest-api/internal/auth"
	"rest-api/internal/downloader"
	"rest-api/internal/ratelimit"
)

func newAdminRouter(t *testing.T, admin auth.AdminManager, authSvc auth.Authenticator) http.Handler {
	t.Helper()

	registry := downloader.NewRegistry()
	svc := downloader.NewService(registry)

	if admin == nil {
		admin = &fakeAdminManager{}
	}
	if authSvc == nil {
		authSvc = &fakeAuthenticator{}
	}

	return NewRouter(Dependencies{
		PrettyJSON:  false,
		Logger:      slog.Default(),
		Downloader:  svc,
		Auth:        authSvc,
		Admin:       admin,
		RateLimiter: ratelimit.NewMemoryLimiter(),
		Quota:       &fakeQuota{},
	})
}

func doAdminRequest(router http.Handler, method, path, apiKey, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	router.ServeHTTP(rec, req)
	return rec
}

func decodeAdminEnvelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return body
}

func adminErrorCode(t *testing.T, body map[string]json.RawMessage) string {
	t.Helper()
	var e struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body["error"], &e); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	return e.Code
}

func TestAdminRoutesRequireAPIKey(t *testing.T) {
	router := newAdminRouter(t, &fakeAdminManager{}, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodGet, "/v1/admin/accounts", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if code := adminErrorCode(t, decodeAdminEnvelope(t, rec)); code != "API_KEY_MISSING" {
		t.Errorf("error.code = %q, want API_KEY_MISSING", code)
	}
}

func TestAdminRoutesRejectNonAdmin(t *testing.T) {
	router := newAdminRouter(t, &fakeAdminManager{}, &fakeAuthenticator{})

	for _, path := range []string{
		"/v1/admin/accounts",
		"/v1/admin/accounts/1",
		"/v1/admin/keys",
		"/v1/admin/keys/1",
		"/v1/admin/stats",
	} {
		rec := doAdminRequest(router, http.MethodGet, path, testAPIKey, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s status = %d, want %d", path, rec.Code, http.StatusForbidden)
			continue
		}
		if code := adminErrorCode(t, decodeAdminEnvelope(t, rec)); code != "FORBIDDEN" {
			t.Errorf("GET %s error.code = %q, want FORBIDDEN", path, code)
		}
	}
}

func TestAdminListAccounts(t *testing.T) {
	admin := &fakeAdminManager{
		accounts: []auth.AccountSummary{
			{Account: auth.Account{ID: 1, Name: "one", Role: auth.RoleUser, Plan: "pro", Status: auth.AccountStatusActive}, KeyCount: 2},
		},
	}
	router := newAdminRouter(t, admin, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodGet, "/v1/admin/accounts", adminTestAPIKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := decodeAdminEnvelope(t, rec)
	var data adminListAccountsResponse
	if err := json.Unmarshal(body["data"], &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if len(data.Accounts) != 1 || data.Accounts[0].ID != 1 || data.Accounts[0].KeyCount != 2 {
		t.Errorf("accounts = %+v, want one account id=1 key_count=2", data.Accounts)
	}
}

func TestAdminGetAccount(t *testing.T) {
	admin := &fakeAdminManager{
		account: &auth.AccountSummary{
			Account:  auth.Account{ID: 7, Name: "seven", Role: auth.RoleAdmin, Plan: "pro", Status: auth.AccountStatusActive},
			KeyCount: 3,
		},
	}
	router := newAdminRouter(t, admin, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodGet, "/v1/admin/accounts/7", adminTestAPIKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := decodeAdminEnvelope(t, rec)
	var data adminAccountSummary
	if err := json.Unmarshal(body["data"], &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.ID != 7 || data.KeyCount != 3 {
		t.Errorf("account = %+v, want id=7 key_count=3", data)
	}
}

func TestAdminGetAccountNotFound(t *testing.T) {
	router := newAdminRouter(t, &fakeAdminManager{}, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodGet, "/v1/admin/accounts/999", adminTestAPIKey, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if code := adminErrorCode(t, decodeAdminEnvelope(t, rec)); code != "RESOURCE_NOT_FOUND" {
		t.Errorf("error.code = %q, want RESOURCE_NOT_FOUND", code)
	}
}

func TestAdminUpdateAccount(t *testing.T) {
	var gotID int64
	var gotChanges auth.AccountChanges
	admin := &fakeAdminManager{
		updateAccountFn: func(id int64, changes auth.AccountChanges) (*auth.Account, error) {
			gotID = id
			gotChanges = changes
			return &auth.Account{ID: id, Name: "updated", Role: auth.RoleAdmin, Plan: "pro", Status: auth.AccountStatusActive}, nil
		},
	}
	router := newAdminRouter(t, admin, &fakeAuthenticator{})

	body := `{"name":"updated","role":"admin","plan":"pro","status":"active"}`
	rec := doAdminRequest(router, http.MethodPatch, "/v1/admin/accounts/5", adminTestAPIKey, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotID != 5 {
		t.Errorf("update called with id %d, want 5", gotID)
	}
	if gotChanges.Name == nil || *gotChanges.Name != "updated" {
		t.Errorf("name not applied: %+v", gotChanges.Name)
	}
	if gotChanges.Role == nil || *gotChanges.Role != auth.RoleAdmin {
		t.Errorf("role not applied: %+v", gotChanges.Role)
	}
}

func TestAdminUpdateAccountRejectsUnknownField(t *testing.T) {
	router := newAdminRouter(t, &fakeAdminManager{}, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodPatch, "/v1/admin/accounts/1", adminTestAPIKey, `{"bogus":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if code := adminErrorCode(t, decodeAdminEnvelope(t, rec)); code != "INVALID_JSON" {
		t.Errorf("error.code = %q, want INVALID_JSON", code)
	}
}

func TestAdminUpdateAccountRejectsInvalidRole(t *testing.T) {
	router := newAdminRouter(t, &fakeAdminManager{}, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodPatch, "/v1/admin/accounts/1", adminTestAPIKey, `{"role":"superuser"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if code := adminErrorCode(t, decodeAdminEnvelope(t, rec)); code != "INVALID_REQUEST" {
		t.Errorf("error.code = %q, want INVALID_REQUEST", code)
	}
}

func TestAdminDisableAccount(t *testing.T) {
	admin := &fakeAdminManager{
		accounts: []auth.AccountSummary{
			{Account: auth.Account{ID: 3, Name: "three", Role: auth.RoleUser, Plan: "free", Status: auth.AccountStatusActive}},
		},
	}
	router := newAdminRouter(t, admin, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodDelete, "/v1/admin/accounts/3", adminTestAPIKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := decodeAdminEnvelope(t, rec)
	var data adminDisabledAccountResponse
	if err := json.Unmarshal(body["data"], &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.ID != 3 || data.Status != "disabled" {
		t.Errorf("data = %+v, want id=3 status=disabled", data)
	}
}

func TestAdminListKeys(t *testing.T) {
	admin := &fakeAdminManager{
		keys: []auth.APIKeySummary{
			{APIKey: auth.APIKey{ID: 9, AccountID: 4, Name: "k9", KeyHash: "abcdef1234567890", Status: auth.KeyStatusActive}, AccountName: "owner"},
		},
	}
	router := newAdminRouter(t, admin, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodGet, "/v1/admin/keys", adminTestAPIKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := decodeAdminEnvelope(t, rec)
	var data adminListKeysResponse
	if err := json.Unmarshal(body["data"], &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if len(data.Keys) != 1 || data.Keys[0].ID != 9 || data.Keys[0].AccountName != "owner" {
		t.Errorf("keys = %+v, want one key id=9 account_name=owner", data.Keys)
	}
	if data.Keys[0].Fingerprint == "" {
		t.Error("fingerprint should not be empty")
	}
}

func TestAdminCreateKeyReturnsRawKeyOnce(t *testing.T) {
	admin := &fakeAdminManager{
		accounts: []auth.AccountSummary{
			{Account: auth.Account{ID: 6, Name: "six", Role: auth.RoleUser, Plan: "pro", Status: auth.AccountStatusActive}},
		},
	}
	router := newAdminRouter(t, admin, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodPost, "/v1/admin/keys", adminTestAPIKey, `{"account_id":6,"name":"cli"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	body := decodeAdminEnvelope(t, rec)
	var data adminCreatedKeyResponse
	if err := json.Unmarshal(body["data"], &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.APIKey == "" {
		t.Error("raw api key should be returned once")
	}
	if data.AccountID != 6 || data.Name != "cli" {
		t.Errorf("data = %+v, want account_id=6 name=cli", data)
	}
}

func TestAdminCreateKeyRequiresAccountID(t *testing.T) {
	router := newAdminRouter(t, &fakeAdminManager{}, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodPost, "/v1/admin/keys", adminTestAPIKey, `{"name":"cli"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAdminRevokeKey(t *testing.T) {
	admin := &fakeAdminManager{
		keys: []auth.APIKeySummary{
			{APIKey: auth.APIKey{ID: 11, AccountID: 4, Name: "k11", Status: auth.KeyStatusActive}, AccountName: "owner"},
		},
	}
	router := newAdminRouter(t, admin, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodDelete, "/v1/admin/keys/11", adminTestAPIKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := decodeAdminEnvelope(t, rec)
	var data adminRevokedKeyResponse
	if err := json.Unmarshal(body["data"], &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.ID != 11 || data.Status != "revoked" {
		t.Errorf("data = %+v, want id=11 status=revoked", data)
	}
}

func TestAdminStats(t *testing.T) {
	admin := &fakeAdminManager{
		accountCount: 5,
		keyCounts: map[auth.KeyStatus]int64{
			auth.KeyStatusActive:  4,
			auth.KeyStatusRevoked: 1,
			auth.KeyStatusExpired: 0,
		},
	}
	router := newAdminRouter(t, admin, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodGet, "/v1/admin/stats", adminTestAPIKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := decodeAdminEnvelope(t, rec)
	var data adminStatsData
	if err := json.Unmarshal(body["data"], &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.Accounts.Total != 5 {
		t.Errorf("accounts.total = %d, want 5", data.Accounts.Total)
	}
	if data.Keys.Active != 4 || data.Keys.Revoked != 1 {
		t.Errorf("keys = %+v, want active=4 revoked=1", data.Keys)
	}
}

func TestAdminStatsInvalidWindow(t *testing.T) {
	router := newAdminRouter(t, &fakeAdminManager{}, &fakeAuthenticator{})

	rec := doAdminRequest(router, http.MethodGet, "/v1/admin/stats?from=not-a-date", adminTestAPIKey, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if code := adminErrorCode(t, decodeAdminEnvelope(t, rec)); code != "INVALID_REQUEST" {
		t.Errorf("error.code = %q, want INVALID_REQUEST", code)
	}
}
