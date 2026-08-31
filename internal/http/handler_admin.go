package http

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rest-api/internal/auth"
	apperrors "rest-api/internal/errors"
	"rest-api/internal/metrics"
	"rest-api/internal/plans"
	"rest-api/internal/quota"
)

// ---------- accounts ----------

type adminAccountSummary struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Plan      string    `json:"plan"`
	Status    string    `json:"status"`
	KeyCount  int64     `json:"key_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type adminListAccountsResponse struct {
	Accounts []adminAccountSummary `json:"accounts"`
}

func adminAccountFromSummary(a auth.AccountSummary) adminAccountSummary {
	return adminAccountSummary{
		ID:        a.ID,
		Name:      a.Name,
		Role:      string(a.Role),
		Plan:      string(a.Plan),
		Status:    string(a.Status),
		KeyCount:  a.KeyCount,
		CreatedAt: a.CreatedAt,
		UpdatedAt: a.UpdatedAt,
	}
}

func adminAccountFromAccount(a auth.Account) adminAccountSummary {
	return adminAccountSummary{
		ID:        a.ID,
		Name:      a.Name,
		Role:      string(a.Role),
		Plan:      string(a.Plan),
		Status:    string(a.Status),
		CreatedAt: a.CreatedAt,
		UpdatedAt: a.UpdatedAt,
	}
}

func handleAdminListAccounts(svc auth.AdminManager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "admin is not configured"))
			return
		}

		accounts, err := svc.ListAccounts(r.Context())
		if err != nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to list accounts").WithCause(err))
			return
		}

		items := make([]adminAccountSummary, 0, len(accounts))
		for _, a := range accounts {
			items = append(items, adminAccountFromSummary(a))
		}

		WriteSuccess(w, r, http.StatusOK, adminListAccountsResponse{Accounts: items})
	}
}

func handleAdminGetAccount(svc auth.AdminManager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "admin is not configured"))
			return
		}

		id, ok := parseAdminID(w, r, errHandler)
		if !ok {
			return
		}

		account, err := svc.GetAccount(r.Context(), id)
		if err != nil {
			if errors.Is(err, auth.ErrAccountNotFound) {
				errHandler.Handle(w, r, apperrors.NotFound(apperrors.CodeResourceNotFound, "account not found"))
				return
			}
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to get account").WithCause(err))
			return
		}

		WriteSuccess(w, r, http.StatusOK, adminAccountFromSummary(*account))
	}
}

type adminUpdateAccountRequest struct {
	Name   *string `json:"name"`
	Role   *string `json:"role"`
	Plan   *string `json:"plan"`
	Status *string `json:"status"`
}

func handleAdminUpdateAccount(svc auth.AdminManager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "admin is not configured"))
			return
		}

		id, ok := parseAdminID(w, r, errHandler)
		if !ok {
			return
		}

		var req adminUpdateAccountRequest
		if err := decodeJSON(r, &req); err != nil {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidJSON, "request body is not valid JSON").WithCause(err))
			return
		}

		changes, err := accountChangesFromRequest(req)
		if err != nil {
			errHandler.Handle(w, r, err)
			return
		}

		account, err := svc.UpdateAccount(r.Context(), id, changes)
		if err != nil {
			if errors.Is(err, auth.ErrAccountNotFound) {
				errHandler.Handle(w, r, apperrors.NotFound(apperrors.CodeResourceNotFound, "account not found"))
				return
			}
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to update account").WithCause(err))
			return
		}

		WriteSuccess(w, r, http.StatusOK, adminAccountFromAccount(*account))
	}
}

func accountChangesFromRequest(req adminUpdateAccountRequest) (auth.AccountChanges, error) {
	changes := auth.AccountChanges{}

	if req.Name == nil && req.Role == nil && req.Plan == nil && req.Status == nil {
		return changes, apperrors.Validation(apperrors.CodeInvalidRequest, "at least one field is required")
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return changes, apperrors.Validation(apperrors.CodeInvalidRequest, "name must not be empty")
		}
		changes.Name = &name
	}

	if req.Role != nil {
		role := auth.Role(*req.Role)
		if role != auth.RoleUser && role != auth.RoleAdmin {
			return changes, apperrors.Validation(apperrors.CodeInvalidRequest, "role must be one of: user, admin")
		}
		changes.Role = &role
	}

	if req.Plan != nil {
		plan := plans.Plan(*req.Plan)
		if _, ok := plans.Defaults()[plan]; !ok {
			return changes, apperrors.Validation(apperrors.CodeInvalidRequest, "plan must be one of: trial, free, pro")
		}
		changes.Plan = &plan
	}

	if req.Status != nil {
		status := auth.AccountStatus(*req.Status)
		switch status {
		case auth.AccountStatusActive, auth.AccountStatusSuspended, auth.AccountStatusDisabled:
		default:
			return changes, apperrors.Validation(apperrors.CodeInvalidRequest, "status must be one of: active, suspended, disabled")
		}
		changes.Status = &status
	}

	return changes, nil
}

type adminDisabledAccountResponse struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

func handleAdminDisableAccount(svc auth.AdminManager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "admin is not configured"))
			return
		}

		id, ok := parseAdminID(w, r, errHandler)
		if !ok {
			return
		}

		if err := svc.DisableAccount(r.Context(), id); err != nil {
			if errors.Is(err, auth.ErrAccountNotFound) {
				errHandler.Handle(w, r, apperrors.NotFound(apperrors.CodeResourceNotFound, "account not found"))
				return
			}
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to disable account").WithCause(err))
			return
		}

		WriteSuccess(w, r, http.StatusOK, adminDisabledAccountResponse{ID: id, Status: string(auth.AccountStatusDisabled)})
	}
}

// ---------- keys ----------

type adminKeySummary struct {
	ID          int64      `json:"id"`
	AccountID   int64      `json:"account_id"`
	AccountName string     `json:"account_name"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Fingerprint string     `json:"fingerprint"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type adminListKeysResponse struct {
	Keys []adminKeySummary `json:"keys"`
}

func adminKeyFromSummary(k auth.APIKeySummary) adminKeySummary {
	return adminKeySummary{
		ID:          k.ID,
		AccountID:   k.AccountID,
		AccountName: k.AccountName,
		Name:        k.Name,
		Status:      string(k.Status),
		Fingerprint: fingerprint(k.KeyHash),
		CreatedAt:   k.CreatedAt,
		LastUsedAt:  k.LastUsedAt,
		ExpiresAt:   k.ExpiresAt,
	}
}

func handleAdminListKeys(svc auth.AdminManager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "admin is not configured"))
			return
		}

		keys, err := svc.ListAllKeys(r.Context())
		if err != nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to list keys").WithCause(err))
			return
		}

		items := make([]adminKeySummary, 0, len(keys))
		for _, k := range keys {
			items = append(items, adminKeyFromSummary(k))
		}

		WriteSuccess(w, r, http.StatusOK, adminListKeysResponse{Keys: items})
	}
}

func handleAdminGetKey(svc auth.AdminManager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "admin is not configured"))
			return
		}

		id, ok := parseAdminID(w, r, errHandler)
		if !ok {
			return
		}

		key, err := svc.GetKey(r.Context(), id)
		if err != nil {
			if errors.Is(err, auth.ErrKeyNotFound) {
				errHandler.Handle(w, r, apperrors.NotFound(apperrors.CodeResourceNotFound, "api key not found"))
				return
			}
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to get key").WithCause(err))
			return
		}

		WriteSuccess(w, r, http.StatusOK, adminKeyFromSummary(*key))
	}
}

type adminCreateKeyRequest struct {
	AccountID int64  `json:"account_id"`
	Name      string `json:"name"`
	ExpiresIn string `json:"expires_in"`
}

type adminCreatedKeyResponse struct {
	ID        int64      `json:"id"`
	AccountID int64      `json:"account_id"`
	Name      string     `json:"name"`
	APIKey    string     `json:"api_key"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func handleAdminCreateKey(svc auth.AdminManager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "admin is not configured"))
			return
		}

		var req adminCreateKeyRequest
		if err := decodeJSON(r, &req); err != nil {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidJSON, "request body is not valid JSON").WithCause(err))
			return
		}

		if req.AccountID <= 0 {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidRequest, "account_id is required"))
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

		if _, err := svc.GetAccount(r.Context(), req.AccountID); err != nil {
			if errors.Is(err, auth.ErrAccountNotFound) {
				errHandler.Handle(w, r, apperrors.NotFound(apperrors.CodeResourceNotFound, "account not found"))
				return
			}
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to verify account").WithCause(err))
			return
		}

		raw, key, err := svc.CreateKey(r.Context(), req.AccountID, name, expiresAt)
		if err != nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to create key").WithCause(err))
			return
		}

		WriteSuccess(w, r, http.StatusCreated, adminCreatedKeyResponse{
			ID:        key.ID,
			AccountID: req.AccountID,
			Name:      key.Name,
			APIKey:    raw,
			Status:    string(key.Status),
			ExpiresAt: key.ExpiresAt,
		})
	}
}

