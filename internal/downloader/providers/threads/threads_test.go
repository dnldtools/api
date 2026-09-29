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

func TestMediaFromNodeLayouts(t *testing.T) {
	img := func(u string) map[string]any {
		return map[string]any{"image_versions2": map[string]any{"candidates": []any{
			map[string]any{"url": u, "width": float64(1620), "height": float64(1620)},
		}}}
	}
	vid := func(u string) map[string]any {
		return map[string]any{"video_versions": []any{
			map[string]any{"url": u, "width": float64(1080), "height": float64(1920)},
		}}
	}

	cases := []struct {
		name string
		node map[string]any
		want []mediaItem
	}{
		{
			name: "single image",
			node: img("https://cdn.example/a.jpg"),
			want: []mediaItem{{kind: "photo", url: "https://cdn.example/a.jpg"}},
		},
		{
			name: "single video with thumbnail",
			node: map[string]any{
				"video_versions":  []any{map[string]any{"url": "https://cdn.example/v.mp4", "width": float64(1080), "height": float64(1920)}},
				"image_versions2": map[string]any{"candidates": []any{map[string]any{"url": "https://cdn.example/t.jpg", "width": float64(720), "height": float64(1280)}}},
			},
			want: []mediaItem{{kind: "video", url: "https://cdn.example/v.mp4"}},
		},
		{
			name: "image carousel",
			node: map[string]any{"carousel_media": []any{
				img("https://cdn.example/a.jpg"),
				img("https://cdn.example/b.jpg"),
			}},
			want: []mediaItem{{kind: "photo", url: "https://cdn.example/a.jpg"}, {kind: "photo", url: "https://cdn.example/b.jpg"}},
		},
		{
			name: "video carousel",
			node: map[string]any{"carousel_media": []any{
				vid("https://cdn.example/v1.mp4"),
				vid("https://cdn.example/v2.mp4"),
			}},
			want: []mediaItem{{kind: "video", url: "https://cdn.example/v1.mp4"}, {kind: "video", url: "https://cdn.example/v2.mp4"}},
		},
		{
			name: "mixed carousel",
			node: map[string]any{"carousel_media": []any{
				img("https://cdn.example/a.jpg"),
				vid("https://cdn.example/v1.mp4"),
				img("https://cdn.example/b.jpg"),
			}},
			want: []mediaItem{{kind: "photo", url: "https://cdn.example/a.jpg"}, {kind: "video", url: "https://cdn.example/v1.mp4"}, {kind: "photo", url: "https://cdn.example/b.jpg"}},
		},
		{
			name: "text post no media",
			node: map[string]any{"image_versions2": map[string]any{"candidates": []any{}}, "video_versions": nil},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mediaFromNode(tc.node)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (%+v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("item %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
