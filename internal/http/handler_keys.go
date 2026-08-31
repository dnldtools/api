package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rest-api/internal/auth"
	apperrors "rest-api/internal/errors"
)

type keySummary struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Fingerprint string     `json:"fingerprint"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type listKeysResponse struct {
	Keys []keySummary `json:"keys"`
}

func handleListKeys(svc auth.KeyManager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFromContext(r.Context())
		if !ok {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "identity is missing"))
			return
		}
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "key management is not configured"))
			return
		}

		keys, err := svc.ListKeys(r.Context(), identity.AccountID)
		if err != nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to list keys").WithCause(err))
			return
		}

		summaries := make([]keySummary, 0, len(keys))
		for _, k := range keys {
			summaries = append(summaries, summaryFromKey(k))
		}

		WriteSuccess(w, r, http.StatusOK, listKeysResponse{Keys: summaries})
	}
}

type createKeyRequest struct {
	Name      string `json:"name"`
	ExpiresIn string `json:"expires_in"`
}

type createdKeyResponse struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	APIKey    string     `json:"api_key"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func handleCreateKey(svc auth.KeyManager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFromContext(r.Context())
		if !ok {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "identity is missing"))
			return
		}
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "key management is not configured"))
			return
		}

		var req createKeyRequest
		if err := decodeJSON(r, &req); err != nil {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidJSON, "request body is not valid JSON").WithCause(err))
			return
		}

		name := strings.TrimSpace(req.Name)
		if name == "" {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeMissingParameter, "name is required"))
			return
		}

		var expiresAt *time.Time
		if req.ExpiresIn != "" {
			d, err := time.ParseDuration(req.ExpiresIn)
			if err != nil || d <= 0 {
				errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidRequest, "expires_in must be a positive duration"))
				return
			}
			t := time.Now().UTC().Add(d)
			expiresAt = &t
		}

		raw, key, err := svc.CreateKey(r.Context(), identity.AccountID, name, expiresAt)
		if err != nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to create key").WithCause(err))
			return
		}

		WriteSuccess(w, r, http.StatusCreated, createdKeyResponse{
			ID:        key.ID,
			Name:      key.Name,
			APIKey:    raw,
			Status:    string(key.Status),
			ExpiresAt: key.ExpiresAt,
		})
	}
}

type revokedKeyResponse struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

func handleRevokeKey(svc auth.KeyManager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFromContext(r.Context())
		if !ok {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "identity is missing"))
			return
		}
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "key management is not configured"))
			return
		}

		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidRequest, "invalid key id"))
			return
		}

		if err := svc.RevokeKey(r.Context(), identity.AccountID, id); err != nil {
			if errors.Is(err, auth.ErrKeyNotFound) {
				errHandler.Handle(w, r, apperrors.NotFound(apperrors.CodeResourceNotFound, "api key not found"))
				return
			}
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to revoke key").WithCause(err))
			return
		}

		WriteSuccess(w, r, http.StatusOK, revokedKeyResponse{ID: id, Status: string(auth.KeyStatusRevoked)})
	}
}

func summaryFromKey(k auth.APIKey) keySummary {
	return keySummary{
		ID:          k.ID,
		Name:        k.Name,
		Status:      string(k.Status),
		Fingerprint: fingerprint(k.KeyHash),
		CreatedAt:   k.CreatedAt,
		LastUsedAt:  k.LastUsedAt,
		ExpiresAt:   k.ExpiresAt,
	}
}

func fingerprint(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}
