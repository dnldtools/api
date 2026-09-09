package snapx

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rest-api/internal/downloader"
)

const (
	DefaultBaseURL = "https://api.snapx.info/v1"
	DefaultAppID   = "22120300515132"
	DefaultSecret  = "S7O1qf3ZRyNLYA"
	DefaultUA      = "Ktor client"
	maxBody        = 8 << 20
	tokenTTL       = 600
)

type Client struct {
	base      string
	appID     string
	secret    string
	userAgent string
	client    *http.Client
}

type Config struct {
	BaseURL    string
	AppID      string
	Secret     string
	UserAgent  string
	Timeout    time.Duration
	HTTPClient *http.Client
}

func New() *Client { return NewWithConfig(Config{}) }

func NewWithConfig(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.AppID == "" {
		cfg.AppID = DefaultAppID
	}
	if cfg.Secret == "" {
		cfg.Secret = DefaultSecret
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUA
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 25 * time.Second
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
	}
	return &Client{
		base:      strings.TrimRight(cfg.BaseURL, "/"),
		appID:     cfg.AppID,
		secret:    cfg.Secret,
		userAgent: cfg.UserAgent,
		client:    hc,
	}
}

func (c *Client) Instagram(ctx context.Context, mediaURL string) (*downloader.DownloadResult, error) {
	raw, err := c.get(ctx, "/instagram", mediaURL)
	if err != nil {
		return nil, err
	}
	return parseInstagram(raw, mediaURL)
}

func (c *Client) Facebook(ctx context.Context, mediaURL string) (*downloader.DownloadResult, error) {
	raw, err := c.get(ctx, "/fb", mediaURL)
	if err != nil {
		return nil, err
	}
	return parseFacebook(raw, mediaURL)
}

func (c *Client) TikTok(ctx context.Context, mediaURL string) (*downloader.DownloadResult, error) {
	raw, err := c.get(ctx, "/tiktok", mediaURL)
	if err != nil {
		return nil, err
	}
	return parseTikTok(raw, mediaURL)
}

func (c *Client) get(ctx context.Context, path, mediaURL string) (map[string]any, error) {
	u, err := url.Parse(c.base + path)
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	q := u.Query()
	q.Set("url", mediaURL)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	req.Header.Set("X-App-Id", c.appID)
	req.Header.Set("X-App-Token", createAppToken(c.secret, tokenTTL))
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, classifyClientError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, classifyClientError(err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, downloader.ErrProviderUnavailable
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, downloader.ErrMediaNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, downloader.ErrProviderInvalidResponse
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}
	return raw, nil
}

func createAppToken(secret string, ttl int64) string {
	header := b64url([]byte(`{"alg":"HS256"}`))
	payload := b64url([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Unix()+ttl)))
	unsigned := header + "." + payload
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))
	sig := b64url(mac.Sum(nil))
	return unsigned + "." + sig
}

func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func parseInstagram(raw map[string]any, mediaURL string) (*downloader.DownloadResult, error) {
	data := raw
	if inner, ok := raw["data"].(map[string]any); ok {
		data = inner
	}
	title, _ := data["title"].(string)
	thumb := firstString(data, "display_url", "thumbnail")
	formats := collectIGFormats(data)
	if len(formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	res := &downloader.DownloadResult{
		Platform:  downloader.PlatformInstagram,
		URL:       mediaURL,
		Title:     title,
		Thumbnail: thumb,
		Formats:   formats,
		Metadata:  map[string]string{"source": "snapx"},
	}
	res.Type = sameType(formats)
	if id, _ := data["id"].(string); id != "" {
		res.Metadata["id"] = id
	}
	if sc, _ := data["shortcode"].(string); sc != "" {
		res.Metadata["shortcode"] = sc
	}
	return res, nil
}

func collectIGFormats(node map[string]any) []downloader.Format {
	out := make([]downloader.Format, 0)
	seen := map[string]struct{}{}
	add := func(u string, typ downloader.MediaType, quality string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		if _, ok := seen[u]; ok {
			return
		}
		seen[u] = struct{}{}
		ext := "jpg"
		if typ == downloader.MediaVideo {
			ext = "mp4"
		}
		out = append(out, downloader.Format{Type: typ, URL: u, Quality: quality, Ext: ext})
	}

	var walk func(any, string)
	walk = func(v any, q string) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		if vu, _ := m["video_url"].(string); vu != "" {
			add(vu, downloader.MediaVideo, firstNonEmpty(q, "video"))
		}
		if du, _ := m["display_url"].(string); du != "" && m["video_url"] == nil {
			add(du, downloader.MediaImage, firstNonEmpty(q, "image"))
		}
		if u, _ := m["url"].(string); u != "" {
			typ := downloader.MediaImage
			if looksVideo(u) || strings.Contains(strings.ToLower(fmt.Sprint(m["__type"])), "video") {
				typ = downloader.MediaVideo
			}
			add(u, typ, firstNonEmpty(q, "default"))
		}
		if items, ok := m["items"].([]any); ok {
			if allPlainURLs(items) {
				best := pickLargestImage(items)
				add(best, downloader.MediaImage, "image")
			} else {
				for i, it := range items {
					walk(it, "item"+strconv.Itoa(i+1))
				}
			}
		}
		if media, ok := m["media"].([]any); ok {
			for i, it := range media {
				walk(it, "media"+strconv.Itoa(i+1))
			}
		}
	}
	walk(node, "")
	return out
}

