package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"rest-api/internal/auth"
	apperrors "rest-api/internal/errors"
	"rest-api/internal/metrics"
	"rest-api/internal/plans"
	"rest-api/internal/quota"
	"rest-api/internal/ratelimit"
)

type contextKey string

const (
	ctxRequestID contextKey = "rest-api.request_id"
	ctxPretty    contextKey = "rest-api.pretty"
	ctxStartTime contextKey = "rest-api.start_time"
	ctxPlatform  contextKey = "rest-api.platform"
	ctxIdentity  contextKey = "rest-api.identity"
	ctxAuthInfo  contextKey = "rest-api.auth_info"
)

const maxRequestIDLength = 128

type Middleware func(http.Handler) http.Handler

func chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}

func requestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ctxRequestID).(string); ok {
		return id
	}
	return ""
}

func prettyFromContext(r *http.Request) bool {
	v, ok := r.Context().Value(ctxPretty).(bool)
	return ok && v
}

func startTimeFromContext(ctx context.Context) (time.Time, bool) {
	t, ok := ctx.Value(ctxStartTime).(time.Time)
	return t, ok
}

func setPlatform(ctx context.Context, platform string) {
	if p, ok := ctx.Value(ctxPlatform).(*string); ok {
		*p = platform
	}
}

func platformFromContext(ctx context.Context) string {
	if p, ok := ctx.Value(ctxPlatform).(*string); ok {
		return *p
	}
	return ""
}

func identityFromContext(ctx context.Context) (*auth.Identity, bool) {
	id, ok := ctx.Value(ctxIdentity).(*auth.Identity)
	return id, ok
}

type requestAuthInfo struct {
	ClientID      string
	AccountID     int64
	ValidAPIKey   bool
	InvalidAPIKey bool
	APIKeyMissing bool
	RateLimited   bool
	QuotaExceeded bool
}

func authInfoFromContext(ctx context.Context) *requestAuthInfo {
	if info, ok := ctx.Value(ctxAuthInfo).(*requestAuthInfo); ok {
		return info
	}
	return nil
}

func recoverMiddleware(errHandler *ErrorHandler) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			defer func() {
				if p := recover(); p != nil {
					errHandler.logger.Error("panic recovered",
						"error", p,
						"stack", string(debug.Stack()),
						"request_id", requestIDFromContext(r.Context()),
					)
					if !rec.wroteHeader {
						errHandler.Handle(rec, r, apperrors.Internal(apperrors.CodeInternalError, "Something went wrong on our end. Please try again."))
					}
				}
			}()

			next.ServeHTTP(rec, r)
		})
	}
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = newRequestID()
		}

		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func prettyMiddleware(prettyByDefault bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pretty := prettyByDefault
			if v := r.URL.Query().Get("pretty"); v != "" {
				pretty = v == "1" || v == "true"
			}

			ctx := context.WithValue(r.Context(), ctxPretty, pretty)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func contentTypeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			ctx := context.WithValue(r.Context(), ctxStartTime, start)
			next.ServeHTTP(rec, r.WithContext(ctx))

			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration", time.Since(start).String(),
				"request_id", requestIDFromContext(ctx),
			)
		})
	}
}

func authMiddleware(authSvc auth.Authenticator, errHandler *ErrorHandler) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.Header.Get("X-API-Key")

			if authSvc == nil {
				errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "authentication is not configured"))
				return
			}

			identity, err := authSvc.Authenticate(r.Context(), raw)
			if err != nil {
				if info := authInfoFromContext(r.Context()); info != nil {
					if raw == "" {
						info.APIKeyMissing = true
					} else {
						info.ClientID = auth.HashKey(raw)
						info.InvalidAPIKey = true
					}
				}
				errHandler.Handle(w, r, err)
				return
			}

			if info := authInfoFromContext(r.Context()); info != nil {
				info.ClientID = identity.KeyHash
				info.AccountID = identity.AccountID
				info.ValidAPIKey = true
			}

			ctx := context.WithValue(r.Context(), ctxIdentity, identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func requireAdminMiddleware(errHandler *ErrorHandler) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := identityFromContext(r.Context())
			if !ok {
				errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "identity is missing"))
				return
			}
			if identity.Role != auth.RoleAdmin {
				errHandler.Handle(w, r, apperrors.Authorization(apperrors.CodeForbidden, "admin access required"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func rateLimitMiddleware(limiter ratelimit.Limiter, policies plans.PolicySet, errHandler *ErrorHandler) Middleware {
	if policies == nil {
		policies = plans.Defaults()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := identityFromContext(r.Context())
			if !ok || limiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Admin keys bypass rate limiting entirely.
			if identity.Role == auth.RoleAdmin {
				next.ServeHTTP(w, r)
				return
			}

			policy := policies.Get(identity.Plan)
			if policy.RateLimit <= 0 {
				next.ServeHTTP(w, r)
				return
			}

			key := "account:" + strconv.FormatInt(identity.AccountID, 10)
			result, err := limiter.Allow(r.Context(), key, policy.RateLimit, policy.RateWindow)
			if err != nil {
				errHandler.logger.Warn("rate limiter error; allowing request", "error", err)
				next.ServeHTTP(w, r)
				return
			}
			if !result.Allowed {
				if info := authInfoFromContext(r.Context()); info != nil {
					info.RateLimited = true
				}
				if result.RetryAfter > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(int(result.RetryAfter.Seconds())+1))
				}
				errHandler.Handle(w, r, apperrors.RateLimitExceeded("rate limit exceeded"))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func quotaMiddleware(svc quota.Service, errHandler *ErrorHandler) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := identityFromContext(r.Context())
			if !ok || svc == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Admin keys bypass quota checks and usage recording.
			if identity.Role == auth.RoleAdmin {
				next.ServeHTTP(w, r)
				return
			}

			result, err := svc.Check(r.Context(), identity.AccountID, identity.Plan)
			if err != nil {
				errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "quota check failed").WithCause(err))
				return
			}
			if !result.Allowed {
				if info := authInfoFromContext(r.Context()); info != nil {
					info.QuotaExceeded = true
				}
				errHandler.Handle(w, r, apperrors.QuotaExceeded("quota exceeded"))
				return
			}

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			svc.Record(r.Context(), identity.AccountID, identity.Plan, rec.status)
		})
	}
}

func metricsMiddleware(svc *metrics.Service) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start, ok := startTimeFromContext(r.Context())
			if !ok {
				start = time.Now()
			}

			var platform string
			info := &requestAuthInfo{}
			ctx := context.WithValue(r.Context(), ctxPlatform, &platform)
			ctx = context.WithValue(ctx, ctxAuthInfo, info)

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))

			if svc != nil {
				svc.Record(metrics.Event{
					RequestID:     requestIDFromContext(ctx),
					StatusCode:    rec.status,
					ValidAPIKey:   info.ValidAPIKey,
					InvalidAPIKey: info.InvalidAPIKey,
					APIKeyMissing: info.APIKeyMissing,
					ClientID:      info.ClientID,
					AccountID:     info.AccountID,
					Endpoint:      r.URL.Path,
					Platform:      platform,
					RateLimited:   info.RateLimited,
					QuotaExceeded: info.QuotaExceeded,
					Duration:      time.Since(start),
					Timestamp:     start.UTC(),
				})
			}
		})
	}
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.':
		default:
			return false
		}
	}
	return true
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}
