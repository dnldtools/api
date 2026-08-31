package metrics

import (
	"net/http"
	"time"
)

const SuccessStatusCode = http.StatusOK

func IsSuccess(code int) bool { return code == SuccessStatusCode }

type Event struct {
	RequestID     string
	StatusCode    int
	Success       bool
	Failed        bool
	ValidAPIKey   bool
	InvalidAPIKey bool
	APIKeyMissing bool
	ClientID      string
	AccountID     int64
	Endpoint      string
	Platform      string
	RateLimited   bool
	QuotaExceeded bool
	Duration      time.Duration
	Timestamp     time.Time
}

func (e *Event) Normalize() {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	} else {
		e.Timestamp = e.Timestamp.UTC()
	}
	e.Success = IsSuccess(e.StatusCode)
	e.Failed = !e.Success
}

func (e Event) DurationMilliseconds() int64 { return e.Duration.Milliseconds() }

type Stats struct {
	TotalRequests      int64   `json:"total_requests"`
	SuccessCount       int64   `json:"success_count"`
	ErrorCount         int64   `json:"error_count"`
	SuccessRate        float64 `json:"success_rate"`
	ValidAPIKeyCount   int64   `json:"valid_api_key_count"`
	InvalidAPIKeyCount int64   `json:"invalid_api_key_count"`
	MissingAPIKeyCount int64   `json:"missing_api_key_count"`
	RateLimitedCount   int64   `json:"rate_limited_count"`
	QuotaExceededCount int64   `json:"quota_exceeded_count"`
	AvgDurationMs      float64 `json:"avg_duration_ms"`
}

// EndpointStat is a per-path request count for admin stats.
type EndpointStat struct {
	Endpoint string `json:"endpoint"`
	Requests int64  `json:"requests"`
}

// PlatformStat is a per-platform request count for admin stats.
type PlatformStat struct {
	Platform string `json:"platform"`
	Requests int64  `json:"requests"`
}

func SuccessRate(success, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(success) / float64(total)
}
