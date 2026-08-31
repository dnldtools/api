package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"rest-api/internal/plans"
	"rest-api/internal/testutil"
)

func TestPostgresRepositoryAccountRoundTrip(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Truncate(t, pool)

	repo := NewPostgresRepository(pool)
	ctx := context.Background()

	if _, err := repo.FindAccount(ctx, 999999); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("FindAccount(missing) = %v, want ErrAccountNotFound", err)
	}

	id, err := repo.CreateAccount(ctx, "int-test-account", RoleAdmin, string(plans.PlanPro))
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	acc, err := repo.FindAccount(ctx, id)
	if err != nil {
		t.Fatalf("FindAccount: %v", err)
	}
	if acc.ID != id {
		t.Errorf("ID = %d, want %d", acc.ID, id)
	}
	if acc.Name != "int-test-account" {
		t.Errorf("Name = %q, want int-test-account", acc.Name)
	}
	if acc.Role != RoleAdmin {
		t.Errorf("Role = %q, want admin", acc.Role)
	}
	if acc.Plan != plans.PlanPro {
		t.Errorf("Plan = %q, want pro", acc.Plan)
	}
	if acc.Status != AccountStatusActive {
		t.Errorf("Status = %q, want active (default)", acc.Status)
	}
	if acc.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}

func TestPostgresRepositoryKeyRoundTrip(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Truncate(t, pool)

	repo := NewPostgresRepository(pool)
	ctx := context.Background()

	accountID, err := repo.CreateAccount(ctx, "key-roundtrip", RoleUser, string(plans.PlanTrial))
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	if _, err := repo.FindKeyByHash(ctx, "no-such-hash"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("FindKeyByHash(missing) = %v, want ErrKeyNotFound", err)
	}

	raw, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	hash := HashKey(raw)

	expiry := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Microsecond)
	keyID, err := repo.CreateKey(ctx, accountID, "int-test-key", hash, &expiry)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	k, err := repo.FindKeyByHash(ctx, hash)
	if err != nil {
		t.Fatalf("FindKeyByHash: %v", err)
	}
	if k.ID != keyID {
		t.Errorf("ID = %d, want %d", k.ID, keyID)
	}
	if k.AccountID != accountID {
		t.Errorf("AccountID = %d, want %d", k.AccountID, accountID)
	}
	if k.KeyHash != hash {
		t.Errorf("KeyHash = %q, want %q", k.KeyHash, hash)
	}

	if k.KeyHash == raw {
		t.Error("KeyHash equals the raw key — raw key must never be stored")
	}
	if k.Status != KeyStatusActive {
		t.Errorf("Status = %q, want active (default)", k.Status)
	}
	if k.ExpiresAt == nil || !k.ExpiresAt.Equal(expiry) {
		t.Errorf("ExpiresAt = %v, want %v", k.ExpiresAt, expiry)
	}
	if k.Expired(time.Now()) {
		t.Error("Expired(now) = true, want false for a 24h expiry")
	}

	usedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	if err := repo.TouchKey(ctx, keyID, usedAt); err != nil {
		t.Fatalf("TouchKey: %v", err)
	}
	k2, err := repo.FindKeyByHash(ctx, hash)
	if err != nil {
		t.Fatalf("FindKeyByHash(after touch): %v", err)
	}
	if k2.LastUsedAt == nil {
		t.Fatal("LastUsedAt = nil after TouchKey, want non-nil")
	}
	if !k2.LastUsedAt.Equal(usedAt) {
		t.Errorf("LastUsedAt = %v, want %v", k2.LastUsedAt, usedAt)
	}
}

func TestPostgresRepositoryExpiredKey(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC()
	key := APIKey{ExpiresAt: &past}
	if !key.Expired(time.Now()) {
		t.Error("Expired(now) = false, want true for a past expiry")
	}
	future := time.Now().Add(time.Hour).UTC()
	key.ExpiresAt = &future
	if key.Expired(time.Now()) {
		t.Error("Expired(now) = true, want false for a future expiry")
	}
}

func TestPostgresRepositoryListAndRevokeKeys(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Truncate(t, pool)

	repo := NewPostgresRepository(pool)
	ctx := context.Background()

	accountID, err := repo.CreateAccount(ctx, "list-revoke", RoleUser, string(plans.PlanFree))
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	otherID, err := repo.CreateAccount(ctx, "other", RoleUser, string(plans.PlanFree))
	if err != nil {
		t.Fatalf("CreateAccount(other): %v", err)
	}

	key1, err := repo.CreateKey(ctx, accountID, "k1", HashKey("raw-1"), nil)
	if err != nil {
		t.Fatalf("CreateKey(k1): %v", err)
	}
	key2, err := repo.CreateKey(ctx, accountID, "k2", HashKey("raw-2"), nil)
	if err != nil {
		t.Fatalf("CreateKey(k2): %v", err)
	}

	keys, err := repo.ListKeys(ctx, accountID)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("len = %d, want 2", len(keys))
	}
	if keys[0].ID != key1 || keys[1].ID != key2 {
		t.Errorf("key order = [%d %d], want [%d %d]", keys[0].ID, keys[1].ID, key1, key2)
	}

	if err := repo.RevokeKey(ctx, accountID, key1); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	keys, err = repo.ListKeys(ctx, accountID)
	if err != nil {
		t.Fatalf("ListKeys(after revoke): %v", err)
	}
	if keys[0].Status != KeyStatusRevoked {
		t.Errorf("key1 status = %q, want revoked", keys[0].Status)
	}
	if keys[1].Status != KeyStatusActive {
		t.Errorf("key2 status = %q, want active", keys[1].Status)
	}

	if err := repo.RevokeKey(ctx, accountID, key1); err != nil {
		t.Errorf("RevokeKey(already revoked) = %v, want nil (idempotent)", err)
	}

	if err := repo.RevokeKey(ctx, otherID, key2); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("RevokeKey(other account) = %v, want ErrKeyNotFound", err)
	}

	if err := repo.RevokeKey(ctx, accountID, 999999); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("RevokeKey(missing) = %v, want ErrKeyNotFound", err)
	}
}
