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
		var body struct {
			URL    string `json:"url"`
			Output struct {
				Type    string `json:"type"`
				Format  string `json:"format"`
				Quality string `json:"quality"`
			} `json:"output"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
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
			"downloadUrl": serverURL + "/files/job-1/output.mp3",
		})
	})

	server := httptest.NewServer(mux)
	serverURL = server.URL
	t.Cleanup(server.Close)
	return mux
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
		Data youtube.ConvertResult `json:"data"`
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
	if body.Data.Output.ID != "mp3-320" {
		t.Errorf("output.id = %q, want mp3-320", body.Data.Output.ID)
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
