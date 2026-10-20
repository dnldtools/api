package pinterest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"rest-api/internal/downloader"
)

func SkipIfNoPinCookie(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("D:/dnld.app/rest-api/cookie/pinterest.json"); err != nil {
		t.Skip("pinterest.json cookie file not found")
	}
}

func NewLivePinProvider(t *testing.T) *Provider {
	t.Helper()
	raw, err := os.ReadFile("D:/dnld.app/rest-api/cookie/pinterest.json")
	if err != nil {
		t.Skipf("cannot read cookie file: %v", err)
	}
	var entries []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	cook := ""
	if err := json.Unmarshal(raw, &entries); err == nil {
		var pairs []string
		for _, e := range entries {
			if e.Name != "" {
				pairs = append(pairs, e.Name+"="+e.Value)
			}
		}
		if len(pairs) > 0 {
			cook = joinSemicolon(pairs)
		}
	}
	cfg := DefaultConfig()
	cfg.PinterestCookie = cook
	return NewWithConfig(cfg)
}

func joinSemicolon(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += "; "
		}
		out += s
	}
	return out
}

func TestLivePin(t *testing.T) {
	SkipIfNoPinCookie(t)
	p := NewLivePinProvider(t)
	cases := []struct {
		label string
		url   string
	}{
		{"VIDEO", "https://id.pinterest.com/pin/909164243548086053/"},
		{"GIF", "https://id.pinterest.com/pin/811633164124318427/"},
		{"IMAGE", "https://id.pinterest.com/pin/1970393584797216/"},
	}
	for _, c := range cases {
		result, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: c.url})
		if err != nil {
			t.Errorf("Resolve(%s) error = %v", c.label, err)
			continue
		}
		t.Logf("%s → type=%s formats=%d", c.label, result.Type, len(result.Formats))
		for i, f := range result.Formats {
			t.Logf("  [%d] type=%s quality=%s ext=%s url=%s", i, f.Type, f.Quality, f.Ext, safeSlice(f.URL, 80))
		}
		b, _ := json.MarshalIndent(result.Metadata, "", "  ")
		t.Logf("  meta: %s", string(b))
		if result.Title != "" {
			t.Logf("  title: %s", result.Title)
		}
		time.Sleep(1 * time.Second)
	}
	fmt.Println("done")
}

func safeSlice(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}