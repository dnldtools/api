package http

import (
	"context"
	"sync"
	"time"

	"rest-api/internal/auth"
	apperrors "rest-api/internal/errors"
	"rest-api/internal/plans"
	"rest-api/internal/quota"
	"rest-api/internal/ratelimit"
)

const testAPIKey = "test-key"

const adminTestAPIKey = "admin-test-key"

var testIdentity = &auth.Identity{
	AccountID:   42,
	AccountName: "Test Account",
	Role:        auth.RoleUser,
	Plan:        plans.PlanPro,
	APIKeyID:    7,
	KeyHash:     auth.HashKey(testAPIKey),
}

var adminIdentity = &auth.Identity{
	AccountID:   1,
	AccountName: "Admin",
	Role:        auth.RoleAdmin,
	Plan:        plans.PlanPro,
	APIKeyID:    1,
	KeyHash:     auth.HashKey(adminTestAPIKey),
}

type fakeAuthenticator struct {
	fn func(ctx context.Context, raw string) (*auth.Identity, error)
}

func (f *fakeAuthenticator) Authenticate(ctx context.Context, raw string) (*auth.Identity, error) {
	if f.fn != nil {
		return f.fn(ctx, raw)
	}
	switch {
	case raw == testAPIKey:
		return testIdentity, nil
	case raw == adminTestAPIKey:
		return adminIdentity, nil
	case raw == "":
		return nil, apperrors.APIKeyMissing("api key is required")
	default:
		return nil, apperrors.APIKeyInvalid("invalid api key")
	}
}

type recordedQuota struct {
	accountID  int64
	plan       plans.Plan
	statusCode int
}

type fakeQuota struct {
	checkFn func(ctx context.Context, accountID int64, plan plans.Plan) (quota.Result, error)

	mu       sync.Mutex
	recorded []recordedQuota
}

func (f *fakeQuota) Check(ctx context.Context, accountID int64, plan plans.Plan) (quota.Result, error) {
	if f.checkFn != nil {
		return f.checkFn(ctx, accountID, plan)
	}
	return quota.Result{Allowed: true, DailyRemaining: -1, MonthlyRemaining: -1}, nil
}

func (f *fakeQuota) Record(_ context.Context, accountID int64, plan plans.Plan, statusCode int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = append(f.recorded, recordedQuota{accountID: accountID, plan: plan, statusCode: statusCode})
}

func (f *fakeQuota) Usage(context.Context, int64, plans.Plan) (quota.UsageReport, error) {
	return quota.UsageReport{
		Usage:            quota.Usage{DailyTotal: 0, MonthlyTotal: 0},
		DailyQuota:       -1,
		MonthlyQuota:     -1,
		DailyRemaining:   -1,
		MonthlyRemaining: -1,
	}, nil
}

func (f *fakeQuota) TotalUsage(context.Context) (quota.Usage, error) {
	return quota.Usage{}, nil
}

func (f *fakeQuota) snapshot() []recordedQuota {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedQuota, len(f.recorded))
	copy(out, f.recorded)
	return out
}

type fakeLimiter struct {
	result ratelimit.Result
	err    error
}

func (f *fakeLimiter) Allow(context.Context, string, int, time.Duration) (ratelimit.Result, error) {
	return f.result, f.err
}

type fakeKeyManager struct {
	keys       []auth.APIKey
	createErr  error
	listErr    error
	revokeErr  error
	revokedIDs []int64
}

func (f *fakeKeyManager) CreateKey(_ context.Context, _ int64, name string, expiresAt *time.Time) (string, *auth.APIKey, error) {
	if f.createErr != nil {
		return "", nil, f.createErr
	}
	key := &auth.APIKey{
		ID:        99,
		Name:      name,
		KeyHash:   auth.HashKey("new-raw-key"),
		Status:    auth.KeyStatusActive,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
	}
	return "ra_newrawkey", key, nil
}

func (f *fakeKeyManager) ListKeys(_ context.Context, _ int64) ([]auth.APIKey, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.keys, nil
}

func (f *fakeKeyManager) RevokeKey(_ context.Context, _ int64, keyID int64) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.revokedIDs = append(f.revokedIDs, keyID)
	return nil
}

