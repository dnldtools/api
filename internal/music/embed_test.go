package music

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMaterializeScrapers(t *testing.T) {
	dir := t.TempDir()
	root, err := materializeScrapers(dir)
	if err != nil {
		t.Fatalf("materializeScrapers: %v", err)
	}
	for _, rel := range []string{
		"amazon/amazon.js",
		"amazon/wvdecrypt.py",
		"amazon/package.json",
		"apple/apple.js",
		"apple/apple_wvdecrypt.py",
		"apple/package.json",
		"soundcloud/soundcloud.js",
		"soundcloud/package.json",
		"tidal/tidal.js",
		"tidal/mp4tags.py",
		"tidal/package.json",
	} {
		p := filepath.Join(root, rel)
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
		if st.Size() == 0 {
			t.Fatalf("empty %s", rel)
		}
	}

	again, err := materializeScrapers(dir)
	if err != nil {
		t.Fatalf("materializeScrapers second call: %v", err)
	}
	if again != root {
		t.Fatalf("root = %q, want %q", again, root)
	}
}

func TestMaterializeScrapersScriptsParse(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	root, err := materializeScrapers(t.TempDir())
	if err != nil {
		t.Fatalf("materializeScrapers: %v", err)
	}
	for _, rel := range []string{
		"amazon/amazon.js",
		"apple/apple.js",
		"soundcloud/soundcloud.js",
		"tidal/tidal.js",
	} {
		cmd := exec.Command("node", "--check", filepath.Join(root, rel))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("node --check %s: %v: %s", rel, err, out)
		}
	}
}

func TestNewFallsBackToEmbeddedScrapers(t *testing.T) {
	svc, err := New(Config{Root: filepath.Join(t.TempDir(), "missing")}, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !svc.Enabled() {
		t.Fatal("Enabled() = false, want true")
	}
	if _, err := os.Stat(filepath.Join(svc.Root(), "soundcloud", "soundcloud.js")); err != nil {
		t.Fatalf("embedded soundcloud scraper missing: %v", err)
	}
}

func TestDevicePathFallsBackToPlatformDir(t *testing.T) {
	svc, err := New(Config{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := filepath.Join(svc.Root(), "amazon", "device.wvd")
	if got := svc.devicePath(PlatformAmazon); got != want {
		t.Fatalf("devicePath = %q, want %q", got, want)
	}
}
