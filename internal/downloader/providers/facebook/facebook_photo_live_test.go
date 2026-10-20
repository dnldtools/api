package facebook

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"rest-api/internal/downloader"
)

func TestLivePhotoSingle(t *testing.T) {
	SkipIfNoCookie(t)
	p := NewLiveProvider(t)
	result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://web.facebook.com/share/p/1HWupQb9PM/"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Type != downloader.MediaImage {
		t.Errorf("Type = %q, want image", result.Type)
	}
	t.Logf("Formats: %d", len(result.Formats))
	for i, f := range result.Formats {
		t.Logf("  [%d] type=%s quality=%s ext=%s url=%s", i, f.Type, f.Quality, f.Ext, f.URL[:80])
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(b))
}

func TestLivePhotoCarousel(t *testing.T) {
	SkipIfNoCookie(t)
	p := NewLiveProvider(t)
	for _, u := range []string{
		"https://web.facebook.com/share/p/19SiQpBubt/",
		"https://web.facebook.com/share/p/19DUUteUAR/",
	} {
		result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: u})
		if err != nil {
			t.Errorf("Resolve(%s) error = %v", u, err)
			continue
		}
		if result.Type != downloader.MediaImage {
			t.Errorf("Type = %q, want image", result.Type)
		}
		t.Logf("%s → %d formats", u, len(result.Formats))
		for i, f := range result.Formats {
			t.Logf("  [%d] type=%s quality=%s url=%s", i, f.Type, f.Quality, f.URL[:80])
		}
		time.Sleep(4 * time.Second)
	}
}

func SkipIfNoCookie(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("D:/dnld.app/rest-api/cookie/facebook.json"); err != nil {
		t.Skip("facebook.json cookie file not found")
	}
}

func NewLiveProvider(t *testing.T) *Provider {
	t.Helper()
	ck, err := os.ReadFile("D:/dnld.app/rest-api/cookie/facebook.json")
	if err != nil {
		t.Skipf("cannot read cookie file: %v", err)
	}
	cfg := DefaultConfig()
	cfg.FacebookCookie = string(ck)
	cook := parseCookieJSON(t, string(ck))
	if cook != "" {
		cfg.FacebookCookie = cook
	}
	return NewWithConfig(cfg)
}

func parseCookieJSON(t *testing.T, raw string) string {
	t.Helper()
	var entries []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return raw
	}
	var pairs []string
	for _, e := range entries {
		if e.Name != "" {
			pairs = append(pairs, e.Name+"="+e.Value)
		}
	}
	if len(pairs) == 0 {
		return raw
	}
	return joinStrings(pairs, "; ")
}

func joinStrings(ss []string, sep string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}