package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"rest-api/internal/browser"
	"rest-api/internal/cache"
	"rest-api/internal/database"
	"rest-api/internal/music"
	"rest-api/internal/r2"
	"rest-api/pkg/env"
)

type Config struct {
	AppName    string
	AppEnv     string
	AppVersion string

	HTTPHost        string
	HTTPPort        string
	PublicBaseURL   string
	InstagramCookie string
	FacebookCookie  string
	TikTokCookie    string
	PinterestCookie string
	ThreadsCookie   string

	DocsAPIKey string

	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration

	ResolveTimeout time.Duration

	ResolveCacheTTL time.Duration

	PrettyJSON bool
	LogLevel   string

	Browser browser.Config

	Database database.Config

	Redis cache.Config

	MetricsQueueSize int

	R2 r2.Config

	Music music.Config
	MusicEnabled bool
}

func Load() (*Config, error) {

	_ = env.Load(".env")

	instagramCookie, err := loadCookie(env.Get("INSTAGRAM_COOKIE", ""))
	if err != nil {
		return nil, fmt.Errorf("INSTAGRAM_COOKIE: %w", err)
	}
	facebookCookie, err := loadCookie(env.Get("FACEBOOK_COOKIE", ""))
	if err != nil {
		return nil, fmt.Errorf("FACEBOOK_COOKIE: %w", err)
	}
	tiktokCookie, err := loadCookie(env.Get("TIKTOK_COOKIE", ""))
	if err != nil {
		return nil, fmt.Errorf("TIKTOK_COOKIE: %w", err)
	}
	pinterestCookie, err := loadCookie(env.Get("PINTEREST_COOKIE", ""))
	if err != nil {
		return nil, fmt.Errorf("PINTEREST_COOKIE: %w", err)
	}
	threadsCookie, err := loadCookie(env.Get("THREADS_COOKIE", ""))
	if err != nil {
		return nil, fmt.Errorf("THREADS_COOKIE: %w", err)
	}

	cfg := &Config{
		AppName:         env.Get("APP_NAME", "rest-api"),
		AppEnv:          env.Get("APP_ENV", "development"),
		AppVersion:      env.Get("APP_VERSION", "0.1.0"),
		HTTPHost:        env.Get("HTTP_HOST", "0.0.0.0"),
		HTTPPort:        env.Get("HTTP_PORT", "8080"),
		PublicBaseURL:   env.Get("PUBLIC_BASE_URL", ""),
		InstagramCookie: instagramCookie,
		FacebookCookie:  facebookCookie,
		TikTokCookie:    tiktokCookie,
		PinterestCookie: pinterestCookie,
		ThreadsCookie:   threadsCookie,
		DocsAPIKey:      env.Get("DOCS_API_KEY", ""),
		ReadTimeout:     env.GetDuration("HTTP_READ_TIMEOUT", 30*time.Second),
		WriteTimeout:    env.GetDuration("HTTP_WRITE_TIMEOUT", 60*time.Second),
		IdleTimeout:     env.GetDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		PrettyJSON:      env.GetBool("PRETTY_JSON", false),
		LogLevel:        env.Get("LOG_LEVEL", "info"),
		ShutdownTimeout: env.GetDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
		ResolveTimeout:  env.GetDuration("RESOLVE_TIMEOUT", 45*time.Second),
		ResolveCacheTTL: env.GetDuration("RESOLVE_CACHE_TTL", 10*time.Minute),

		Browser: browser.Config{
			Enabled:        env.GetBool("BROWSER_ENABLED", false),
			Headless:       env.GetBool("BROWSER_HEADLESS", true),
			ExecutablePath: env.Get("BROWSER_EXECUTABLE_PATH", ""),
			UserAgent:      env.Get("BROWSER_USER_AGENT", ""),
			Proxy: browser.ProxyConfig{
				Server:   env.Get("BROWSER_PROXY_SERVER", ""),
				Username: env.Get("BROWSER_PROXY_USERNAME", ""),
				Password: env.Get("BROWSER_PROXY_PASSWORD", ""),
			},
			Timeout:           env.GetDuration("BROWSER_TIMEOUT", 30*time.Second),
			NavigationTimeout: env.GetDuration("BROWSER_NAVIGATION_TIMEOUT", 30*time.Second),
			MaxConcurrency:    env.GetInt("BROWSER_MAX_CONCURRENCY", 4),
			CleanupTimeout:    env.GetDuration("BROWSER_CLEANUP_TIMEOUT", 5*time.Second),
			Retry: browser.RetryConfig{
				MaxAttempts: env.GetInt("BROWSER_RETRY_MAX_ATTEMPTS", 2),
				Backoff:     env.GetDuration("BROWSER_RETRY_BACKOFF", 200*time.Millisecond),
			},
		},

		Database: database.Config{
			URL:            env.Get("DATABASE_URL", ""),
			MaxConns:       int32(env.GetInt("DATABASE_MAX_CONNS", 10)),
			MinConns:       int32(env.GetInt("DATABASE_MIN_CONNS", 1)),
			ConnectTimeout: env.GetDuration("DATABASE_CONNECT_TIMEOUT", 10*time.Second),
		},

		Redis: cache.Config{
			Addr:     env.Get("REDIS_ADDR", ""),
			Password: env.Get("REDIS_PASSWORD", ""),
			DB:       env.GetInt("REDIS_DB", 0),
			PoolSize: env.GetInt("REDIS_POOL_SIZE", 10),
		},

		MetricsQueueSize: env.GetInt("METRICS_QUEUE_SIZE", 1024),
	}

	accounts, err := parseR2Accounts(env.Get("R2_ACCOUNTS", ""))
	if err != nil {
		return nil, fmt.Errorf("R2_ACCOUNTS: %w", err)
	}
	cfg.R2 = r2.Config{
		Accounts:   accounts,
		PresignTTL: env.GetDuration("R2_PRESIGN_TTL", 5*time.Minute),
	}

	cfg.Music = music.Config{
		Root:             env.Get("MUSIC_ROOT", ""),
		DeviceWVD:        env.Get("MUSIC_DEVICE_WVD", ""),
		AmazonCookie:     env.Get("AMAZON_COOKIE", ""),
		AppleCookie:      env.Get("APPLE_COOKIE", ""),
		SoundCloudCookie: env.Get("SOUNDCLOUD_COOKIE", ""),
		TidalToken:       env.Get("TIDAL_COOKIE", ""),
		TempDir:          env.Get("MUSIC_TEMP_DIR", ""),
		Timeout:          env.GetDuration("MUSIC_TIMEOUT", 15*time.Minute),
		ResolveTimeout:   env.GetDuration("MUSIC_RESOLVE_TIMEOUT", 90*time.Second),
		TidalQuality:     env.Get("TIDAL_QUALITY", "LOSSLESS"),
	}
	cfg.MusicEnabled = env.GetBool("MUSIC_ENABLED", true)

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) Addr() string {
	return net.JoinHostPort(c.HTTPHost, c.HTTPPort)
}

