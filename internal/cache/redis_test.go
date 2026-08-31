package cache

import "testing"

func TestConfigEnabled(t *testing.T) {
	if (Config{}).Enabled() {
		t.Error("empty config should be disabled")
	}
	if !(Config{Addr: "localhost:6379"}).Enabled() {
		t.Error("config with address should be enabled")
	}
}

func TestConfigValidate(t *testing.T) {
	if err := (Config{Addr: "localhost:6379", PoolSize: 10}).Validate(); err != nil {
		t.Errorf("valid config should pass: %v", err)
	}

	if err := (Config{PoolSize: 10}).Validate(); err == nil {
		t.Error("missing address should fail validation")
	}
	if err := (Config{Addr: "localhost:6379", PoolSize: 0}).Validate(); err == nil {
		t.Error("non-positive pool size should fail validation")
	}
}
