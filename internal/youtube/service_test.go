package youtube

import "testing"

func TestResolveFormatByPreset(t *testing.T) {
	tests := []struct {
		preset  string
		typ     string
		format  string
		quality string
	}{
		{"mp3-320", "audio", "mp3", "320kbps"},
		{"mp4-720", "video", "mp4", "720p"},
		{"mp4-2160", "video", "mp4", "2160p"},
		{"flac-best", "audio", "flac", "best"},
		{"mkv-1080", "video", "mkv", "1080p"},
	}

	for _, tc := range tests {
		output, f, err := resolveFormat(ConvertRequest{Preset: tc.preset})
		if err != nil {
			t.Errorf("%s: resolveFormat error = %v", tc.preset, err)
			continue
		}
		if output.Type != tc.typ || output.Format != tc.format || output.Quality != tc.quality {
			t.Errorf("%s: output = %+v, want {%s %s %s}", tc.preset, output, tc.typ, tc.format, tc.quality)
		}
		if f.ID != tc.preset {
			t.Errorf("%s: descriptor id = %q, want %q", tc.preset, f.ID, tc.preset)
		}
	}
}

func TestResolveFormatUnknownPreset(t *testing.T) {
	if _, _, err := resolveFormat(ConvertRequest{Preset: "nope"}); err != ErrFormatUnavailable {
		t.Errorf("unknown preset error = %v, want ErrFormatUnavailable", err)
	}
}

func TestResolveFormatDefaults(t *testing.T) {
	// No fields → audio mp3 320kbps.
	output, f, err := resolveFormat(ConvertRequest{})
	if err != nil {
		t.Fatalf("resolveFormat default error = %v", err)
	}
	if output.Type != "audio" || output.Format != "mp3" || output.Quality != "320kbps" {
		t.Errorf("default output = %+v, want audio/mp3/320kbps", output)
	}
	if f.ID != "mp3-320" {
		t.Errorf("default descriptor id = %q, want mp3-320", f.ID)
	}
}

func TestResolveFormatInfersTypeFromFormat(t *testing.T) {
	output, _, err := resolveFormat(ConvertRequest{Format: "mp4"})
	if err != nil {
		t.Fatalf("resolveFormat(mp4) error = %v", err)
	}
	if output.Type != "video" || output.Quality != "720p" {
		t.Errorf("mp4 output = %+v, want video/mp4/720p", output)
	}
}

func TestResolveFormatRejectsInvalidValues(t *testing.T) {
	cases := []ConvertRequest{
		{Type: "image"},
		{Type: "video", Format: "mp3"},
		{Type: "audio", Format: "mp4"},
		{Type: "video", Format: "mp4", Quality: "999p"},
		{Format: "notarealformat"},
	}
	for _, req := range cases {
		if _, _, err := resolveFormat(req); err != ErrFormatUnavailable {
			t.Errorf("resolveFormat(%+v) error = %v, want ErrFormatUnavailable", req, err)
		}
	}
}

func TestVideoID(t *testing.T) {
	tests := []struct {
		in string
		id string
		ok bool
	}{
		{"https://www.youtube.com/watch?v=jNQXAC9IVRw", "jNQXAC9IVRw", true},
		{"https://www.youtube.com/watch?v=jNQXAC9IVRw&list=PL1", "jNQXAC9IVRw", true},
		{"https://youtu.be/jNQXAC9IVRw", "jNQXAC9IVRw", true},
		{"https://www.youtube.com/shorts/abcDEF123_-", "abcDEF123_-", true},
		{"https://www.youtube.com/embed/jNQXAC9IVRw", "jNQXAC9IVRw", true},
		{"https://www.youtube.com/live/jNQXAC9IVRw", "jNQXAC9IVRw", true},
		{"https://music.youtube.com/watch?v=jNQXAC9IVRw", "jNQXAC9IVRw", true},
		{"jNQXAC9IVRw", "jNQXAC9IVRw", true},
		{"https://www.youtube.com/", "", false},
		{"https://example.com/watch?v=jNQXAC9IVRw", "", false},
		{"", "", false},
		{"too-short", "", false},
	}
	for _, tc := range tests {
		id, ok := videoID(tc.in)
		if ok != tc.ok || id != tc.id {
			t.Errorf("videoID(%q) = (%q, %v), want (%q, %v)", tc.in, id, ok, tc.id, tc.ok)
		}
	}
}

func TestWatchURL(t *testing.T) {
	if got := watchURL("jNQXAC9IVRw"); got != "https://www.youtube.com/watch?v=jNQXAC9IVRw" {
		t.Errorf("watchURL = %q", got)
	}
}

func TestRelay(t *testing.T) {
	hub := "https://hub.convert1s.com"
	tests := []struct {
		in   string
		want string
	}{
		{
			"https://vps-x.shop/api/status/ABC?token=1&expires=2",
			"https://hub.convert1s.com/relay/vps-x.shop/api/status/ABC?token=1&expires=2",
		},
		{
			"https://vps-y.site/files/out.mp4?token=z",
			"https://hub.convert1s.com/relay/vps-y.site/files/out.mp4?token=z",
		},
		{
			"https://other.example.com/api/status/ABC",
			"https://other.example.com/api/status/ABC",
		},
	}
	for _, tc := range tests {
		if got := relay(tc.in, hub); got != tc.want {
			t.Errorf("relay(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWorkerList(t *testing.T) {
	h := healthResponse{}
	h.Servers = []struct {
		Address string `json:"address"`
		Enabled bool   `json:"enabled"`
		Healthy bool   `json:"healthy"`
		Name    string `json:"name"`
	}{
		{Address: "https://a.shop/", Enabled: true, Healthy: true, Name: "a"},
		{Address: "https://b.shop", Enabled: true, Healthy: false, Name: "b"},
		{Address: "https://c.shop", Enabled: false, Healthy: true, Name: "c"},
		{Address: "https://a.shop", Enabled: true, Healthy: true, Name: "a-dup"},
		{Address: "", Enabled: true, Healthy: true, Name: "empty"},
	}

	got := workerList(h)
	if len(got) != 1 || got[0] != "https://a.shop" {
		t.Errorf("workerList = %v, want [https://a.shop]", got)
	}
}

func TestFormatsCatalogHasDefaults(t *testing.T) {
	c := Formats()
	if len(c.Audio) == 0 || len(c.Video) == 0 {
		t.Fatal("catalog is empty")
	}
	if !c.QualityFallback {
		t.Error("quality fallback should be enabled")
	}

	hasDefault := func(list []Format) bool {
		for _, f := range list {
			if f.Default {
				return true
			}
		}
		return false
	}
	if !hasDefault(c.Audio) || !hasDefault(c.Video) {
		t.Error("catalog must mark a default audio and video format")
	}
}
