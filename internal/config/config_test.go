package config

import (
	"os"
	"testing"
	"time"
)

func validTestConfig() *Config {
	return &Config{
		AppName:          "rest-api",
		AppEnv:           "test",
		AppVersion:       "0.1.0",
		HTTPHost:         "127.0.0.1",
		HTTPPort:         "8080",
		ReadTimeout:      30 * time.Second,
		WriteTimeout:     60 * time.Second,
		IdleTimeout:      60 * time.Second,
		ShutdownTimeout:  10 * time.Second,
		PrettyJSON:       false,
		LogLevel:         "info",
		MetricsQueueSize: 1024,
	}
}

func TestConfigAddrJoinsHostAndPort(t *testing.T) {
	cfg := validTestConfig()
	if got := cfg.Addr(); got != "127.0.0.1:8080" {
		t.Errorf("Addr() = %q, want 127.0.0.1:8080", got)
	}
}

func TestConfigIsDevelopment(t *testing.T) {
	dev := validTestConfig()
	dev.AppEnv = "development"
	if !dev.IsDevelopment() {
		t.Error("IsDevelopment() = false for development, want true")
	}

	prod := validTestConfig()
	prod.AppEnv = "production"
	if prod.IsDevelopment() {
		t.Error("IsDevelopment() = true for production, want false")
	}
}

func TestConfigValidateAcceptsValid(t *testing.T) {
	if err := validTestConfig().validate(); err != nil {
		t.Fatalf("validate() = %v, want nil", err)
	}
}

func TestConfigValidateRejectsInvalid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty app name", func(c *Config) { c.AppName = "" }},
		{"empty http port", func(c *Config) { c.HTTPPort = "" }},
		{"non-positive shutdown timeout", func(c *Config) { c.ShutdownTimeout = 0 }},
		{"non-positive read timeout", func(c *Config) { c.ReadTimeout = 0 }},
		{"non-positive write timeout", func(c *Config) { c.WriteTimeout = 0 }},
		{"non-positive idle timeout", func(c *Config) { c.IdleTimeout = 0 }},
		{"non-positive metrics queue", func(c *Config) { c.MetricsQueueSize = 0 }},
		{
			"enabled browser with missing executable",
			func(c *Config) {
				c.Browser.Enabled = true
				c.Browser.Timeout = 30 * time.Second
				c.Browser.NavigationTimeout = 30 * time.Second
			},
		},
		{
			"enabled database with empty url",
			func(c *Config) { c.Database.URL = "postgres://u:p@localhost/db" },
		},
		{
			"enabled redis with non-positive pool size",
			func(c *Config) {
				c.Redis.Addr = "localhost:6379"
				c.Redis.PoolSize = 0
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validTestConfig()
			tc.mutate(cfg)
			if err := cfg.validate(); err == nil {
				t.Error("validate() = nil, want error")
			}
		})
	}
}

func TestConfigLoadAppliesDefaults(t *testing.T) {

	t.Setenv("APP_NAME", "rest-api")
	t.Setenv("HTTP_PORT", "9090")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("BROWSER_ENABLED", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.HTTPPort != "9090" {
		t.Errorf("HTTPPort = %q, want 9090", cfg.HTTPPort)
	}
	if cfg.Addr() != "0.0.0.0:9090" {
		t.Errorf("Addr() = %q, want 0.0.0.0:9090", cfg.Addr())
	}
	if cfg.Database.Enabled() {
		t.Error("Database should be disabled when DATABASE_URL is empty")
	}
	if cfg.Redis.Enabled() {
		t.Error("Redis should be disabled when REDIS_ADDR is empty")
	}
	if cfg.Browser.Enabled {
		t.Error("Browser should be disabled by default")
	}
	if cfg.MetricsQueueSize <= 0 {
		t.Error("MetricsQueueSize should have a positive default")
	}
}

func TestConfigLoadRejectsInvalidEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("BROWSER_ENABLED", "false")
	t.Setenv("SHUTDOWN_TIMEOUT", "0s")

	if _, err := Load(); err == nil {
		t.Error("Load() = nil, want error for non-positive shutdown timeout")
	}
}

func TestParseR2AccountsValid(t *testing.T) {
	accounts, err := parseR2Accounts(`[{"name":"r2-01","account_id":"a","access_key":"k","secret_key":"s","bucket":"media"}]`)
	if err != nil {
		t.Fatalf("parseR2Accounts() = %v", err)
	}
	if len(accounts) != 1 || accounts[0].Name != "r2-01" || accounts[0].Bucket != "media" {
		t.Fatalf("unexpected accounts: %+v", accounts)
	}
}

func TestParseR2AccountsEmpty(t *testing.T) {
	accounts, err := parseR2Accounts("")
	if err != nil {
		t.Fatalf("parseR2Accounts() = %v", err)
	}
	if accounts != nil {
		t.Fatalf("parseR2Accounts() = %v, want nil", accounts)
	}
}

func TestParseR2AccountsInvalid(t *testing.T) {
	if _, err := parseR2Accounts(`not-json`); err == nil {
		t.Fatal("parseR2Accounts() = nil, want error")
	}
}

func TestLoadCookieEmpty(t *testing.T) {
	got, err := loadCookie("")
	if err != nil {
		t.Fatalf("loadCookie(\"\") = %v", err)
	}
	if got != "" {
		t.Fatalf("loadCookie(\"\") = %q, want empty", got)
	}
}

func TestLoadCookieBuildsHeader(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/c.json"
	writeTestFile(t, path, `[{"name":"a","value":"1","domain":".x.com"},{"name":"b","value":"2","domain":".x.com"}]`)
	got, err := loadCookie(path)
	if err != nil {
		t.Fatalf("loadCookie() = %v", err)
	}
	if got != "a=1; b=2" {
		t.Fatalf("loadCookie() = %q, want %q", got, "a=1; b=2")
	}
}

func TestLoadCookieSkipsEmptyName(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/c.json"
	writeTestFile(t, path, `[{"name":"","value":"x"},{"name":"a","value":"1"}]`)
	got, err := loadCookie(path)
	if err != nil {
		t.Fatalf("loadCookie() = %v", err)
	}
	if got != "a=1" {
		t.Fatalf("loadCookie() = %q, want %q", got, "a=1")
	}
}

func TestLoadCookieMissingFile(t *testing.T) {
	if _, err := loadCookie("does-not-exist.json"); err == nil {
		t.Fatal("loadCookie() = nil, want error")
	}
}

func TestLoadCookieInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/c.json"
	writeTestFile(t, path, `not-json`)
	if _, err := loadCookie(path); err == nil {
		t.Fatal("loadCookie() = nil, want error")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