type adminUpdateKeyRequest struct {
	Name   *string `json:"name"`
	Status *string `json:"status"`
}

func handleAdminUpdateKey(svc auth.AdminManager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "admin is not configured"))
			return
		}

		id, ok := parseAdminID(w, r, errHandler)
		if !ok {
			return
		}

		var req adminUpdateKeyRequest
		if err := decodeJSON(r, &req); err != nil {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidJSON, "request body is not valid JSON").WithCause(err))
			return
		}

		changes, err := keyChangesFromRequest(req)
		if err != nil {
			errHandler.Handle(w, r, err)
			return
		}

		key, err := svc.UpdateKey(r.Context(), id, changes)
		if err != nil {
			if errors.Is(err, auth.ErrKeyNotFound) {
				errHandler.Handle(w, r, apperrors.NotFound(apperrors.CodeResourceNotFound, "api key not found"))
				return
			}
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to update key").WithCause(err))
			return
		}

		WriteSuccess(w, r, http.StatusOK, adminKeyFromSummary(*key))
	}
}

func keyChangesFromRequest(req adminUpdateKeyRequest) (auth.KeyChanges, error) {
	changes := auth.KeyChanges{}

	if req.Name == nil && req.Status == nil {
		return changes, apperrors.Validation(apperrors.CodeInvalidRequest, "at least one field is required")
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return changes, apperrors.Validation(apperrors.CodeInvalidRequest, "name must not be empty")
		}
		changes.Name = &name
	}

	if req.Status != nil {
		status := auth.KeyStatus(*req.Status)
		switch status {
		case auth.KeyStatusActive, auth.KeyStatusRevoked, auth.KeyStatusExpired:
		default:
			return changes, apperrors.Validation(apperrors.CodeInvalidRequest, "status must be one of: active, revoked, expired")
		}
		changes.Status = &status
	}

	return changes, nil
}

