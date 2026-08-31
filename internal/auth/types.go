package auth

import (
	"time"

	"rest-api/internal/plans"
)

type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

type AccountStatus string

const (
	AccountStatusActive    AccountStatus = "active"
	AccountStatusSuspended AccountStatus = "suspended"
	AccountStatusDisabled  AccountStatus = "disabled"
)

type KeyStatus string

const (
	KeyStatusActive  KeyStatus = "active"
	KeyStatusRevoked KeyStatus = "revoked"
	KeyStatusExpired KeyStatus = "expired"
)

type Account struct {
	ID        int64
	Name      string
	Role      Role
	Plan      plans.Plan
	Status    AccountStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type APIKey struct {
	ID         int64
	AccountID  int64
	Name       string
	KeyHash    string
	Status     KeyStatus
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
}

// APIKeySummary is an APIKey enriched with the owning account's name for
// admin-facing listing/detail endpoints (never the raw key).
type APIKeySummary struct {
	APIKey
	AccountName string
}

// AccountSummary is an Account enriched with its API key count for admin
// listing/detail endpoints.
type AccountSummary struct {
	Account
	KeyCount int64
}

// AccountChanges describes the mutable fields for an admin account update.
// Nil pointers leave the corresponding field unchanged.
type AccountChanges struct {
	Name   *string
	Role   *Role
	Plan   *plans.Plan
	Status *AccountStatus
}

// KeyChanges describes the mutable fields for an admin API-key update.
// Nil pointers leave the corresponding field unchanged.
type KeyChanges struct {
	Name   *string
	Status *KeyStatus
}

func (k APIKey) Expired(now time.Time) bool {
	return k.ExpiresAt != nil && !k.ExpiresAt.After(now)
}

type Identity struct {
	AccountID   int64
	AccountName string
	Role        Role
	Plan        plans.Plan
	APIKeyID    int64
	KeyHash     string
}
