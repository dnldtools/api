package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"rest-api/internal/auth"
	"rest-api/internal/downloader"
	"rest-api/internal/metrics"
	"rest-api/internal/plans"
	"rest-api/internal/quota"
	"rest-api/internal/ratelimit"
	"rest-api/internal/testutil"
)

func TestFullRequestFlow(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	rdb := testutil.OpenTestRedis(t)
	testutil.Truncate(t, pool)
	testutil.FlushRedis(t, rdb)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	policies, err := plans.LoadFromDB(ctx, pool)
	if err != nil {
		t.Fatalf("load plan policies: %v", err)
	}

	authRepo := auth.NewPostgresRepository(pool)
	accountID, err := authRepo.CreateAccount(ctx, "flow-test-account", auth.RoleUser, "trial")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	rawKey, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if _, err := authRepo.CreateKey(ctx, accountID, "flow-test-key", auth.HashKey(rawKey), nil); err != nil {
		t.Fatalf("create key: %v", err)
	}

	authSvc := auth.NewService(authRepo, nil)
	quotaSvc := quota.NewManager(
		quota.NewPostgresRepository(pool),
		quota.NewRedisCounter(rdb.Client()),
		policies,
		slog.Default(),
		nil,
	)
	rateLimiter := ratelimit.NewRedisLimiter(rdb.Client())
	metricsSvc := metrics.New(
		metrics.NewPostgresRepository(pool),
		metrics.NewRedisAggregator(rdb.Client()),
		slog.Default(),
		32,
	)

	registry := downloader.NewRegistry()
	if err := registry.Register(&stubProvider{
		platform: "stub",
		resolve: func(context.Context, downloader.DownloadRequest) (*downloader.DownloadResult, error) {
			return nil, downloader.ErrNotImplemented
		},
	}); err != nil {
		t.Fatalf("register stub provider: %v", err)
	}
	downloaderSvc := downloader.NewService(registry)

	router := NewRouter(Dependencies{
		PrettyJSON:  false,
		Logger:      slog.Default(),
		Downloader:  downloaderSvc,
		Metrics:     metricsSvc,
		Auth:        authSvc,
		RateLimiter: rateLimiter,
		Quota:       quotaSvc,
		Plans:       policies,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/downloads",
		strings.NewReader(`{"platform":"stub","url":"https://x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", rawKey)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
	var envelope struct {
		Success   bool   `json:"success"`
		RequestID string `json:"request_id"`
		Error     struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Success {
		t.Error("success = true, want false for a 501 response")
	}
	if envelope.Error.Code != "NOT_IMPLEMENTED" {
		t.Errorf("error.code = %q, want NOT_IMPLEMENTED", envelope.Error.Code)
	}
	if envelope.Error.RequestID == "" {
		t.Error("error.request_id should be present in the envelope")
	}
	if rec.Header().Get("X-Request-ID") != envelope.Error.RequestID {
		t.Errorf("X-Request-ID header = %q, envelope error.request_id = %q", rec.Header().Get("X-Request-ID"), envelope.Error.RequestID)
	}

	metricsSvc.Close(ctx)

	var (
		rowRequestID     string
		rowClientID      string
		rowAccountID     int64
		rowStatusCode    int
		rowSuccess       bool
		rowValidKey      bool
		rowRateLimited   bool
		rowQuotaExceeded bool
	)
	err = pool.QueryRow(ctx, `
		SELECT request_id, client_id, account_id, status_code, success,
		       valid_api_key, rate_limited, quota_exceeded
		FROM api_metrics
		WHERE account_id = $1`, accountID,
	).Scan(&rowRequestID, &rowClientID, &rowAccountID, &rowStatusCode,
		&rowSuccess, &rowValidKey, &rowRateLimited, &rowQuotaExceeded)
	if err != nil {
		t.Fatalf("query persisted metric: %v", err)
	}
	if rowRequestID != envelope.Error.RequestID {
		t.Errorf("persisted request_id = %q, want %q", rowRequestID, envelope.Error.RequestID)
	}
	if rowClientID != auth.HashKey(rawKey) {
		t.Errorf("persisted client_id = %q, want hash of raw key", rowClientID)
	}
	if rowAccountID != accountID {
		t.Errorf("persisted account_id = %d, want %d", rowAccountID, accountID)
	}
	if rowStatusCode != http.StatusNotImplemented {
		t.Errorf("persisted status_code = %d, want %d", rowStatusCode, http.StatusNotImplemented)
	}
	if rowSuccess {
		t.Error("persisted success = true, want false for a 501")
	}
	if !rowValidKey {
		t.Error("persisted valid_api_key = false, want true")
	}
	if rowRateLimited || rowQuotaExceeded {
		t.Errorf("persisted rate_limited/quota_exceeded = %v/%v, want false/false", rowRateLimited, rowQuotaExceeded)
	}

	var quotaTotal int64
	err = pool.QueryRow(ctx,
		`SELECT total_requests FROM quota_usage WHERE account_id = $1`, accountID,
	).Scan(&quotaTotal)
	if err != nil {
		t.Fatalf("query quota usage: %v", err)
	}
	if quotaTotal != 1 {
		t.Errorf("quota total_requests = %d, want 1", quotaTotal)
	}

	if got := redisGet(t, rdb.Client(), "metrics:total"); got != "1" {
		t.Errorf("redis metrics:total = %q, want 1", got)
	}
	if got := redisGet(t, rdb.Client(), "metrics:valid_api_key"); got != "1" {
		t.Errorf("redis metrics:valid_api_key = %q, want 1", got)
	}
	if got := redisGet(t, rdb.Client(), "metrics:failed"); got != "1" {
		t.Errorf("redis metrics:failed = %q, want 1 (501 counts as failed)", got)
	}

	daily, monthly := quota.PeriodStarts(time.Now())
	dayKey := "quota:day:" + strconv.FormatInt(accountID, 10) + ":" + daily.Format("20060102")
	monthKey := "quota:month:" + strconv.FormatInt(accountID, 10) + ":" + monthly.Format("200601")
	if got := redisGet(t, rdb.Client(), dayKey); got != "1" {
		t.Errorf("redis %s = %q, want 1", dayKey, got)
	}
	if got := redisGet(t, rdb.Client(), monthKey); got != "1" {
		t.Errorf("redis %s = %q, want 1", monthKey, got)
	}
}

func redisGet(t *testing.T, client redis.Cmdable, key string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	v, err := client.Get(ctx, key).Result()
	if err != nil {
		return ""
	}
	return v
}
