package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// CacheKey identifies a resolved result by platform and (trimmed) source URL.
// The platform is the *resolved* platform so that an explicit platform hint and
// auto-detection share the same cache entry.
type CacheKey struct {
	Platform Platform
	URL      string
}

func (k CacheKey) String() string {
	platform := string(k.Platform)
	if platform == "" {
		platform = "auto"
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(k.URL)))
	return "resolve:" + platform + ":" + hex.EncodeToString(sum[:16])
}

// ResultCache stores successfully resolved DownloadResults. Implementations must
// return a fresh copy on every Get (no shared mutable state) and must never fail
// the caller on a cache error: a miss is always a valid result.
type ResultCache interface {
	Get(ctx context.Context, key CacheKey) (*DownloadResult, bool)
	Set(ctx context.Context, key CacheKey, res *DownloadResult, ttl time.Duration) error
}

// RedisResultCache is a JSON-backed ResultCache backed by Redis. Cache errors
// are treated as misses (Get) or ignored by the service (Set).
type RedisResultCache struct {
	client redis.Cmdable
}

func NewRedisResultCache(client redis.Cmdable) *RedisResultCache {
	return &RedisResultCache{client: client}
}

func (c *RedisResultCache) Get(ctx context.Context, key CacheKey) (*DownloadResult, bool) {
	if c == nil || c.client == nil {
		return nil, false
	}
	raw, err := c.client.Get(ctx, key.String()).Bytes()
	if err != nil {
		return nil, false
	}
	var res DownloadResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, false
	}
	return &res, true
}

func (c *RedisResultCache) Set(ctx context.Context, key CacheKey, res *DownloadResult, ttl time.Duration) error {
	if c == nil || c.client == nil || res == nil || ttl <= 0 {
		return nil
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, key.String(), raw, ttl).Err()
}
