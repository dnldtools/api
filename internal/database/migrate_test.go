package database

import (
	"io/fs"
	"testing"
)

func TestMigrationsEmbedded(t *testing.T) {
	entries, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		t.Fatalf("list embedded migrations: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one embedded migration file")
	}

	found := false
	for _, name := range entries {
		if name == "migrations/0001_create_api_metrics.sql" {
			found = true
		}
		data, err := migrationsFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Errorf("migration %s is empty", name)
		}
	}
	if !found {
		t.Error("expected migration 0001_create_api_metrics.sql to be embedded")
	}
}

func TestConfigValidate(t *testing.T) {
	valid := Config{URL: "postgres://u:p@localhost/db", MaxConns: 10, MinConns: 1, ConnectTimeout: 1}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid config should pass: %v", err)
	}

	cases := []struct {
		name string
		cfg  Config
	}{
		{"empty url", Config{MaxConns: 10, ConnectTimeout: 1}},
		{"zero max conns", Config{URL: "postgres://u:p@localhost/db", ConnectTimeout: 1}},
		{"negative min conns", Config{URL: "postgres://u:p@localhost/db", MaxConns: 10, MinConns: -1, ConnectTimeout: 1}},
		{"zero connect timeout", Config{URL: "postgres://u:p@localhost/db", MaxConns: 10}},
	}
	for _, c := range cases {
		if err := c.cfg.Validate(); err == nil {
			t.Errorf("%s: expected validation error", c.name)
		}
	}
}
