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
		preset       string
		typ          string
		format       string
		quality      string
		audioBitrate string
		premium      bool
	}{
		{"mp3-320", "audio", "mp3", "", "320k", false},
		{"mp3-64", "audio", "mp3", "", "64k", false},
		{"wav", "audio", "wav", "", "", false},
		{"flac", "audio", "flac", "", "", false},
		{"mp4-720", "video", "mp4", "720p", "", false},
		{"mp4-2160", "video", "mp4", "2160p", "", true},
		{"mp4-1080-premium", "video", "mp4", "1080p", "", true},
	}

	for _, tc := range tests {
		sel, err := resolveFormat(ConvertRequest{Preset: tc.preset})
		if err != nil {
			t.Errorf("%s: resolveFormat error = %v", tc.preset, err)
			continue
		}
		if sel.Output.Type != tc.typ || sel.Output.Format != tc.format || sel.Output.Quality != tc.quality {
			t.Errorf("%s: output = %+v, want {%s %s %s}", tc.preset, sel.Output, tc.typ, tc.format, tc.quality)
		}
		if sel.Format.ID != tc.preset {
			t.Errorf("%s: descriptor id = %q, want %q", tc.preset, sel.Format.ID, tc.preset)
		}
		if got := audioBitrate(sel.Audio); got != tc.audioBitrate {
			t.Errorf("%s: audio.bitrate = %q, want %q", tc.preset, got, tc.audioBitrate)
		}
		if sel.Premium != tc.premium {
			t.Errorf("%s: premium = %v, want %v", tc.preset, sel.Premium, tc.premium)
		}
	}
}

func audioBitrate(a *audioPayload) string {
	if a == nil {
		return ""
	}
	return a.Bitrate
}

func TestResolveFormatUnknownPreset(t *testing.T) {
	if _, err := resolveFormat(ConvertRequest{Preset: "nope"}); err != ErrFormatUnavailable {
		t.Errorf("unknown preset error = %v, want ErrFormatUnavailable", err)
	}
	// Old catalog ids are gone and must not resolve.
	if _, err := resolveFormat(ConvertRequest{Preset: "mp3-256"}); err != ErrFormatUnavailable {
		t.Errorf("removed preset error = %v, want ErrFormatUnavailable", err)
	}
}

func TestResolveFormatRequiresSelection(t *testing.T) {
	// No fields → error, never a silent 320kbps default.
	if _, err := resolveFormat(ConvertRequest{}); err != ErrFormatUnavailable {
		t.Errorf("empty request error = %v, want ErrFormatUnavailable", err)
	}
}

func TestResolveFormatRequiresVideoQuality(t *testing.T) {
	// type=video + format=mp4 without quality must not silently pick a preset.
	if _, err := resolveFormat(ConvertRequest{Type: "video", Format: "mp4"}); err != ErrFormatUnavailable {
		t.Errorf("video without quality error = %v, want ErrFormatUnavailable", err)
	}
}

func TestResolveFormatRejectsInvalidValues(t *testing.T) {
	cases := []ConvertRequest{
		{Type: "image"},
		{Type: "video", Format: "mp3"},
		{Type: "audio", Format: "mp4"},
		{Type: "video", Format: "mp4", Quality: "999p"},
		{Format: "notarealformat"},
		{Type: "audio", Format: "mp3"},                 // mp3 without bitrate
		{Type: "audio", Format: "mp3", Bitrate: "999"}, // unknown bitrate
	}
	for _, req := range cases {
		if _, err := resolveFormat(req); err != ErrFormatUnavailable {
			t.Errorf("resolveFormat(%+v) error = %v, want ErrFormatUnavailable", req, err)
		}
	}
}