func allPlainURLs(items []any) bool {
	if len(items) == 0 {
		return false
	}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			return false
		}
		if _, has := m["url"]; !has {
			return false
		}
		if m["__type"] != nil || m["video_url"] != nil || m["display_url"] != nil || m["items"] != nil {
			return false
		}
	}
	return true
}

func pickLargestImage(items []any) string {
	var best string
	var area float64
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		u, _ := m["url"].(string)
		w, _ := m["width"].(float64)
		h, _ := m["height"].(float64)
		a := w * h
		if u != "" && a >= area {
			area = a
			best = u
		}
	}
	return best
}

func parseFacebook(raw map[string]any, mediaURL string) (*downloader.DownloadResult, error) {
	if errFlag, _ := raw["error"].(bool); errFlag {
		return nil, downloader.ErrMediaNotFound
	}
	data := raw
	if inner, ok := raw["data"].(map[string]any); ok {
		data = inner
	}
	formats := make([]downloader.Format, 0, 3)
	if hd, _ := data["hd"].(string); hd != "" {
		formats = append(formats, downloader.Format{Type: downloader.MediaVideo, URL: hd, Quality: "hd", Ext: "mp4"})
	}
	if sd, _ := data["sd"].(string); sd != "" {
		formats = append(formats, downloader.Format{Type: downloader.MediaVideo, URL: sd, Quality: "sd", Ext: "mp4"})
	}
	if au, _ := data["audio_url"].(string); au != "" {
		formats = append(formats, downloader.Format{Type: downloader.MediaAudio, URL: au, Quality: "audio", Ext: "mp4"})
	}
	if len(formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	title, _ := data["title"].(string)
	thumb, _ := data["thumbnail"].(string)
	return &downloader.DownloadResult{
		Platform:  downloader.PlatformFacebook,
		URL:       mediaURL,
		Title:     title,
		Type:      downloader.MediaVideo,
		Thumbnail: thumb,
		Formats:   formats,
		Metadata:  map[string]string{"source": "snapx"},
	}, nil
}

func parseTikTok(raw map[string]any, mediaURL string) (*downloader.DownloadResult, error) {
	status, _ := raw["status"].(string)
	formats := make([]downloader.Format, 0, 4)
	add := func(key, quality string, typ downloader.MediaType, ext string) {
		u, _ := raw[key].(string)
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		formats = append(formats, downloader.Format{Type: typ, URL: u, Quality: quality, Ext: ext})
	}
	add("video_link", "cdn", downloader.MediaVideo, "mp4")
	add("original_video_link", "original", downloader.MediaVideo, "mp4")
	add("snapxcdn", "snapxcdn", downloader.MediaVideo, "mp4")
	add("rapidcdn_link", "rapidcdn", downloader.MediaVideo, "mp4")
	add("music", "music", downloader.MediaAudio, "mp3")
	if len(formats) == 0 && status != "100" {
		return nil, downloader.ErrMediaNotFound
	}
	if len(formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	title := firstString(raw, "name", "title")
	thumb := firstString(raw, "thumbnail", "video_thumbnail")
	res := &downloader.DownloadResult{
		Platform:  downloader.PlatformTikTok,
		URL:       mediaURL,
		Title:     title,
		Thumbnail: thumb,
		Formats:   formats,
		Metadata:  map[string]string{"source": "snapx"},
	}
	res.Type = sameType(formats)
	if id := firstString(raw, "aweme_id", "video_id"); id != "" {
		res.Metadata["id"] = id
	}
	return res, nil
}

func sameType(formats []downloader.Format) downloader.MediaType {
	if len(formats) == 0 {
		return ""
	}
	t := formats[0].Type
	for _, f := range formats[1:] {
		if f.Type != t {
			return ""
		}
	}
	return t
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, _ := m[k].(string); strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func looksVideo(u string) bool {
	low := strings.ToLower(u)
	return strings.Contains(low, ".mp4") || strings.Contains(low, "video")
}

func classifyClientError(err error) error {
	if err == nil {
		return downloader.ErrProviderUnavailable
	}
	switch {
	case stderrors.Is(err, context.Canceled):
		return context.Canceled
	case stderrors.Is(err, context.DeadlineExceeded):
		return downloader.ErrProviderTimeout
	}
	var netErr net.Error
	if stderrors.As(err, &netErr) && netErr.Timeout() {
		return downloader.ErrProviderTimeout
	}
	return stderrors.Join(downloader.ErrProviderUnavailable, err)
}
