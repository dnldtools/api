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
}

type Authenticator interface {
	Authenticate(ctx context.Context, rawKey string) (*Identity, error)
}

type KeyManager interface {
	CreateKey(ctx context.Context, accountID int64, name string, expiresAt *time.Time) (string, *APIKey, error)

	ListKeys(ctx context.Context, accountID int64) ([]APIKey, error)

	RevokeKey(ctx context.Context, accountID, keyID int64) error
}
