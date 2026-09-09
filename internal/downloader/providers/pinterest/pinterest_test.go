package pinterest

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rest-api/internal/downloader"
)

func TestContract(t *testing.T) {
	p := New()
	if p.Name() != "pinterest" || p.Platform() != downloader.PlatformPinterest {
		t.Fatalf("%s %s", p.Name(), p.Platform())
	}
}

func TestMatchesURL(t *testing.T) {
	p := New()
	if !p.MatchesURL("https://www.pinterest.com/pin/426082814743401508/") {
		t.Error("pinterest.com")
	}
	if !p.MatchesURL("https://pin.it/3jcyyFs") {
		t.Error("pin.it")
	}
	if p.MatchesURL("https://tiktok.com/x") {
		t.Error("tiktok")
	}
}

func TestResolveEmpty(t *testing.T) {
	_, err := New().Resolve(context.Background(), downloader.DownloadRequest{})
	if !stderrors.Is(err, downloader.ErrInvalidURL) {
		t.Fatalf("%v", err)
	}
}

func TestResolvePinssaver(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pin", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":200,"data":{"type":"image","title":"Flag","username":"chmara19","src":{"orig":"https://i.pinimg.com/originals/dd/02/28/x.png","736x":"https://i.pinimg.com/736x/dd/02/28/x.jpg"}}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewWithConfig(Config{PinssaverURL: srv.URL + "/api/pin", PintsaveURL: srv.URL + "/missing", OfficialBaseURL: srv.URL})
	res, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://www.pinterest.com/pin/426082814743401508/"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != downloader.MediaImage {
		t.Errorf("type %s", res.Type)
	}
	if len(res.Formats) == 0 || !strings.Contains(res.Formats[0].URL, "originals") {
		t.Fatalf("%+v", res.Formats)
	}
	if res.Metadata["source"] != "pinssaver" {
		t.Errorf("source %v", res.Metadata)
	}
}

func TestResolveFallbackPintsave(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pin", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	})
	mux.HandleFunc("/pin/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	})
	mux.HandleFunc("/fetch", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"title":"x","media":[{"url":"https://i.pinimg.com/originals/a.png","type":"image"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewWithConfig(Config{PinssaverURL: srv.URL + "/api/pin", PintsaveURL: srv.URL + "/fetch", OfficialBaseURL: srv.URL})
	res, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://www.pinterest.com/pin/12345678901/"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Metadata["source"] != "pintsave" {
		t.Errorf("%v", res.Metadata)
	}
}

func TestUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "x", http.StatusBadGateway)
	}))
	defer srv.Close()
	p := NewWithConfig(Config{PinssaverURL: srv.URL + "/api/pin", PintsaveURL: srv.URL + "/fetch", OfficialBaseURL: srv.URL})
	_, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: "https://www.pinterest.com/pin/12345678901/"})
	if !stderrors.Is(err, downloader.ErrProviderUnavailable) {
		t.Fatalf("%v", err)
	}
}
