package youtube

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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

func TestCoverURL(t *testing.T) {
	if got := coverURL("jNQXAC9IVRw"); got != "https://i.ytimg.com/vi/jNQXAC9IVRw/hqdefault.jpg?source=api.dnld.app" {
		t.Errorf("coverURL = %q", got)
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

// mp3FrameBytes builds a valid MPEG-1 Layer III frame header for the given
// bitrate (kbps) and sample rate (Hz), followed by a few bytes of fake frame
// payload so probeMP3 has something to scan.
func mp3FrameBytes(bitrate, sampleRate int) []byte {
	var bitrateIdx int
	for i, v := range []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320} {
		if v == bitrate {
			bitrateIdx = i
			break
		}
	}

	var sampleRateIdx int
	switch sampleRate {
	case 48000:
		sampleRateIdx = 1
	case 32000:
		sampleRateIdx = 2
	default:
		sampleRateIdx = 0 // 44100
	}

	h := uint32(0xFFE00000) // sync
	h |= 3 << 19            // MPEG-1
	h |= 1 << 17            // Layer III
	h |= 1 << 16            // protection bit (no CRC)
	h |= uint32(bitrateIdx) << 12
	h |= uint32(sampleRateIdx) << 10

	out := make([]byte, 4+16)
	binary.BigEndian.PutUint32(out[:4], h)
	return out
}

func TestProbeMP3(t *testing.T) {
	bitrate, sampleRate, ok := probeMP3(mp3FrameBytes(192, 44100))
	if !ok {
		t.Fatal("probeMP3 did not find a frame header")
	}
	if bitrate != 192 {
		t.Errorf("bitrate = %d, want 192", bitrate)
	}
	if sampleRate != 44100 {
		t.Errorf("sampleRate = %d, want 44100", sampleRate)
	}
}

func TestProbeMP3SkipsID3v2(t *testing.T) {
	// 10-byte ID3v2 header + 20-byte tag body, then the MP3 frame.
	tag := []byte{'I', 'D', '3', 0x03, 0x00, 0x00, 0, 0, 0, 20}
	tag = append(tag, make([]byte, 20)...)
	data := append(tag, mp3FrameBytes(128, 44100)...)

	bitrate, _, ok := probeMP3(data)
	if !ok {
		t.Fatal("probeMP3 did not skip the ID3v2 tag")
	}
	if bitrate != 128 {
		t.Errorf("bitrate = %d, want 128", bitrate)
	}
}

func TestProbeMP3RejectsGarbage(t *testing.T) {
	if _, _, ok := probeMP3([]byte("not an mp3 file at all")); ok {
		t.Error("probeMP3 should reject non-MPEG data")
	}
}

func TestParseKbps(t *testing.T) {
	tests := []struct {
		in  string
		out int
		ok  bool
	}{
		{"192kbps", 192, true},
		{" 320KBPS ", 320, true},
		{"320", 320, true},
		{"best", 0, false},
		{"720p", 0, false},
		{"", 0, false},
		{"0kbps", 0, false},
	}
	for _, tc := range tests {
		out, ok := parseKbps(tc.in)
		if ok != tc.ok || out != tc.out {
			t.Errorf("parseKbps(%q) = (%d, %v), want (%d, %v)", tc.in, out, ok, tc.out, tc.ok)
		}
	}
}

func TestQualityNote(t *testing.T) {
	if got := qualityNote("320kbps", "192kbps"); got != "Converted, quality adjusted to 192kbps" {
		t.Errorf("qualityNote = %q", got)
	}
	if got := qualityNote("320kbps", "320kbps"); got != "" {
		t.Errorf("qualityNote(equal) = %q, want empty", got)
	}
}

func TestSanitizeDownloadURL(t *testing.T) {
	in := `https://x.example/files/a.mp3?token=1\u0026expires=2`
	want := "https://x.example/files/a.mp3?token=1&expires=2"
	if got := sanitizeDownloadURL(in); got != want {
		t.Errorf("sanitizeDownloadURL = %q, want %q", got, want)
	}
}

func TestFormatSpecFrom(t *testing.T) {
	f := Format{ID: "mp3-320", Type: "audio", Format: "mp3", Quality: "320kbps"}
	got := formatSpecFrom(f)
	if got.ID != "mp3-320" || got.Type != "audio" || got.Format != "mp3" || got.Quality != "320kbps" {
		t.Errorf("formatSpecFrom = %+v", got)
	}
}

func TestApplyQualityUsesProbeBitrate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(mp3FrameBytes(192, 44100))
	}))
	defer server.Close()

	svc := NewWithConfig(Config{HTTPClient: server.Client(), RequestTimeout: time.Second})
	res := &ConvertResult{DownloadURL: server.URL + "/file.mp3"}
	requested := Format{ID: "mp3-320", Type: "audio", Format: "mp3", Quality: "320kbps"}

	svc.applyQuality(context.Background(), res, requested, completedJob{
		SelectedQuality: "192kbps",
		QualityChanged:  true,
	})

	if res.Output.Quality != "192kbps" {
		t.Errorf("output.quality = %q, want 192kbps", res.Output.Quality)
	}
	if res.Output.BitrateKbps != 192 {
		t.Errorf("output.bitrate_kbps = %d, want 192", res.Output.BitrateKbps)
	}
	if res.Output.ID != "mp3-192" {
		t.Errorf("output.id = %q, want mp3-192", res.Output.ID)
	}
	if !res.QualityChanged {
		t.Error("quality_changed = false, want true")
	}
	if res.QualityNote != "Converted, quality adjusted to 192kbps" {
		t.Errorf("quality_note = %q", res.QualityNote)
	}
}

func TestApplyQualityFallsBackToWorkerSelection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	svc := NewWithConfig(Config{HTTPClient: server.Client(), RequestTimeout: time.Second})
	res := &ConvertResult{DownloadURL: server.URL + "/missing.mp3"}
	requested := Format{ID: "mp3-320", Type: "audio", Format: "mp3", Quality: "320kbps"}

	svc.applyQuality(context.Background(), res, requested, completedJob{SelectedQuality: "128kbps"})

	if res.Output.Quality != "128kbps" {
		t.Errorf("output.quality = %q, want 128kbps", res.Output.Quality)
	}
	if res.Output.BitrateKbps != 128 {
		t.Errorf("output.bitrate_kbps = %d, want 128", res.Output.BitrateKbps)
	}
	if !res.QualityChanged {
		t.Error("quality_changed = false, want true")
	}
}
