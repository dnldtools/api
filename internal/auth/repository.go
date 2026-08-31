package auth

import (
	"context"
	"errors"
	"time"
)

var (
	ErrKeyNotFound = errors.New("auth: api key not found")

	ErrAccountNotFound = errors.New("auth: account not found")
)

type Repository interface {
	FindKeyByHash(ctx context.Context, hash string) (*APIKey, error)

	FindAccount(ctx context.Context, id int64) (*Account, error)

	TouchKey(ctx context.Context, id int64, now time.Time) error

	CreateAccount(ctx context.Context, name string, role Role, plan string) (int64, error)

	CreateKey(ctx context.Context, accountID int64, name, keyHash string, expiresAt *time.Time) (int64, error)

	ListKeys(ctx context.Context, accountID int64) ([]APIKey, error)

	RevokeKey(ctx context.Context, accountID, keyID int64) error

	// Admin operations (cross-account management).
	ListAccounts(ctx context.Context) ([]AccountSummary, error)

	FindAccountSummary(ctx context.Context, id int64) (*AccountSummary, error)

	UpdateAccount(ctx context.Context, id int64, changes AccountChanges) (*Account, error)

	DisableAccount(ctx context.Context, id int64) error

	ListAllKeys(ctx context.Context) ([]APIKeySummary, error)

	FindKeyByID(ctx context.Context, id int64) (*APIKeySummary, error)

	UpdateKey(ctx context.Context, id int64, changes KeyChanges) (*APIKeySummary, error)

	RevokeKeyByID(ctx context.Context, id int64) error

	CountAccounts(ctx context.Context) (int64, error)

	CountKeysByStatus(ctx context.Context) (map[KeyStatus]int64, error)
}

type Authenticator interface {
	Authenticate(ctx context.Context, rawKey string) (*Identity, error)
}

type KeyManager interface {
	CreateKey(ctx context.Context, accountID int64, name string, expiresAt *time.Time) (string, *APIKey, error)

	ListKeys(ctx context.Context, accountID int64) ([]APIKey, error)

	RevokeKey(ctx context.Context, accountID, keyID int64) error
}

// AdminManager is the cross-account management surface used by the
// /v1/admin/* endpoints. *Service implements it.
type AdminManager interface {
	ListAccounts(ctx context.Context) ([]AccountSummary, error)

	GetAccount(ctx context.Context, id int64) (*AccountSummary, error)

	UpdateAccount(ctx context.Context, id int64, changes AccountChanges) (*Account, error)

	DisableAccount(ctx context.Context, id int64) error

	ListAllKeys(ctx context.Context) ([]APIKeySummary, error)

	GetKey(ctx context.Context, id int64) (*APIKeySummary, error)

	CreateKey(ctx context.Context, accountID int64, name string, expiresAt *time.Time) (string, *APIKey, error)

	UpdateKey(ctx context.Context, id int64, changes KeyChanges) (*APIKeySummary, error)

	RevokeKeyByID(ctx context.Context, id int64) error

	AccountCount(ctx context.Context) (int64, error)

	KeyCounts(ctx context.Context) (map[KeyStatus]int64, error)
}