func TestResolveFormatFromFields(t *testing.T) {
	tests := []struct {
		req     ConvertRequest
		id      string
		quality string
		bitrate string
		premium bool
	}{
		{ConvertRequest{Type: "audio", Format: "mp3", Bitrate: "128kbps"}, "mp3-128", "", "128k", false},
		{ConvertRequest{Type: "audio", Format: "mp3", Bitrate: "320"}, "mp3-320", "", "320k", false},
		{ConvertRequest{Type: "audio", Format: "wav"}, "wav", "", "", false},
		{ConvertRequest{Type: "video", Format: "mp4", Quality: "720p"}, "mp4-720", "720p", "", false},
		{ConvertRequest{Type: "video", Format: "mp4", Quality: "2160p"}, "mp4-2160", "2160p", "", true},
		{ConvertRequest{Type: "video", Format: "mp4", Quality: "1440"}, "mp4-1440", "1440p", "", true},
	}
	for _, tc := range tests {
		sel, err := resolveFormat(tc.req)
		if err != nil {
			t.Errorf("resolveFormat(%+v) error = %v", tc.req, err)
			continue
		}
		if sel.Format.ID != tc.id {
			t.Errorf("resolveFormat(%+v) id = %q, want %q", tc.req, sel.Format.ID, tc.id)
		}
		if sel.Output.Quality != tc.quality {
			t.Errorf("resolveFormat(%+v) quality = %q, want %q", tc.req, sel.Output.Quality, tc.quality)
		}
		if got := audioBitrate(sel.Audio); got != tc.bitrate {
			t.Errorf("resolveFormat(%+v) bitrate = %q, want %q", tc.req, got, tc.bitrate)
		}
		if sel.Premium != tc.premium {
			t.Errorf("resolveFormat(%+v) premium = %v, want %v", tc.req, sel.Premium, tc.premium)
		}
	}
}

