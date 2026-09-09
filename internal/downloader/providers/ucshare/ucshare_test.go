package ucshare

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rest-api/internal/downloader"
)

const testShare = "https://uc-share.com/s/1aa3cde14a514"

func TestProviderContract(t *testing.T) {
	p := New()
	if p.Name() != "uc-share" || p.Platform() != downloader.PlatformUCShare || p.Type() != downloader.ProviderExternalAPI {
		t.Fatalf("contract mismatch name=%s platform=%s type=%s", p.Name(), p.Platform(), p.Type())
	}
}

func TestMatchesURL(t *testing.T) {
	p := New()
	if !p.MatchesURL(testShare) {
		t.Error("should match uc-share.com")
	}
	if !p.MatchesURL("https://drive.ucweb.com/s/abc") {
		t.Error("should match drive.ucweb.com")
	}
	if p.MatchesURL("1aa3cde14a514") {
		t.Error("bare slug must not auto-claim")
	}
	if p.MatchesURL("https://music.apple.com/x") {
		t.Error("should not match apple")
	}
}

func TestResolveEmptyURL(t *testing.T) {
	_, err := New().Resolve(context.Background(), downloader.DownloadRequest{})
	if !stderrors.Is(err, downloader.ErrInvalidURL) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveShareWithFolder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/share/sharepage/v2/detail"):
			var body []byte
			if r.Body != nil {
				body, _ = ioRead(r)
			}
			if strings.Contains(string(body), "pdir_fid") {
				_, _ = w.Write([]byte(`{"code":0,"data":{"detail_info":{"list":[{"fid":"f1","file_name":"clip.mp4","dir":false,"size":123,"format_type":"video/mp4","obj_category":"video","share_fid_token":"tok1"}]}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"token_info":{"stoken":"ST","title":"Queenbee dan 7 file lainnya"},"detail_info":{"list":[{"fid":"d1","file_name":"Queenbee","dir":true,"share_fid_token":"t"}]}}}`))
		case strings.Contains(r.URL.Path, "/share/sharepage/video_preview"):
			_, _ = w.Write([]byte(`{"code":0,"data":{"duration":12,"preview_url":"https://th.example/p.jpg","play_info":{"url":"https://cdn.example/v.mp4"}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewWithConfig(Config{APIBaseURL: srv.URL})
	res, err := p.Resolve(context.Background(), downloader.DownloadRequest{URL: testShare})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Platform != downloader.PlatformUCShare {
		t.Errorf("platform %q", res.Platform)
	}
	if res.Title != "Queenbee dan 7 file lainnya" {
		t.Errorf("title %q", res.Title)
	}
	if res.Type != downloader.MediaVideo {
		t.Errorf("type %q", res.Type)
	}
	if len(res.Formats) != 1 || res.Formats[0].URL != "https://cdn.example/v.mp4" {
		t.Fatalf("formats %+v", res.Formats)
	}
	if res.Formats[0].Quality != "Queenbee/clip.mp4" {
		t.Errorf("quality %q", res.Formats[0].Quality)
	}
}

func ioRead(r *http.Request) ([]byte, error) {
	buf := make([]byte, 4096)
	n, _ := r.Body.Read(buf)
	return buf[:n], nil
}

func TestResolveUpstreamUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := NewWithConfig(Config{APIBaseURL: srv.URL}).Resolve(context.Background(), downloader.DownloadRequest{URL: testShare})
	if !stderrors.Is(err, downloader.ErrProviderUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
