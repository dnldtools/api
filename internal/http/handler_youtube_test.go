package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rest-api/internal/downloader"
	"rest-api/internal/ratelimit"
	"rest-api/internal/youtube"
)

func newYouTubeRouter(t *testing.T, upstream http.Handler) http.Handler {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)

	svc := youtube.NewWithConfig(youtube.Config{
		Origin:         "https://convert1s.com",
		Hub:            server.URL,
		Meta:           server.URL,
		RequestTimeout: 5 * time.Second,
		PollTimeout:    5 * time.Second,
		PollInterval:   10 * time.Millisecond,
	})

	registry := downloader.NewRegistry()
	return NewRouter(Dependencies{
		PrettyJSON:  false,
		Logger:      slog.Default(),
		Downloader:  downloader.NewService(registry),
		Youtube:     svc,
		Auth:        &fakeAuthenticator{},
		RateLimiter: ratelimit.NewMemoryLimiter(),
		Quota:       &fakeQuota{},
	})
}

// youtubeUpstream returns a handler emulating the convert1s hub/meta/worker
// endpoints needed by the YouTube service.
func youtubeUpstream(t *testing.T) http.Handler {
	return youtubeUpstreamAt(t, 192)
}

// youtubeUpstreamAt emulates the upstream, serving an MP3 download encoded at
// the given bitrate so quality-change behaviour can be exercised.
func youtubeUpstreamAt(t *testing.T, mp3Bitrate int) http.Handler {
	return newYouTubeUpstream(t, mp3Bitrate, nil)
}

// capturedDownload mirrors the fields of the v3 worker payload that tests
// assert on.
type capturedDownload struct {
	URL    string `json:"url"`
	OS     string `json:"os"`
	Output struct {
		Type    string `json:"type"`
		Format  string `json:"format"`
		Quality string `json:"quality"`
	} `json:"output"`
	Audio *struct {
		Bitrate string `json:"bitrate"`
		TrackID string `json:"trackId"`
	} `json:"audio"`
	Premium bool `json:"premium"`
}

// newYouTubeUpstream emulates the convert1s hub/meta/worker endpoints. When
// capture is non-nil, the decoded /api/download request body is copied into it.
func newYouTubeUpstream(t *testing.T, mp3Bitrate int, capture *capturedDownload) http.Handler {
	t.Helper()
	var serverURL string

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"healthy_count": 1,
			"servers": []map[string]interface{}{
				{"address": serverURL, "enabled": true, "healthy": true, "name": "test-worker"},
			},
		})
	})

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("q") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []map[string]interface{}{
				{
					"type":         "stream",
					"id":           "https://www.youtube.com/watch?v=jNQXAC9IVRw",
					"title":        "Me at the zoo",
					"thumbnailUrl": "https://i.ytimg.com/vi/jNQXAC9IVRw/hqdefault.jpg",
					"uploaderName": "jawed",
					"duration":     19,
					"viewCount":    411546957,
					"uploadDate":   "2005-08-30T00:00Z",
				},
			},
			"nextPageToken": "token-abc",
		})
	})

	mux.HandleFunc("/api/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body capturedDownload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if capture != nil {
			*capture = body
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"statusUrl":        serverURL + "/api/status/job-1",
			"title":            "Me at the zoo",
			"duration":         19,
			"requestedQuality": body.Output.Quality,
			"selectedQuality":  body.Output.Quality,
			"qualityChanged":   false,
		})
	})

	mux.HandleFunc("/api/status/job-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "completed",
			"progress":    100,
			"title":       "Me at the zoo",
			"duration":    19,
			"downloadUrl": serverURL + "/files/job-1/output.mp3?token=abc&expires=123",
		})
	})

	mux.HandleFunc("/files/job-1/output.mp3", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(mp3Frame(mp3Bitrate))
	})

	server := httptest.NewServer(mux)
	serverURL = server.URL
	t.Cleanup(server.Close)
	return mux
}

// mp3Frame returns a minimal MPEG-1 Layer III frame header (44.1 kHz, stereo)
// for the given bitrate, plus a few bytes of fake payload.
func mp3Frame(bitrate int) []byte {
	var header []byte
	switch bitrate {
	case 320:
		header = []byte{0xFF, 0xFB, 0xE0, 0x00}
	default: // 192
		header = []byte{0xFF, 0xFB, 0xB0, 0x00}
	}
	return append(header, make([]byte, 16)...)
}

func doYouTubeRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestYouTubeSearchRequiresQuery(t *testing.T) {
	h := newYouTubeRouter(t, youtubeUpstream(t))

	rec := doYouTubeRequest(t, h, http.MethodGet, "/v1/youtube/search", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := errCode(t, rec.Body.Bytes()); got != "MISSING_PARAMETER" {
		t.Errorf("error.code = %q, want MISSING_PARAMETER", got)
	}
}

func TestYouTubeSearchReturnsResults(t *testing.T) {
	h := newYouTubeRouter(t, youtubeUpstream(t))

	rec := doYouTubeRequest(t, h, http.MethodGet, "/v1/youtube/search?q=me+at+the+zoo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Data struct {
			Items []youtube.SearchItem `json:"items"`
			Next  string               `json:"next_page_token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Data.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(body.Data.Items))
	}
	if body.Data.Items[0].Title != "Me at the zoo" {
		t.Errorf("title = %q, want Me at the zoo", body.Data.Items[0].Title)
	}
	if body.Data.Items[0].Duration != 19 {
		t.Errorf("duration = %d, want 19", body.Data.Items[0].Duration)
	}
	if body.Data.Next != "token-abc" {
		t.Errorf("next_page_token = %q, want token-abc", body.Data.Next)
	}
}

func TestYouTubeFormatsReturnsCatalog(t *testing.T) {
	h := newYouTubeRouter(t, youtubeUpstream(t))

	rec := doYouTubeRequest(t, h, http.MethodGet, "/v1/youtube/formats", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Data youtube.Catalog `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Data.Audio) == 0 || len(body.Data.Video) == 0 {
		t.Error("catalog should include audio and video formats")
	}
}

func TestYouTubeConvertReturnsDownload(t *testing.T) {
	h := newYouTubeRouter(t, youtubeUpstream(t))

	rec := doYouTubeRequest(t, h, http.MethodPost, "/v1/youtube/convert",
		`{"url":"https://www.youtube.com/watch?v=jNQXAC9IVRw","preset":"mp3-320"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Message string                `json:"message"`
		Data    youtube.ConvertResult `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Data.DownloadURL == "" {
		t.Error("download_url should be set")
	}
	if body.Data.Title != "Me at the zoo" {
		t.Errorf("title = %q, want Me at the zoo", body.Data.Title)
	}
	if body.Data.Cover != "https://i.ytimg.com/vi/jNQXAC9IVRw/hqdefault.jpg?source=api.dnld.app" {
		t.Errorf("cover = %q", body.Data.Cover)
	}

	// The fake upstream serves a 192kbps file, so the actual output must never
	// claim 320kbps even though that was requested.
	if body.Data.Requested.Quality != "320kbps" {
		t.Errorf("requested.quality = %q, want 320kbps", body.Data.Requested.Quality)
	}
	if body.Data.Output.Quality != "192kbps" {
		t.Errorf("output.quality = %q, want 192kbps", body.Data.Output.Quality)
	}
	if body.Data.Output.BitrateKbps != 192 {
		t.Errorf("output.bitrate_kbps = %d, want 192", body.Data.Output.BitrateKbps)
	}
	if body.Data.Output.ID != "mp3-192" {
		t.Errorf("output.id = %q, want mp3-192", body.Data.Output.ID)
	}
	if !body.Data.QualityChanged {
		t.Error("quality_changed = false, want true")
	}
	if body.Data.QualityNote != "Converted, quality adjusted to 192kbps" {
		t.Errorf("quality_note = %q", body.Data.QualityNote)
	}
	if body.Message != "Converted, quality adjusted to 192kbps" {
		t.Errorf("message = %q, want quality-adjusted message", body.Message)
	}
}

func TestYouTubeConvertNoQualityChange(t *testing.T) {
	h := newYouTubeRouter(t, youtubeUpstreamAt(t, 320))

	rec := doYouTubeRequest(t, h, http.MethodPost, "/v1/youtube/convert",
		`{"url":"https://www.youtube.com/watch?v=jNQXAC9IVRw","preset":"mp3-320"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Message string                `json:"message"`
		Data    youtube.ConvertResult `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Data.Output.Quality != "320kbps" {
		t.Errorf("output.quality = %q, want 320kbps", body.Data.Output.Quality)
	}
	if body.Data.Output.BitrateKbps != 320 {
		t.Errorf("output.bitrate_kbps = %d, want 320", body.Data.Output.BitrateKbps)
	}
	if body.Data.QualityChanged {
		t.Error("quality_changed = true, want false")
	}
	if body.Message != "OK" {
		t.Errorf("message = %q, want OK", body.Message)
	}
}

func TestYouTubeConvertDownloadURLKeepsAmpersand(t *testing.T) {
	h := newYouTubeRouter(t, youtubeUpstream(t))

	rec := doYouTubeRequest(t, h, http.MethodPost, "/v1/youtube/convert",
		`{"url":"https://www.youtube.com/watch?v=jNQXAC9IVRw","preset":"mp3-320"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	raw := rec.Body.String()
	if strings.Contains(raw, `\u0026`) {
		t.Errorf("response re-encoded ampersand as \\u0026: %s", raw)
	}
	if !strings.Contains(raw, "token=abc&expires=123") {
		t.Errorf("response should contain a literal & in download_url: %s", raw)
	}

	var body struct {
		Data youtube.ConvertResult `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(body.Data.DownloadURL, "token=abc&expires=123") {
		t.Errorf("download_url = %q, want token=abc&expires=123", body.Data.DownloadURL)
	}
}

func TestYouTubeConvertSendsV3Payload(t *testing.T) {
	cases := []struct {
		name        string
		preset      string
		wantType    string
		wantFormat  string
		wantQuality string // "" means output.quality must be omitted
		wantBitrate string // "" means audio.bitrate must be absent
		wantPremium bool
	}{
		{"mp3-320", "mp3-320", "audio", "mp3", "", "320k", false},
		{"wav", "wav", "audio", "wav", "", "", false},
		{"mp4-720", "mp4-720", "video", "mp4", "720p", "", false},
		{"mp4-1080-premium", "mp4-1080-premium", "video", "mp4", "1080p", "", true},
		{"mp4-2160", "mp4-2160", "video", "mp4", "2160p", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cap capturedDownload
			h := newYouTubeRouter(t, newYouTubeUpstream(t, 192, &cap))

			rec := doYouTubeRequest(t, h, http.MethodPost, "/v1/youtube/convert",
				`{"url":"https://www.youtube.com/watch?v=jNQXAC9IVRw","preset":"`+tc.preset+`"}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}

			if cap.URL != "https://www.youtube.com/watch?v=jNQXAC9IVRw" {
				t.Errorf("url = %q", cap.URL)
			}
			if cap.OS != "windows" {
				t.Errorf("os = %q, want windows", cap.OS)
			}
			if cap.Output.Type != tc.wantType || cap.Output.Format != tc.wantFormat || cap.Output.Quality != tc.wantQuality {
				t.Errorf("output = %+v, want {%s %s %q}", cap.Output, tc.wantType, tc.wantFormat, tc.wantQuality)
			}
			if cap.Premium != tc.wantPremium {
				t.Errorf("premium = %v, want %v", cap.Premium, tc.wantPremium)
			}
			var gotBitrate string
			if cap.Audio != nil {
				gotBitrate = cap.Audio.Bitrate
			}
			if gotBitrate != tc.wantBitrate {
				t.Errorf("audio.bitrate = %q, want %q", gotBitrate, tc.wantBitrate)
			}
		})
	}
}

func TestYouTubeConvertSendsTrackID(t *testing.T) {
	var cap capturedDownload
	h := newYouTubeRouter(t, newYouTubeUpstream(t, 192, &cap))

	rec := doYouTubeRequest(t, h, http.MethodPost, "/v1/youtube/convert",
		`{"url":"https://www.youtube.com/watch?v=jNQXAC9IVRw","preset":"mp3-320","track":"en"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if cap.Audio == nil || cap.Audio.TrackID != "en" || cap.Audio.Bitrate != "320k" {
		t.Errorf("audio = %+v, want bitrate 320k + trackId en", cap.Audio)
	}
}

func TestYouTubeConvertValidation(t *testing.T) {
	h := newYouTubeRouter(t, youtubeUpstream(t))

	cases := []struct {
		name string
		body string
		code string
	}{
		{"missing url", `{"preset":"mp3-320"}`, "INVALID_URL"},
		{"no format", `{"url":"https://youtu.be/jNQXAC9IVRw"}`, "FORMAT_NOT_AVAILABLE"},
		{"unknown preset", `{"url":"https://youtu.be/jNQXAC9IVRw","preset":"nope"}`, "FORMAT_NOT_AVAILABLE"},
		{"invalid json", `{`, "INVALID_JSON"},
		{"unknown field", `{"url":"https://youtu.be/jNQXAC9IVRw","extra":1}`, "INVALID_JSON"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doYouTubeRequest(t, h, http.MethodPost, "/v1/youtube/convert", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if got := errCode(t, rec.Body.Bytes()); got != tc.code {
				t.Errorf("error.code = %q, want %q", got, tc.code)
			}
		})
	}
}
