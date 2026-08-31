package auth

import (
	"context"
	"testing"
	"time"

	apperrors "rest-api/internal/errors"
	"rest-api/internal/plans"
)

type fakeRepo struct {
	key        *APIKey
	keyErr     error
	account    *Account
	accountErr error
	touchedID  int64
	touchedAt  time.Time

	keys      []APIKey
	listErr   error
	revokeErr error

	revokeAccountID int64
	revokeKeyID     int64
}

func (f *fakeRepo) FindKeyByHash(context.Context, string) (*APIKey, error) {
	if f.keyErr != nil {
		return nil, f.keyErr
	}
	if f.key == nil {
		return nil, ErrKeyNotFound
	}
	return f.key, nil
}

func (f *fakeRepo) FindAccount(context.Context, int64) (*Account, error) {
	if f.accountErr != nil {
		return nil, f.accountErr
	}
	if f.account == nil {
		return nil, ErrAccountNotFound
	}
	return f.account, nil
}

func (f *fakeRepo) TouchKey(_ context.Context, id int64, now time.Time) error {
	f.touchedID = id
	f.touchedAt = now
	return nil
}

func (f *fakeRepo) CreateAccount(context.Context, string, Role, string) (int64, error) {
	return 1, nil
}

func (f *fakeRepo) CreateKey(context.Context, int64, string, string, *time.Time) (int64, error) {
	return 1, nil
}

func (f *fakeRepo) ListKeys(context.Context, int64) ([]APIKey, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.keys, nil
}

func (f *fakeRepo) RevokeKey(_ context.Context, accountID, keyID int64) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.revokeAccountID = accountID
	f.revokeKeyID = keyID
	return nil
}

func activeAccount() *Account {
	return &Account{
		ID:     42,
		Name:   "test",
		Role:   RoleUser,
		Plan:   plans.PlanPro,
		Status: AccountStatusActive,
	}
}

func activeKey() *APIKey {
	return &APIKey{
		ID:        7,
		AccountID: 42,
		Name:      "test-key",
		KeyHash:   HashKey("raw-key"),
		Status:    KeyStatusActive,
	}
}

func TestGenerateKey(t *testing.T) {
	k1, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	k2, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	if len(k1) == 0 || len(k2) == 0 {
		t.Fatal("generated keys must not be empty")
	}
	if k1 == k2 {
		t.Error("generated keys must be unique")
	}
	const prefix = "ra_"
	if len(k1) < len(prefix) || k1[:len(prefix)] != prefix {
		t.Errorf("key %q must start with %q", k1, prefix)
	}
}

func TestHashKey(t *testing.T) {
	h1 := HashKey("secret-key")
	h2 := HashKey("secret-key")
	h3 := HashKey("other-key")

	if h1 != h2 {
		t.Error("HashKey should be deterministic")
	}
	if h1 == h3 {
		t.Error("different keys should produce different hashes")
	}
	if len(h1) != 64 {
		t.Errorf("SHA-256 hex digest should be 64 chars, got %d", len(h1))
	}
	if h1 == "secret-key" {
		t.Error("hash must never equal the raw key")
	}
}

func assertAuthError(t *testing.T, err error, code apperrors.Code, status int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %q, got nil", code)
	}
	appErr, ok := apperrors.AsAppError(err)
	if !ok {
		t.Fatalf("expected *AppError, got %T", err)
	}
	if appErr.Code != code {
		t.Errorf("code = %q, want %q", appErr.Code, code)
	}
	if appErr.Status != status {
		t.Errorf("status = %d, want %d", appErr.Status, status)
	}
}

func TestAuthenticateMissingKey(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	_, err := svc.Authenticate(context.Background(), "")
	assertAuthError(t, err, apperrors.CodeAPIKeyMissing, 401)
}

func TestAuthenticateUnknownKey(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	_, err := svc.Authenticate(context.Background(), "unknown")
	assertAuthError(t, err, apperrors.CodeAPIKeyInvalid, 401)
}

func TestAuthenticateRevokedKey(t *testing.T) {
	key := activeKey()
	key.Status = KeyStatusRevoked
	svc := NewService(&fakeRepo{key: key, account: activeAccount()}, nil)
	_, err := svc.Authenticate(context.Background(), "raw-key")
	assertAuthError(t, err, apperrors.CodeAPIKeyRevoked, 401)
}

func TestAuthenticateExpiredKeyByStatus(t *testing.T) {
	key := activeKey()
	key.Status = KeyStatusExpired
	svc := NewService(&fakeRepo{key: key, account: activeAccount()}, nil)
	_, err := svc.Authenticate(context.Background(), "raw-key")
	assertAuthError(t, err, apperrors.CodeAPIKeyExpired, 401)
}

