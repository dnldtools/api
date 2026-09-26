package threads

import "testing"

func TestMatchesURL(t *testing.T) {
	p := New()
	ok := []string{
		"https://www.threads.com/@zuck/post/DakyAavlKLZ",
		"https://threads.net/@zuck/post/DakyAavlKLZ",
		"https://www.threads.net/@zuck/post/DakyAavlKLZ",
		"https://threads.com/@zuck",
	}
	bad := []string{
		"https://youtube.com/watch?v=abc",
		"https://x.com/zuck/status/1",
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

func TestShortcodeToID(t *testing.T) {
	if got := shortcodeToID("CuyKxkYPJiT"); got == "" {
		t.Fatal("expected numeric id")
	}
	if got := shortcodeToID("bad*code"); got != "" {
		t.Fatalf("expected empty for invalid shortcode, got %q", got)
	}
}
