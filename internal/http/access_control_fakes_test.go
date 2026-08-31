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

var testIdentity = &auth.Identity{
	AccountID:   42,
	AccountName: "Test Account",
	Role:        auth.RoleUser,
	Plan:        plans.PlanPro,
	APIKeyID:    7,
	KeyHash:     auth.HashKey(testAPIKey),
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