func TestAuthenticateExpiredKeyByTime(t *testing.T) {
	key := activeKey()
	past := time.Now().Add(-time.Hour)
	key.ExpiresAt = &past
	svc := NewService(&fakeRepo{key: key, account: activeAccount()}, nil)
	_, err := svc.Authenticate(context.Background(), "raw-key")
	assertAuthError(t, err, apperrors.CodeAPIKeyExpired, 401)
}

func TestAuthenticateSuspendedAccount(t *testing.T) {
	acct := activeAccount()
	acct.Status = AccountStatusSuspended
	svc := NewService(&fakeRepo{key: activeKey(), account: acct}, nil)
	_, err := svc.Authenticate(context.Background(), "raw-key")
	assertAuthError(t, err, apperrors.CodeAccountSuspended, 403)
}

func TestAuthenticateDisabledAccount(t *testing.T) {
	acct := activeAccount()
	acct.Status = AccountStatusDisabled
	svc := NewService(&fakeRepo{key: activeKey(), account: acct}, nil)
	_, err := svc.Authenticate(context.Background(), "raw-key")
	assertAuthError(t, err, apperrors.CodeAccountDisabled, 403)
}

func TestAuthenticateUnknownAccount(t *testing.T) {
	svc := NewService(&fakeRepo{key: activeKey()}, nil)
	_, err := svc.Authenticate(context.Background(), "raw-key")
	assertAuthError(t, err, apperrors.CodeAPIKeyInvalid, 401)
}

func TestAuthenticateValidKey(t *testing.T) {
	repo := &fakeRepo{key: activeKey(), account: activeAccount()}
	fixedNow := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	svc := NewService(repo, func() time.Time { return fixedNow })

	id, err := svc.Authenticate(context.Background(), "raw-key")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if id.AccountID != 42 {
		t.Errorf("account ID = %d, want 42", id.AccountID)
	}
	if id.Plan != plans.PlanPro {
		t.Errorf("plan = %q, want pro", id.Plan)
	}
	if id.KeyHash != HashKey("raw-key") {
		t.Errorf("key hash = %q, want hash of raw-key", id.KeyHash)
	}
	if id.APIKeyID != 7 {
		t.Errorf("api key ID = %d, want 7", id.APIKeyID)
	}
	if repo.touchedID != 7 || repo.touchedAt != fixedNow {
		t.Errorf("TouchKey not called with expected values: id=%d at=%v", repo.touchedID, repo.touchedAt)
	}
}

func TestAuthenticateRepoErrorIsInternal(t *testing.T) {
	svc := NewService(&fakeRepo{keyErr: errRepoBoom}, nil)
	_, err := svc.Authenticate(context.Background(), "raw-key")
	assertAuthError(t, err, apperrors.CodeInternalError, 500)
}

var errRepoBoom = &repoErr{}

type repoErr struct{}

func (e *repoErr) Error() string { return "db boom" }

func TestCreateKeyReturnsRawKeyAndHash(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)

	raw, key, err := svc.CreateKey(context.Background(), 42, "my-key", nil)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if len(raw) < 3 || raw[:3] != "ra_" {
		t.Errorf("raw key %q must start with ra_", raw)
	}
	if key == nil {
		t.Fatal("key must not be nil")
	}
	if key.ID != 1 {
		t.Errorf("key ID = %d, want 1", key.ID)
	}
	if key.AccountID != 42 {
		t.Errorf("account ID = %d, want 42", key.AccountID)
	}
	if key.Name != "my-key" {
		t.Errorf("name = %q, want my-key", key.Name)
	}
	if key.Status != KeyStatusActive {
		t.Errorf("status = %q, want active", key.Status)
	}
	if key.KeyHash != HashKey(raw) {
		t.Error("key hash must match SHA-256 of the raw key")
	}
	if key.ExpiresAt != nil {
		t.Error("expires_at should be nil when no expiry is set")
	}
}

func TestListKeysDelegatesToRepository(t *testing.T) {
	repo := &fakeRepo{keys: []APIKey{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}}
	svc := NewService(repo, nil)

	keys, err := svc.ListKeys(context.Background(), 42)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("len = %d, want 2", len(keys))
	}
	if keys[0].Name != "a" || keys[1].Name != "b" {
		t.Errorf("unexpected keys: %+v", keys)
	}
}

func TestRevokeKeyDelegatesToRepository(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, nil)

	if err := svc.RevokeKey(context.Background(), 42, 7); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if repo.revokeAccountID != 42 || repo.revokeKeyID != 7 {
		t.Errorf("revoke args = (%d, %d), want (42, 7)", repo.revokeAccountID, repo.revokeKeyID)
	}
}
