package music

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed scrapers
var scraperFS embed.FS

const scraperEmbedRoot = "scrapers"

const scraperMarker = ".embedded-digest"

func materializeScrapers(cacheDir string) (string, error) {
	if strings.TrimSpace(cacheDir) == "" {
		cacheDir = os.TempDir()
	}
	dest := filepath.Join(cacheDir, "dnld-music-scrapers")
	digest, err := scraperDigest()
	if err != nil {
		return "", err
	}
	if b, err := os.ReadFile(filepath.Join(dest, scraperMarker)); err == nil && strings.TrimSpace(string(b)) == digest {
		return dest, nil
	}
	if err := os.RemoveAll(dest); err != nil {
		return "", fmt.Errorf("music: clear scraper cache: %w", err)
	}
	err = fs.WalkDir(scraperFS, scraperEmbedRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(scraperEmbedRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := scraperFS.ReadFile(path)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".py") {
			mode = 0o755
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, mode)
	})
	if err != nil {
		return "", fmt.Errorf("music: extract scrapers: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dest, scraperMarker), []byte(digest), 0o644); err != nil {
		return "", fmt.Errorf("music: write scraper marker: %w", err)
	}
	return dest, nil
}

func scraperDigest() (string, error) {
	var paths []string
	err := fs.WalkDir(scraperFS, scraperEmbedRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		data, err := scraperFS.ReadFile(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
