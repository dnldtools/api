package http

import (
	"log/slog"
	"net/http"

	"rest-api/internal/auth"
	"rest-api/internal/downloader"
	"rest-api/internal/health"
	"rest-api/internal/metrics"
	"rest-api/internal/plans"
	"rest-api/internal/quota"
	"rest-api/internal/ratelimit"
)

type Dependencies struct {
	PrettyJSON bool
	Logger     *slog.Logger
	Health     *health.Checker
	Downloader *downloader.Service

	Metrics *metrics.Service

	Auth auth.Authenticator

	Keys auth.KeyManager

	RateLimiter ratelimit.Limiter

	Quota quota.Service

	Plans plans.PolicySet

	ErrorHandler *ErrorHandler
}

func registerProtected(mux *http.ServeMux, pattern string, h http.HandlerFunc, deps Dependencies, errHandler *ErrorHandler) {
	mux.Handle(pattern, chain(
		h,
		authMiddleware(deps.Auth, errHandler),
		rateLimitMiddleware(deps.RateLimiter, deps.Plans, errHandler),
	))
}

func NewRouter(deps Dependencies) http.Handler {
	errHandler := deps.ErrorHandler
	if errHandler == nil {
		errHandler = NewErrorHandler(deps.Logger)
	}
	if deps.Plans == nil {
		deps.Plans = plans.Defaults()
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", handleHealth(deps.Health))

	downloadHandler := handleDownload(deps.Downloader, errHandler)
	protected := chain(
		downloadHandler,
		authMiddleware(deps.Auth, errHandler),
		rateLimitMiddleware(deps.RateLimiter, deps.Plans, errHandler),
		quotaMiddleware(deps.Quota, errHandler),
	)
	mux.Handle("POST /v1/downloads", protected)

	registerProtected(mux, "GET /v1/account", handleAccount(errHandler), deps, errHandler)
	registerProtected(mux, "GET /v1/usage", handleUsage(deps.Quota, deps.Plans, errHandler), deps, errHandler)
	registerProtected(mux, "GET /v1/keys", handleListKeys(deps.Keys, errHandler), deps, errHandler)
	registerProtected(mux, "POST /v1/keys", handleCreateKey(deps.Keys, errHandler), deps, errHandler)
	registerProtected(mux, "POST /v1/keys/{id}/revoke", handleRevokeKey(deps.Keys, errHandler), deps, errHandler)

	mux.HandleFunc("/", handleNotFound(errHandler))

	return chain(
		mux,
		recoverMiddleware(errHandler),
		requestIDMiddleware,
		loggingMiddleware(deps.Logger),
		metricsMiddleware(deps.Metrics),
		contentTypeMiddleware,
		prettyMiddleware(deps.PrettyJSON),
	)
}
