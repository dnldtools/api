package x

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"rest-api/internal/downloader"
)

const (
	defaultAPIBase = "https://api.fxtwitter.com"
	defaultUA      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
	maxBody        = 2 << 20
)

var statusRe = regexp.MustCompile(`(?i)(?:twitter|x)\.com/([A-Za-z0-9_]+)/status/([0-9]+)`)

type Config struct {
	BaseURL    string
	UserAgent  string
	Timeout    time.Duration
	HTTPClient *http.Client
}

type Provider struct {
	base      string
	userAgent string
	client    *http.Client
}

var _ downloader.Provider = (*Provider)(nil)

var _ downloader.URLMatcher = (*Provider)(nil)

func New() *Provider { return NewWithConfig(Config{}) }

func NewWithConfig(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultAPIBase
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUA
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
	}
	return &Provider{base: strings.TrimRight(cfg.BaseURL, "/"), userAgent: cfg.UserAgent, client: hc}
}

func (p *Provider) Name() string { return "x" }

func (p *Provider) Platform() downloader.Platform { return downloader.PlatformX }

func (p *Provider) Type() downloader.ProviderType { return downloader.ProviderExternalAPI }

func (p *Provider) MatchesURL(u string) bool {
	parsed, err := url.Parse(strings.TrimSpace(u))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "x.com" || host == "twitter.com" || strings.HasSuffix(host, ".x.com") || strings.HasSuffix(host, ".twitter.com")
}

func (p *Provider) Resolve(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	target := strings.TrimSpace(req.URL)
	m := statusRe.FindStringSubmatch(target)
	if m == nil {
		return nil, downloader.ErrInvalidURL
	}
	screen, id := m[1], m[2]

	raw, err := p.fetch(ctx, screen, id)
	if err != nil {
		return nil, err
	}

	tweet, _ := raw["tweet"].(map[string]any)
	if tweet == nil {
		return nil, downloader.ErrMediaNotFound
	}

	var media []any
	if mw, ok := tweet["media"].(map[string]any); ok {
		if all, ok := mw["all"].([]any); ok {
			media = all
		}
	}

	formats := make([]downloader.Format, 0, len(media))
	var thumb string
	var durationMs int64
	for _, it := range media {
		mItem, ok := it.(map[string]any)
		if !ok {
			continue
		}
		mu, _ := mItem["url"].(string)
		mu = strings.TrimSpace(mu)
		if mu == "" {
			continue
		}
		typ, _ := mItem["type"].(string)
		switch typ {
		case "video", "gif":
			quality := typ
			if w, h := numOf(mItem["width"]), numOf(mItem["height"]); w > 0 && h > 0 {
				quality = fmt.Sprintf("%dx%d", w, h)
			}
			formats = append(formats, downloader.Format{Type: downloader.MediaVideo, URL: mu, Quality: quality, Ext: "mp4"})
			if durationMs == 0 && typ == "video" {
				if d, ok := mItem["duration"].(float64); ok && d > 0 {
					durationMs = int64(d * 1000)
				}
			}
		default:
			formats = append(formats, downloader.Format{Type: downloader.MediaImage, URL: mu, Quality: "photo", Ext: "jpg"})
		}
		if thumb == "" {
			if tu, _ := mItem["thumbnail_url"].(string); strings.TrimSpace(tu) != "" {
				thumb = tu
			}
		}
	}
	if thumb == "" {
		thumb = firstImageURL(formats)
	}
	if len(formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}

	title, _ := tweet["text"].(string)
	res := &downloader.DownloadResult{
		Platform:   downloader.PlatformX,
		URL:        target,
		Title:      title,
		Type:       sameType(formats),
		Thumbnail:  thumb,
		DurationMs: durationMs,
		Formats:    formats,
		Metadata: map[string]string{
			"source":      "fxtwitter",
			"screen_name": screen,
			"tweet_id":    id,
			"author":      "@" + screen,
		},
	}
	return res, nil
}

func (p *Provider) fetch(ctx context.Context, screen, id string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/%s/status/%s", p.base, screen, id), nil)
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, classify(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, classify(err)
	}
	if resp.StatusCode >= 500 {
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

func numOf(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

func firstImageURL(formats []downloader.Format) string {
	for _, f := range formats {
		if f.Type == downloader.MediaImage {
			return f.URL
		}
	}
	return ""
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

func classify(err error) error {
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
