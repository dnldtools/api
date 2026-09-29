package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"rest-api/internal/downloader"
	"rest-api/internal/media"
	"rest-api/internal/r2"
)

var SkipPlatforms = map[downloader.Platform]bool{
	downloader.PlatformYouTube:    true,
	downloader.PlatformUCShare:    true,
	downloader.PlatformSavefrom:   true,
	downloader.Platform9xbuddy:    true,
	downloader.PlatformDoodstream: true,
}

const mirrorUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"

type Uploader struct {
	r2     *r2.Manager
	media  *media.Store
	client *http.Client
	logger *slog.Logger
}

func NewUploader(mgr *r2.Manager, store *media.Store, logger *slog.Logger) *Uploader {
	if mgr == nil {
		return nil
	}
	return &Uploader{
		r2:     mgr,
		media:  store,
		client: &http.Client{Timeout: 60 * time.Second},
		logger: logger,
	}
}

func (u *Uploader) Enabled() bool {
	return u != nil && u.r2 != nil
}

func (u *Uploader) ShouldMirror(p downloader.Platform) bool {
	return u.Enabled() && !SkipPlatforms[p]
}

func (u *Uploader) Mirror(ctx context.Context, svc *downloader.Service, result *downloader.DownloadResult) {
	if !u.ShouldMirror(result.Platform) {
		return
	}
	for i := range result.Formats {
		f := &result.Formats[i]
		if f.URL == "" {
			continue
		}
		if err := u.mirrorFormat(ctx, svc, result.Platform, f, i); err != nil {
			if u.logger != nil {
				u.logger.Warn("r2 mirror failed; keeping direct url", "platform", result.Platform, "error", err)
			}
		}
	}
}

func (u *Uploader) mirrorFormat(ctx context.Context, svc *downloader.Service, platform downloader.Platform, f *downloader.Format, index int) error {
	stream, err := u.fetch(ctx, svc, platform, f.URL)
	if err != nil {
		return err
	}
	defer stream.Body.Close()

	tmp, err := os.CreateTemp("", "r2mirror-*")
	if err != nil {
		return err
	}
	defer tmp.Close()
	defer os.Remove(tmp.Name())

	size, err := io.Copy(tmp, stream.Body)
	if err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}

	sourceURL := f.URL
	key := objectKey(platform, sourceURL, index, f.Ext)
	up, err := u.r2.Upload(ctx, key, tmp, size, stream.ContentType)
	if err != nil {
		return err
	}
	signed, err := u.r2.PresignOn(ctx, up.Account, up.ObjectKey)
	if err != nil {
		return err
	}
	f.URL = signed.URL

	if u.media != nil {
		rec := &media.Record{
			Platform:    string(platform),
			SourceURL:   sourceURL,
			ObjectKey:   up.ObjectKey,
			Bucket:      up.Bucket,
			Account:     up.Account,
			ContentType: stream.ContentType,
			Size:        size,
			Status:      media.StatusReady,
		}
		if err := u.media.Create(ctx, rec); err != nil {
			if u.logger != nil {
				u.logger.Warn("r2 media record failed", "platform", platform, "error", err)
			}
		} else {
			f.MediaID = rec.ID
		}
	}
	return nil
}

func (u *Uploader) fetch(ctx context.Context, svc *downloader.Service, platform downloader.Platform, mediaURL string) (*downloader.MediaStream, error) {
	stream, err := svc.StreamMedia(ctx, platform, mediaURL)
	if err == nil {
		return stream, nil
	}
	if !errors.Is(err, downloader.ErrStreamUnsupported) {
		return nil, err
	}
	return u.genericFetch(ctx, mediaURL)
}

func (u *Uploader) genericFetch(ctx context.Context, mediaURL string) (*downloader.MediaStream, error) {
	if err := guardURL(mediaURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("user-agent", mirrorUserAgent)
	req.Header.Set("accept", "*/*")

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		resp.Body.Close()
		return nil, fmt.Errorf("storage: upstream returned %d", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return &downloader.MediaStream{
		Body:        resp.Body,
		ContentType: contentType,
		Length:      resp.ContentLength,
	}, nil
}

func guardURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("storage: unsupported scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if forbiddenIP(ip) {
			return fmt.Errorf("storage: forbidden host %q", host)
		}
		return nil
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		return err
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && forbiddenIP(ip) {
			return fmt.Errorf("storage: forbidden host %q", host)
		}
	}
	return nil
}

func forbiddenIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

func objectKey(platform downloader.Platform, mediaURL string, index int, ext string) string {
	sum := sha256.Sum256([]byte(mediaURL))
	h := hex.EncodeToString(sum[:8])
	e := strings.TrimPrefix(ext, ".")
	if e == "" {
		e = "bin"
	}
	return fmt.Sprintf("%s/%s-%d.%s", platform, h, index, e)
}
