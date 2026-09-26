package x

import (
	"testing"

	"rest-api/internal/downloader"
)

func TestMatchesURL(t *testing.T) {
	p := New()
	ok := []string{
		"https://x.com/acdlite/status/974390255393505280",
		"https://twitter.com/acdlite/status/974390255393505280",
		"https://www.x.com/acdlite/status/974390255393505280",
		"https://x.com/acdlite",
	}
	bad := []string{
		"https://youtube.com/watch?v=abc",
		"https://threads.com/@zuck/post/abc",
	}
	for _, u := range ok {
		if !p.MatchesURL(u) {
			t.Fatalf("expected match: %s", u)
		}
	}
	for _, u := range bad {
		if p.MatchesURL(u) {
			t.Fatalf("expected no match: %s", u)
		}
	}
}

func TestResolveInvalidURL(t *testing.T) {
	p := New()
	if _, err := p.Resolve(t.Context(), downloader.DownloadRequest{URL: "https://x.com/acdlite"}); err == nil {
		t.Fatal("expected error for non-status url")
	}
}