func TestBuildJobV3Payload(t *testing.T) {
	sel, err := resolveFormat(ConvertRequest{Preset: "mp3-320"})
	if err != nil {
		t.Fatal(err)
	}
	job := buildJob("https://www.youtube.com/watch?v=ID", sel, "")
	if job.URL != "https://www.youtube.com/watch?v=ID" || job.OS != "windows" {
		t.Errorf("job url/os = %q/%q", job.URL, job.OS)
	}
	if job.Output.Type != "audio" || job.Output.Format != "mp3" || job.Output.Quality != "" {
		t.Errorf("job output = %+v", job.Output)
	}
	if job.Audio == nil || job.Audio.Bitrate != "320k" {
		t.Errorf("job audio = %+v, want bitrate 320k", job.Audio)
	}
	if job.Premium {
		t.Error("mp3 job must not set premium")
	}

	// Alternate track merges into the same audio object.
	job2 := buildJob("https://www.youtube.com/watch?v=ID", sel, "en")
	if job2.Audio == nil || job2.Audio.TrackID != "en" || job2.Audio.Bitrate != "320k" {
		t.Errorf("track job audio = %+v, want bitrate 320k + trackId en", job2.Audio)
	}

	// Video jobs never carry an audio fragment, even when a track is requested.
	videoSel, err := resolveFormat(ConvertRequest{Preset: "mp4-720"})
	if err != nil {
		t.Fatal(err)
	}
	videoJob := buildJob("https://www.youtube.com/watch?v=ID", videoSel, "en")
	if videoJob.Audio != nil {
		t.Errorf("video job must not carry audio fragment, got %+v", videoJob.Audio)
	}
	if videoJob.Output.Quality != "720p" || videoJob.Output.Type != "video" || videoJob.Output.Format != "mp4" {
		t.Errorf("video job output = %+v", videoJob.Output)
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

func TestFormatsCatalog(t *testing.T) {
	c := Formats()
	if len(c.Audio) == 0 || len(c.Video) == 0 {
		t.Fatal("catalog is empty")
	}
	if !c.QualityFallback {
		t.Error("quality fallback should be enabled")
	}

	wantAudio := []string{"mp3-320", "mp3-192", "mp3-128", "mp3-64", "wav", "m4a", "ogg", "opus", "flac", "aac", "alac"}
	wantVideo := []string{"mp4-2160", "mp4-1440", "mp4-1080-premium", "mp4-1080", "mp4-720", "mp4-480", "mp4-360", "mp4-144"}

	assertIDs := func(got []Format, want []string, name string) {
		if len(got) != len(want) {
			t.Errorf("%s catalog has %d entries, want %d", name, len(got), len(want))
		}
		for i, id := range want {
			if i >= len(got) {
				break
			}
			if got[i].ID != id {
				t.Errorf("%s[%d].id = %q, want %q", name, i, got[i].ID, id)
			}
		}
	}
	assertIDs(c.Audio, wantAudio, "audio")
	assertIDs(c.Video, wantVideo, "video")
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

// wavHeaderBytes builds a minimal RIFF/WAVE header with a PCM fmt chunk.
func wavHeaderBytes(sampleRate, channels, bitsPerSample int) []byte {
	b := make([]byte, 44)
	copy(b[0:4], "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], 36)
	copy(b[8:12], "WAVE")
	copy(b[12:16], "fmt ")
	binary.LittleEndian.PutUint32(b[16:20], 16) // fmt chunk size
	binary.LittleEndian.PutUint16(b[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(b[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(b[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(b[28:32], uint32(sampleRate*channels*bitsPerSample/8))
	binary.LittleEndian.PutUint16(b[32:34], uint16(channels*bitsPerSample/8))
	binary.LittleEndian.PutUint16(b[34:36], uint16(bitsPerSample))
	copy(b[36:40], "data")
	binary.LittleEndian.PutUint32(b[40:44], 0)
	return b
}

// flacStreamInfoBytes builds a "fLaC" magic + STREAMINFO metadata block.
func flacStreamInfoBytes(sampleRate int, totalSamples int64) []byte {
	b := []byte{'f', 'L', 'a', 'C', 0x00, 0x00, 0x00, 0x22} // type 0, 34-byte block
	st := make([]byte, 34)
	st[10] = byte(sampleRate >> 12)
	st[11] = byte(sampleRate >> 4)
	st[12] = byte(sampleRate<<4) & 0xF0
	st[13] = byte(totalSamples>>32) & 0x0F
	st[14] = byte(totalSamples >> 24)
	st[15] = byte(totalSamples >> 16)
	st[16] = byte(totalSamples >> 8)
	st[17] = byte(totalSamples)
	return append(b, st...)
}

func TestProbeWAV(t *testing.T) {
	bitrate, ok := probeWAV(wavHeaderBytes(44100, 2, 16))
	if !ok {
		t.Fatal("probeWAV did not find the fmt chunk")
	}
	if bitrate != 1411 {
		t.Errorf("bitrate = %d, want 1411", bitrate)
	}
}

func TestProbeWAVRejectsGarbage(t *testing.T) {
	if _, ok := probeWAV([]byte("not a wav file")); ok {
		t.Error("probeWAV should reject non-RIFF data")
	}
}

func TestProbeFLAC(t *testing.T) {
	bitrate, ok := probeFLAC(flacStreamInfoBytes(44100, 44100), 100000)
	if !ok {
		t.Fatal("probeFLAC did not parse STREAMINFO")
	}
	if bitrate != 800 {
		t.Errorf("bitrate = %d, want 800", bitrate)
	}
}

func TestProbeFLACRejectsGarbage(t *testing.T) {
	if _, ok := probeFLAC([]byte("not flac"), 1000); ok {
		t.Error("probeFLAC should reject non-FLAC data")
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

func TestApplyQualityWAVReportsBitrate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wavHeaderBytes(44100, 2, 16))
	}))
	defer server.Close()

	svc := NewWithConfig(Config{HTTPClient: server.Client(), RequestTimeout: time.Second})
	res := &ConvertResult{DownloadURL: server.URL + "/file.wav"}
	requested := Format{ID: "wav", Type: "audio", Format: "wav"}

	svc.applyQuality(context.Background(), res, requested, completedJob{})

	if res.Output.BitrateKbps != 1411 {
		t.Errorf("output.bitrate_kbps = %d, want 1411", res.Output.BitrateKbps)
	}
	if res.Output.Quality != "" {
		t.Errorf("output.quality = %q, want empty (WAV has no bitrate quality)", res.Output.Quality)
	}
	if res.Output.ID != "wav" {
		t.Errorf("output.id = %q, want wav", res.Output.ID)
	}
	if res.QualityChanged {
		t.Error("quality_changed = true, want false for WAV")
	}
}
