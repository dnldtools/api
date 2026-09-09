package facebook

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"html"
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
	defaultBaseURL = "https://fget.io"

	defaultUserAgent = "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Mobile Safari/537.36"

	defaultLocale = "id"

	maxResponseBytes = 5 << 20
)

type Config struct {
	BaseURL string

	Timeout time.Duration

	UserAgent string

	Locale string

	HTTPClient *http.Client
}

func DefaultConfig() Config {
	return Config{
		BaseURL:   defaultBaseURL,
		Timeout:   30 * time.Second,
		UserAgent: defaultUserAgent,
		Locale:    defaultLocale,
	}
}

type Provider struct {
	baseURL   string
	userAgent string
	locale    string
	client    *http.Client
}

var _ downloader.Provider = (*Provider)(nil)

var _ downloader.URLMatcher = (*Provider)(nil)

func New() *Provider {
	return NewWithConfig(DefaultConfig())
}

func NewWithConfig(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}
	if cfg.Locale == "" {
		cfg.Locale = defaultLocale
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &Provider{
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		userAgent: cfg.UserAgent,
		locale:    cfg.Locale,
		client:    client,
	}
}

func (p *Provider) Name() string { return "facebook" }

func (p *Provider) Platform() downloader.Platform { return downloader.PlatformFacebook }

func (p *Provider) Type() downloader.ProviderType { return downloader.ProviderExternalAPI }

// MatchesURL claims Facebook URLs so they no longer fall through to the
// generic 9xbuddy fallback.
func (p *Provider) MatchesURL(u string) bool {
	parsed, err := url.Parse(strings.TrimSpace(u))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	for _, suffix := range []string{"facebook.com", "fb.com", "fb.watch", "fbwat.ch"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func (p *Provider) Resolve(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, downloader.ErrInvalidURL
	}

	form := url.Values{}
	form.Set("id", req.URL)
	form.Set("locale", p.locale)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/process", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	p.applyHeaders(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, classifyClientError(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, classifyClientError(err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, classifyHTTPStatus(resp.StatusCode)
	}

	result, err := p.parse(body)
	if err != nil {
		return nil, err
	}
	result.Platform = downloader.PlatformFacebook
	if result.URL == "" {
		result.URL = req.URL
	}
	return result, nil
}

func (p *Provider) applyHeaders(r *http.Request) {
	r.Header.Set("User-Agent", p.userAgent)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Hx-Current-Url", p.baseURL+"/id")
	r.Header.Set("Hx-Request", "true")
	r.Header.Set("Hx-Target", "target")
	r.Header.Set("Hx-Trigger", "form")
	r.Header.Set("Origin", p.baseURL)
	r.Header.Set("Referer", p.baseURL+"/id")
}

type upstreamFormat struct {
	Quality string `json:"quality"`
	Type    string `json:"type"`
	URL     string `json:"url"`
}

type upstreamResponse struct {
	Title     string           `json:"title"`
	Thumbnail string           `json:"thumbnail"`
	Downloads []upstreamFormat `json:"downloads"`
}

func (p *Provider) parse(body []byte) (*downloader.DownloadResult, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, downloader.ErrProviderInvalidResponse
	}

	if strings.HasPrefix(trimmed, "{") {
		var parsed upstreamResponse
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
		}
		return p.buildResult(parsed.Title, parsed.Thumbnail, parsed.Downloads)
	}

	if !looksLikeHTML(trimmed) {
		return nil, downloader.ErrProviderInvalidResponse
	}
	title, thumbnail, downloads := parseHTML(trimmed)
	return p.buildResult(title, thumbnail, downloads)
}

func (p *Provider) buildResult(title, thumbnail string, downloads []upstreamFormat) (*downloader.DownloadResult, error) {
	formats := make([]downloader.Format, 0, len(downloads))
	for _, d := range downloads {
		u := html.UnescapeString(strings.TrimSpace(d.URL))
		if u == "" {
			continue
		}
		lower := strings.ToLower(u)
		if strings.Contains(lower, "play.google.com") || strings.Contains(lower, "apple.com") {
			continue
		}
		formats = append(formats, downloader.Format{
			Type:    normalizeMediaType(d.Type),
			URL:     u,
			Quality: html.UnescapeString(strings.TrimSpace(d.Quality)),
		})
	}

	if len(formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}

	result := &downloader.DownloadResult{
		Title:     html.UnescapeString(strings.TrimSpace(title)),
		Thumbnail: html.UnescapeString(strings.TrimSpace(thumbnail)),
		Formats:   formats,
	}

	primary := formats[0].Type
	same := true
	for _, f := range formats[1:] {
		if f.Type != primary {
			same = false
			break
		}
	}
	if same {
		result.Type = primary
	}

	return result, nil
}

func normalizeMediaType(typeStr string) downloader.MediaType {
	t := strings.ToLower(strings.TrimSpace(typeStr))
	switch {
	case containsAny(t, "audio", "mp3", "m4a", "aac", "wav", "ogg"):
		return downloader.MediaAudio
	case containsAny(t, "image", "jpg", "jpeg", "png", "webp", "gif", "bmp"):
		return downloader.MediaImage
	default:
		return downloader.MediaVideo
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

var (
	reThumbnail = regexp.MustCompile(`(?is)class="result-thumbnail".*?<img[^>]+src="([^"]+)"`)
	reTitle     = regexp.MustCompile(`(?is)class="[^"]*result-title[^"]*"[^>]*>(.*?)</h3>`)
	reRow       = regexp.MustCompile(`(?is)<div class="text-sm[^"]*">\s*([^<]+)\s*</div>\s*<div class="text-xs[^"]*">\s*\(?([^)<]*)\)?\s*</div>.*?<a href="([^"]+)"`)
	reTag       = regexp.MustCompile(`<[^>]+>`)
)

func parseHTML(text string) (title, thumbnail string, downloads []upstreamFormat) {
	if m := reThumbnail.FindStringSubmatch(text); m != nil {
		thumbnail = m[1]
	}
	if m := reTitle.FindStringSubmatch(text); m != nil {
		title = reTag.ReplaceAllString(m[1], "")
	}
	for _, m := range reRow.FindAllStringSubmatch(text, -1) {
		downloads = append(downloads, upstreamFormat{
			Quality: strings.TrimSpace(m[1]),
			Type:    strings.TrimSpace(m[2]),
			URL:     strings.TrimSpace(m[3]),
		})
	}
	return strings.TrimSpace(title), strings.TrimSpace(thumbnail), downloads
}

func looksLikeHTML(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{"<html", "<div", "<a ", "<img", "<h3", "<table", "<td", "<tr", "result-"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func classifyClientError(err error) error {
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

func classifyHTTPStatus(status int) error {
	if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
		return downloader.ErrProviderUnavailable
	}
	return downloader.ErrProviderInvalidResponse
}
