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
