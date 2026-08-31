package http

import (
	"net/http"

	apperrors "rest-api/internal/errors"
	"rest-api/internal/plans"
	"rest-api/internal/quota"
)

type accountResponse struct {
	AccountID int64  `json:"account_id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Plan      string `json:"plan"`
	APIKeyID  int64  `json:"api_key_id"`
}

func handleAccount(errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFromContext(r.Context())
		if !ok {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "identity is missing"))
			return
		}

		WriteSuccess(w, r, http.StatusOK, accountResponse{
			AccountID: identity.AccountID,
			Name:      identity.AccountName,
			Role:      string(identity.Role),
			Plan:      string(identity.Plan),
			APIKeyID:  identity.APIKeyID,
		})
	}
}

type usagePeriod struct {
	Used      int64 `json:"used"`
	Quota     int64 `json:"quota"`
	Remaining int64 `json:"remaining"`
	Success   int64 `json:"success"`
	Failed    int64 `json:"failed"`
}

type usageResponse struct {
	Plan              string      `json:"plan"`
	RateLimit         int         `json:"rate_limit"`
	RateWindowSeconds int64       `json:"rate_window_seconds"`
	Daily             usagePeriod `json:"daily"`
	Monthly           usagePeriod `json:"monthly"`
}

func handleUsage(svc quota.Service, policies plans.PolicySet, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFromContext(r.Context())
		if !ok {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "identity is missing"))
			return
		}
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "usage is not configured"))
			return
		}

		report, err := svc.Usage(r.Context(), identity.AccountID, identity.Plan)
		if err != nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "usage lookup failed").WithCause(err))
			return
		}

		policy := policies.Get(identity.Plan)

		WriteSuccess(w, r, http.StatusOK, usageResponse{
			Plan:              string(identity.Plan),
			RateLimit:         policy.RateLimit,
			RateWindowSeconds: int64(policy.RateWindow.Seconds()),
			Daily: usagePeriod{
				Used:      report.DailyTotal,
				Quota:     report.DailyQuota,
				Remaining: report.DailyRemaining,
				Success:   report.DailySuccess,
				Failed:    report.DailyFailed,
			},
			Monthly: usagePeriod{
				Used:      report.MonthlyTotal,
				Quota:     report.MonthlyQuota,
				Remaining: report.MonthlyRemaining,
				Success:   report.MonthlySuccess,
				Failed:    report.MonthlyFailed,
			},
		})
	}
}
