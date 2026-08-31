package auth

import (
	"context"
	"time"

	apperrors "rest-api/internal/errors"
)

type Service struct {
	repo Repository
	now  func() time.Time
}

func NewService(repo Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, now: now}
}

func (s *Service) CreateKey(ctx context.Context, accountID int64, name string, expiresAt *time.Time) (string, *APIKey, error) {
	raw, err := GenerateKey()
	if err != nil {
		return "", nil, err
	}

	keyID, err := s.repo.CreateKey(ctx, accountID, name, HashKey(raw), expiresAt)
	if err != nil {
		return "", nil, err
	}

	key := &APIKey{
		ID:        keyID,
		AccountID: accountID,
		Name:      name,
		KeyHash:   HashKey(raw),
		Status:    KeyStatusActive,
		CreatedAt: s.now().UTC(),
		ExpiresAt: expiresAt,
	}

	return raw, key, nil
}

func (s *Service) ListKeys(ctx context.Context, accountID int64) ([]APIKey, error) {
	return s.repo.ListKeys(ctx, accountID)
}

func (s *Service) RevokeKey(ctx context.Context, accountID, keyID int64) error {
	return s.repo.RevokeKey(ctx, accountID, keyID)
}

func (s *Service) Authenticate(ctx context.Context, rawKey string) (*Identity, error) {
	if rawKey == "" {
		return nil, apperrors.APIKeyMissing("api key is required")
	}

	hash := HashKey(rawKey)
	key, err := s.repo.FindKeyByHash(ctx, hash)
	if err != nil {
		if err == ErrKeyNotFound {
			return nil, apperrors.APIKeyInvalid("invalid api key")
		}
		return nil, apperrors.Internal(apperrors.CodeInternalError, "authentication failed").WithCause(err)
	}

	now := s.now().UTC()
	switch key.Status {
	case KeyStatusRevoked:
		return nil, apperrors.APIKeyRevoked("api key revoked")
	case KeyStatusExpired:
		return nil, apperrors.APIKeyExpired("api key expired")
	}
	if key.Expired(now) {
		return nil, apperrors.APIKeyExpired("api key expired")
	}

	account, err := s.repo.FindAccount(ctx, key.AccountID)
	if err != nil {
		if err == ErrAccountNotFound {
			return nil, apperrors.APIKeyInvalid("invalid api key")
		}
		return nil, apperrors.Internal(apperrors.CodeInternalError, "authentication failed").WithCause(err)
	}

	switch account.Status {
	case AccountStatusSuspended:
		return nil, apperrors.AccountSuspended("account suspended")
	case AccountStatusDisabled:
		return nil, apperrors.AccountDisabled("account disabled")
	}

	_ = s.repo.TouchKey(ctx, key.ID, now)

	return &Identity{
		AccountID:   account.ID,
		AccountName: account.Name,
		Role:        account.Role,
		Plan:        account.Plan,
		APIKeyID:    key.ID,
		KeyHash:     hash,
	}, nil
}
