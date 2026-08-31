package browser

import (
	"fmt"
	"time"
)

type ProxyConfig struct {
	Server   string
	Username string
	Password string
}

type LaunchOptions struct {
	Headless       bool
	ExecutablePath string
	UserAgent      string
	Proxy          ProxyConfig
	Timeout        time.Duration
}

type ContextOptions struct {
	UserAgent         string
	Proxy             ProxyConfig
	Timeout           time.Duration
	NavigationTimeout time.Duration
}

type RetryConfig struct {
	MaxAttempts int
	Backoff     time.Duration
}

type Config struct {
	Enabled           bool
	Headless          bool
	ExecutablePath    string
	UserAgent         string
	Proxy             ProxyConfig
	Timeout           time.Duration
	NavigationTimeout time.Duration
	MaxConcurrency    int
	CleanupTimeout    time.Duration
	Retry             RetryConfig
}

func DefaultConfig() Config {
	return Config{
		Enabled:           true,
		Headless:          true,
		Timeout:           30 * time.Second,
		NavigationTimeout: 30 * time.Second,
		MaxConcurrency:    4,
		CleanupTimeout:    5 * time.Second,
		Retry: RetryConfig{
			MaxAttempts: 2,
			Backoff:     200 * time.Millisecond,
		},
	}
}

func (c Config) Validate() error {
	if c.Timeout <= 0 {
		return fmt.Errorf("browser: timeout must be positive")
	}
	if c.NavigationTimeout <= 0 {
		return fmt.Errorf("browser: navigation timeout must be positive")
	}
	if c.MaxConcurrency <= 0 {
		return fmt.Errorf("browser: max concurrency must be positive")
	}
	if c.CleanupTimeout <= 0 {
		return fmt.Errorf("browser: cleanup timeout must be positive")
	}
	if c.Retry.MaxAttempts <= 0 {
		return fmt.Errorf("browser: retry max attempts must be positive")
	}
	if c.Retry.Backoff < 0 {
		return fmt.Errorf("browser: retry backoff must not be negative")
	}
	return nil
}