func (c *Config) IsDevelopment() bool {
	return c.AppEnv == "development"
}

func (c *Config) validate() error {
	if c.AppName == "" {
		return fmt.Errorf("APP_NAME must not be empty")
	}
	if c.HTTPPort == "" {
		return fmt.Errorf("HTTP_PORT must not be empty")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("SHUTDOWN_TIMEOUT must be positive")
	}
	if c.ReadTimeout <= 0 {
		return fmt.Errorf("HTTP_READ_TIMEOUT must be positive")
	}
	if c.WriteTimeout <= 0 {
		return fmt.Errorf("HTTP_WRITE_TIMEOUT must be positive")
	}
	if c.IdleTimeout <= 0 {
		return fmt.Errorf("HTTP_IDLE_TIMEOUT must be positive")
	}
	if c.Browser.Enabled {
		if err := c.Browser.Validate(); err != nil {
			return fmt.Errorf("browser config: %w", err)
		}
	}
	if c.Database.Enabled() {
		if err := c.Database.Validate(); err != nil {
			return fmt.Errorf("database config: %w", err)
		}
	}
	if c.Redis.Enabled() {
		if err := c.Redis.Validate(); err != nil {
			return fmt.Errorf("redis config: %w", err)
		}
	}
	if c.MetricsQueueSize <= 0 {
		return fmt.Errorf("METRICS_QUEUE_SIZE must be positive")
	}
	return nil
}

func parseR2Accounts(raw string) ([]r2.Account, error) {
	if raw == "" {
		return nil, nil
	}
	var accounts []r2.Account
	if err := json.Unmarshal([]byte(raw), &accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

func loadCookie(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cookie file %q: %w", path, err)
	}
	var entries []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return "", fmt.Errorf("cookie file %q: %w", path, err)
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		parts = append(parts, e.Name+"="+e.Value)
	}
	return strings.Join(parts, "; "), nil
}
