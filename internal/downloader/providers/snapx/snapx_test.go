package snapx

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rest-api/internal/downloader"
)

func TestParseInstagramSingleImage(t *testing.T) {
	raw := map[string]any{
		"status_code": float64(0),
		"data": map[string]any{
			"title":       "",
			"id":          "1",
			"shortcode":   "Dc8t4MmTjVt",
			"display_url": "https://cdn.example/full.jpg",
			"items": []any{
				map[string]any{"url": "https://cdn.example/s.jpg", "width": float64(100), "height": float64(100)},
				map[string]any{"url": "https://cdn.example/l.jpg", "width": float64(1080), "height": float64(1080)},
			},
		},
	}
	res, err := parseInstagram(raw, "https://www.instagram.com/p/Dc8t4MmTjVt")
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != downloader.MediaImage {
		t.Errorf("type %s", res.Type)
	}
	if len(res.Formats) == 0 {
		t.Fatal("no formats")
	}
}

func TestParseFacebook(t *testing.T) {
	raw := map[string]any{
		"error": false,
		"data": map[string]any{
			"title":     "Facebook Video",
			"hd":        "https://cdn.example/hd.mp4",
			"sd":        "https://cdn.example/sd.mp4",
			"audio_url": "https://cdn.example/a.mp4",
			"thumbnail": "https://cdn.example/t.jpg",
		},
	}
	res, err := parseFacebook(raw, "https://facebook.com/x")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Formats) != 3 {
		t.Fatalf("formats %d", len(res.Formats))
	}
}

func TestParseTikTok(t *testing.T) {
	raw := map[string]any{
		"status":              "100",
		"name":                "clip",
		"video_link":          "https://cdn.example/v.mp4",
		"original_video_link": "https://cdn.example/o.mp4",
		"snapxcdn":            "https://cdn.example/s.mp4",
		"music":               "https://cdn.example/m.mp3",
	}
	res, err := parseTikTok(raw, "https://tiktok.com/x")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Formats) != 4 {
		t.Fatalf("formats %d", len(res.Formats))
	}
}

func TestClientHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-App-Id") == "" || r.Header.Get("X-App-Token") == "" {
			t.Error("missing auth headers")
		}
		if !strings.Contains(r.URL.Path, "/tiktok") {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"100","video_link":"https://cdn.example/v.mp4","name":"x"}`))
	}))
	defer srv.Close()
	c := NewWithConfig(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
	res, err := c.TikTok(context.Background(), "https://www.tiktok.com/@a/video/1")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Formats) != 1 {
		t.Fatalf("%+v", res.Formats)
	}
}

func TestTokenShape(t *testing.T) {
	tok := createAppToken(DefaultSecret, 600)
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("parts %d", len(parts))
	}
}

func TestMediaNotFound(t *testing.T) {
	_, err := parseTikTok(map[string]any{"status": "0"}, "https://x")
	if !stderrors.Is(err, downloader.ErrMediaNotFound) {
		t.Fatalf("%v", err)
	}
}
