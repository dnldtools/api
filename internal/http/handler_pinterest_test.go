package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/pinterest"
)

func TestDownloadPinterestThroughHandler(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pin" {
			_, _ = w.Write([]byte(`{"status":200,"data":{"title":"Flag","username":"u","src":{"orig":"https://i.pinimg.com/originals/x.png"}}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	reg := downloader.NewRegistry()
	if err := reg.Register(pinterest.NewWithConfig(pinterest.Config{
		PinssaverURL:    upstream.URL + "/api/pin",
		PintsaveURL:     upstream.URL + "/fetch",
		OfficialBaseURL: upstream.URL,
	})); err != nil {
		t.Fatal(err)
	}
	router := newRouterWithService(t, downloader.NewService(reg))
	rec := postDownload(t, router, testAPIKey, `{"platform":"pinterest","url":"https://www.pinterest.com/pin/426082814743401508/"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
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
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if !env.Success || env.Data.Platform != "pinterest" || len(env.Data.Formats) != 1 {
		t.Fatalf("%s", rec.Body.String())
	}
}