type fakeAdminManager struct {
	accounts        []auth.AccountSummary
	account         *auth.AccountSummary
	accountErr      error
	updateAccount   *auth.Account
	updateAccountFn func(id int64, changes auth.AccountChanges) (*auth.Account, error)
	disableErr      error

	keys        []auth.APIKeySummary
	key         *auth.APIKeySummary
	keyErr      error
	createKeyFn func(accountID int64, name string, expiresAt *time.Time) (string, *auth.APIKey, error)
	updateKey   *auth.APIKeySummary
	updateKeyFn func(id int64, changes auth.KeyChanges) (*auth.APIKeySummary, error)
	revokeErr   error

	accountCount int64
	keyCounts    map[auth.KeyStatus]int64
}

func (f *fakeAdminManager) ListAccounts(context.Context) ([]auth.AccountSummary, error) {
	if f.accountErr != nil {
		return nil, f.accountErr
	}
	return f.accounts, nil
}

func (f *fakeAdminManager) GetAccount(_ context.Context, id int64) (*auth.AccountSummary, error) {
	if f.accountErr != nil {
		return nil, f.accountErr
	}
	if f.account != nil && f.account.ID == id {
		return f.account, nil
	}
	for i := range f.accounts {
		if f.accounts[i].ID == id {
			return &f.accounts[i], nil
		}
	}
	return nil, auth.ErrAccountNotFound
}

func (f *fakeAdminManager) UpdateAccount(_ context.Context, id int64, changes auth.AccountChanges) (*auth.Account, error) {
	if f.updateAccountFn != nil {
		return f.updateAccountFn(id, changes)
	}
	if f.updateAccount != nil {
		return f.updateAccount, nil
	}
	return nil, auth.ErrAccountNotFound
}

func (f *fakeAdminManager) DisableAccount(_ context.Context, id int64) error {
	if f.disableErr != nil {
		return f.disableErr
	}
	for i := range f.accounts {
		if f.accounts[i].ID == id {
			return nil
		}
	}
	return auth.ErrAccountNotFound
}

func (f *fakeAdminManager) ListAllKeys(context.Context) ([]auth.APIKeySummary, error) {
	if f.keyErr != nil {
		return nil, f.keyErr
	}
	return f.keys, nil
}

func (f *fakeAdminManager) GetKey(_ context.Context, id int64) (*auth.APIKeySummary, error) {
	if f.keyErr != nil {
		return nil, f.keyErr
	}
	if f.key != nil && f.key.ID == id {
		return f.key, nil
	}
	for i := range f.keys {
		if f.keys[i].ID == id {
			return &f.keys[i], nil
		}
	}
	return nil, auth.ErrKeyNotFound
}

func (f *fakeAdminManager) CreateKey(_ context.Context, accountID int64, name string, expiresAt *time.Time) (string, *auth.APIKey, error) {
	if f.createKeyFn != nil {
		return f.createKeyFn(accountID, name, expiresAt)
	}
	key := &auth.APIKey{
		ID:        100,
		AccountID: accountID,
		Name:      name,
		KeyHash:   auth.HashKey("ra_admin_created"),
		Status:    auth.KeyStatusActive,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
	}
	return "ra_admin_created", key, nil
}

func (f *fakeAdminManager) UpdateKey(_ context.Context, id int64, changes auth.KeyChanges) (*auth.APIKeySummary, error) {
	if f.updateKeyFn != nil {
		return f.updateKeyFn(id, changes)
	}
	if f.updateKey != nil {
		return f.updateKey, nil
	}
	return nil, auth.ErrKeyNotFound
}

func (f *fakeAdminManager) RevokeKeyByID(_ context.Context, id int64) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	for i := range f.keys {
		if f.keys[i].ID == id {
			return nil
		}
	}
	return auth.ErrKeyNotFound
}

func (f *fakeAdminManager) AccountCount(context.Context) (int64, error) {
	return f.accountCount, nil
}

func (f *fakeAdminManager) KeyCounts(context.Context) (map[auth.KeyStatus]int64, error) {
	if f.keyCounts == nil {
		return map[auth.KeyStatus]int64{}, nil
	}
	return f.keyCounts, nil
}