type adminRevokedKeyResponse struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

func handleAdminRevokeKey(svc auth.AdminManager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "admin is not configured"))
			return
		}

		id, ok := parseAdminID(w, r, errHandler)
		if !ok {
			return
		}

		if err := svc.RevokeKeyByID(r.Context(), id); err != nil {
			if errors.Is(err, auth.ErrKeyNotFound) {
				errHandler.Handle(w, r, apperrors.NotFound(apperrors.CodeResourceNotFound, "api key not found"))
				return
			}
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to revoke key").WithCause(err))
			return
		}

		WriteSuccess(w, r, http.StatusOK, adminRevokedKeyResponse{ID: id, Status: string(auth.KeyStatusRevoked)})
	}
}

// ---------- stats ----------

type adminRequestsStats struct {
	Total         int64   `json:"total"`
	Success       int64   `json:"success"`
	Error         int64   `json:"error"`
	SuccessRate   float64 `json:"success_rate"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
}

type adminAuthStats struct {
	ValidAPIKey   int64 `json:"valid_api_key"`
	InvalidAPIKey int64 `json:"invalid_api_key"`
	MissingAPIKey int64 `json:"missing_api_key"`
	RateLimited   int64 `json:"rate_limited"`
	QuotaExceeded int64 `json:"quota_exceeded"`
}

type adminAccountsStats struct {
	Total int64 `json:"total"`
}

type adminKeysStats struct {
	Active  int64 `json:"active"`
	Revoked int64 `json:"revoked"`
	Expired int64 `json:"expired"`
}

type adminQuotaPeriod struct {
	Total   int64 `json:"total"`
	Success int64 `json:"success"`
	Failed  int64 `json:"failed"`
}

type adminQuotaStats struct {
	Daily   adminQuotaPeriod `json:"daily"`
	Monthly adminQuotaPeriod `json:"monthly"`
}

type adminStatsData struct {
	Requests  adminRequestsStats     `json:"requests"`
	Auth      adminAuthStats         `json:"auth"`
	Endpoints []metrics.EndpointStat `json:"endpoints"`
	Platforms []metrics.PlatformStat `json:"platforms"`
	Accounts  adminAccountsStats     `json:"accounts"`
	Keys      adminKeysStats         `json:"keys"`
	Quota     adminQuotaStats        `json:"quota"`
}

func handleAdminStats(metricsSvc *metrics.Service, admin auth.AdminManager, quotaSvc quota.Service, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "admin is not configured"))
			return
		}

		from, to, err := statsWindow(r)
		if err != nil {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidRequest, "invalid from/to parameter"))
			return
		}

		var (
			stats     metrics.Stats
			endpoints []metrics.EndpointStat
			platforms []metrics.PlatformStat
		)
		if metricsSvc != nil {
			if stats, err = metricsSvc.Aggregate(r.Context(), from, to); err != nil {
				errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to aggregate metrics").WithCause(err))
				return
			}
			if endpoints, err = metricsSvc.EndpointBreakdown(r.Context(), from, to); err != nil {
				errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to load endpoint breakdown").WithCause(err))
				return
			}
			if platforms, err = metricsSvc.PlatformBreakdown(r.Context(), from, to); err != nil {
				errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to load platform breakdown").WithCause(err))
				return
			}
		}

		totalAccounts, err := admin.AccountCount(r.Context())
		if err != nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to count accounts").WithCause(err))
			return
		}

		keyCounts, err := admin.KeyCounts(r.Context())
		if err != nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to count keys").WithCause(err))
			return
		}

		var qUsage quota.Usage
		if quotaSvc != nil {
			if qUsage, err = quotaSvc.TotalUsage(r.Context()); err != nil {
				errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to load quota usage").WithCause(err))
				return
			}
		}

		WriteSuccess(w, r, http.StatusOK, adminStatsData{
			Requests: adminRequestsStats{
				Total:         stats.TotalRequests,
				Success:       stats.SuccessCount,
				Error:         stats.ErrorCount,
				SuccessRate:   stats.SuccessRate,
				AvgDurationMs: stats.AvgDurationMs,
			},
			Auth: adminAuthStats{
				ValidAPIKey:   stats.ValidAPIKeyCount,
				InvalidAPIKey: stats.InvalidAPIKeyCount,
				MissingAPIKey: stats.MissingAPIKeyCount,
				RateLimited:   stats.RateLimitedCount,
				QuotaExceeded: stats.QuotaExceededCount,
			},
			Endpoints: endpoints,
			Platforms: platforms,
			Accounts:  adminAccountsStats{Total: totalAccounts},
			Keys: adminKeysStats{
				Active:  keyCounts[auth.KeyStatusActive],
				Revoked: keyCounts[auth.KeyStatusRevoked],
				Expired: keyCounts[auth.KeyStatusExpired],
			},
			Quota: adminQuotaStats{
				Daily: adminQuotaPeriod{
					Total:   qUsage.DailyTotal,
					Success: qUsage.DailySuccess,
					Failed:  qUsage.DailyFailed,
				},
				Monthly: adminQuotaPeriod{
					Total:   qUsage.MonthlyTotal,
					Success: qUsage.MonthlySuccess,
					Failed:  qUsage.MonthlyFailed,
				},
			},
		})
	}
}

func statsWindow(r *http.Request) (time.Time, time.Time, error) {
	to := time.Now().UTC()
	from := to.Add(-24 * time.Hour)

	if v := r.URL.Query().Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from: %w", err)
		}
		from = t
	}
	if v := r.URL.Query().Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to: %w", err)
		}
		to = t
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("from must be before to")
	}

	return from, to, nil
}

// ---------- helpers ----------

func parseAdminID(w http.ResponseWriter, r *http.Request, errHandler *ErrorHandler) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidRequest, "invalid id"))
		return 0, false
	}
	return id, true
}
