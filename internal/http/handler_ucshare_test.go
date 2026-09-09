package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/ucshare"
)

func TestDownloadUCShareThroughHandler(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(r.URL.Path, "/share/sharepage/v2/detail") && strings.Contains(string(body), "pdir_fid"):
			_, _ = w.Write([]byte(`{"code":0,"data":{"detail_info":{"list":[{"fid":"f1","file_name":"clip.mp4","dir":false,"size":1,"format_type":"video/mp4","obj_category":"video","share_fid_token":"tok"}]}}}`))
		case strings.Contains(r.URL.Path, "/share/sharepage/v2/detail"):
			_, _ = w.Write([]byte(`{"code":0,"data":{"token_info":{"stoken":"ST","title":"Pack"},"detail_info":{"list":[{"fid":"d1","file_name":"Queenbee","dir":true}]}}}`))
		case strings.Contains(r.URL.Path, "/share/sharepage/video_preview"):
			_, _ = w.Write([]byte(`{"code":0,"data":{"play_info":{"url":"https://cdn.example/v.mp4"},"preview_url":"https://th.example/t.jpg"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	reg := downloader.NewRegistry()
	if err := reg.Register(ucshare.NewWithConfig(ucshare.Config{APIBaseURL: upstream.URL})); err != nil {
		t.Fatal(err)
	}
	router := newRouterWithService(t, downloader.NewService(reg))
	rec := postDownload(t, router, testAPIKey, `{"platform":"uc-share","url":"https://uc-share.com/s/1aa3cde14a514"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Platform string `json:"platform"`
			Formats  []struct {
				URL string `json:"url"`
			} `json:"formats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.Success || env.Data.Platform != "uc-share" || len(env.Data.Formats) != 1 {
		t.Fatalf("%+v", env)
	}
}
