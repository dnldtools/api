package http

import (
	"log/slog"
	"net/http"

	"rest-api/internal/auth"
	"rest-api/internal/downloader"
	"rest-api/internal/metrics"
	"rest-api/internal/plans"
	"rest-api/internal/quota"
	"rest-api/internal/ratelimit"
	"rest-api/internal/youtube"
)

type Dependencies struct {
	PrettyJSON bool
	Logger     *slog.Logger
	Downloader *downloader.Service
	Youtube    *youtube.Service

	// PublicBaseURL is used to build absolute URLs in responses (e.g.
	// streaming-proxy links). Empty means derive from the request.
	PublicBaseURL string

	Metrics *metrics.Service

	Auth auth.Authenticator

	Keys auth.KeyManager

	Admin auth.AdminManager

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

func registerAdmin(mux *http.ServeMux, pattern string, h http.HandlerFunc, deps Dependencies, errHandler *ErrorHandler) {
	mux.Handle(pattern, chain(
		h,
		authMiddleware(deps.Auth, errHandler),
		rateLimitMiddleware(deps.RateLimiter, deps.Plans, errHandler),
		requireAdminMiddleware(errHandler),
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

	downloadHandler := handleDownload(deps.Downloader, errHandler, deps.PublicBaseURL)
	protected := chain(
		downloadHandler,
		authMiddleware(deps.Auth, errHandler),
		rateLimitMiddleware(deps.RateLimiter, deps.Plans, errHandler),
		quotaMiddleware(deps.Quota, errHandler),
	)
	mux.Handle("POST /v1/downloads", protected)

	downloadProxyHandler := handleDownloadProxy(deps.Downloader, errHandler)
	downloadProxy := chain(
		downloadProxyHandler,
		authMiddleware(deps.Auth, errHandler),
		rateLimitMiddleware(deps.RateLimiter, deps.Plans, errHandler),
		quotaMiddleware(deps.Quota, errHandler),
	)
	mux.Handle("GET /v1/downloads/proxy", downloadProxy)

	youtubeConvertHandler := handleYouTubeConvert(deps.Youtube, errHandler)
	youtubeConvert := chain(
		youtubeConvertHandler,
		authMiddleware(deps.Auth, errHandler),
		rateLimitMiddleware(deps.RateLimiter, deps.Plans, errHandler),
		quotaMiddleware(deps.Quota, errHandler),
	)
	mux.Handle("POST /v1/youtube/convert", youtubeConvert)

	registerProtected(mux, "GET /v1/youtube/search", handleYouTubeSearch(deps.Youtube, errHandler), deps, errHandler)
	registerProtected(mux, "GET /v1/youtube/formats", handleYouTubeFormats(deps.Youtube, errHandler), deps, errHandler)

	registerProtected(mux, "GET /v1/account", handleAccount(errHandler), deps, errHandler)
	registerProtected(mux, "GET /v1/usage", handleUsage(deps.Quota, deps.Plans, errHandler), deps, errHandler)
	registerProtected(mux, "GET /v1/keys", handleListKeys(deps.Keys, errHandler), deps, errHandler)
	registerProtected(mux, "POST /v1/keys", handleCreateKey(deps.Keys, errHandler), deps, errHandler)
	registerProtected(mux, "POST /v1/keys/{id}/revoke", handleRevokeKey(deps.Keys, errHandler), deps, errHandler)

	registerAdmin(mux, "GET /v1/admin/accounts", handleAdminListAccounts(deps.Admin, errHandler), deps, errHandler)
	registerAdmin(mux, "GET /v1/admin/accounts/{id}", handleAdminGetAccount(deps.Admin, errHandler), deps, errHandler)
	registerAdmin(mux, "PATCH /v1/admin/accounts/{id}", handleAdminUpdateAccount(deps.Admin, errHandler), deps, errHandler)
	registerAdmin(mux, "DELETE /v1/admin/accounts/{id}", handleAdminDisableAccount(deps.Admin, errHandler), deps, errHandler)
	registerAdmin(mux, "GET /v1/admin/keys", handleAdminListKeys(deps.Admin, errHandler), deps, errHandler)
	registerAdmin(mux, "GET /v1/admin/keys/{id}", handleAdminGetKey(deps.Admin, errHandler), deps, errHandler)
	registerAdmin(mux, "POST /v1/admin/keys", handleAdminCreateKey(deps.Admin, errHandler), deps, errHandler)
	registerAdmin(mux, "PATCH /v1/admin/keys/{id}", handleAdminUpdateKey(deps.Admin, errHandler), deps, errHandler)
	registerAdmin(mux, "DELETE /v1/admin/keys/{id}", handleAdminRevokeKey(deps.Admin, errHandler), deps, errHandler)
	registerAdmin(mux, "GET /v1/admin/stats", handleAdminStats(deps.Metrics, deps.Admin, deps.Quota, errHandler), deps, errHandler)

	mux.HandleFunc("GET /favicon.ico", handleFavicon())
	mux.HandleFunc("GET /favicon.png", handleFavicon())

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
