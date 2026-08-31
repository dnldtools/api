package env

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadParsesDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	content := "" +
		"APP_NAME=rest-api\n" +
		"# full line comment\n" +
		"PRETTY_JSON=true\n" +
		"QUOTED=\"hello\"\n" +
		"INLINE=value # trailing comment\n"

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"APP_NAME", "PRETTY_JSON", "QUOTED", "INLINE"} {
		os.Unsetenv(key)
		t.Cleanup(func() { os.Unsetenv(key) })
	}

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := Get("APP_NAME", ""); got != "rest-api" {
		t.Errorf("APP_NAME = %q, want %q", got, "rest-api")
	}
	if got := GetBool("PRETTY_JSON", false); got != true {
		t.Errorf("PRETTY_JSON = %v, want true", got)
	}
	if got := Get("QUOTED", ""); got != "hello" {
		t.Errorf("QUOTED = %q, want %q", got, "hello")
	}
	if got := Get("INLINE", ""); got != "value" {
		t.Errorf("INLINE = %q, want %q", got, "value")
	}
}

func TestLoadDoesNotOverrideExistingEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	if err := os.WriteFile(path, []byte("APP_NAME=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	os.Setenv("APP_NAME", "from-env")
	t.Cleanup(func() { os.Unsetenv("APP_NAME") })

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := Get("APP_NAME", ""); got != "from-env" {
		t.Errorf("APP_NAME = %q, want %q (existing env must win)", got, "from-env")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "does-not-exist.env")); err != nil {
		t.Fatalf("Load() on missing file should not error, got %v", err)
	}
}
