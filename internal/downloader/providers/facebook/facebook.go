package facebook

import (
	"bytes"
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
	"rest-api/internal/downloader/providers/snapx"
)

const (
	defaultBaseURL = "https://fget.io"

	defaultUserAgent = "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Mobile Safari/537.36"

	defaultNativeBaseURL = "https://www.facebook.com"

	defaultNativeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"

	defaultLocale = "id"

	maxResponseBytes = 5 << 20
)

type Config struct {
	BaseURL string

	Timeout time.Duration

	UserAgent string

	Locale string

	// NativeEnabled turns on the direct facebook.com fetch (the primary
	// scraper ported from the native-fetch script). DefaultConfig enables it;
	// tests using NewWithConfig stay off unless set so they stay hermetic.
	NativeEnabled bool

	// NativeBaseURL overrides the facebook.com origin used by the native
	// fetch (intended for tests).
	NativeBaseURL string

	// NativeUA is the desktop browser User-Agent used by the native fetch.
	NativeUA string

	// FacebookCookie is an optional logged-in facebook.com cookie
	// (e.g. `c_user=...; xs=...`) attached to the native fetch.
	FacebookCookie string

	HTTPClient *http.Client

	SnapXEnabled bool
	SnapX        *snapx.Client
}

func DefaultConfig() Config {
	return Config{
		BaseURL:       defaultBaseURL,
		Timeout:       30 * time.Second,
		UserAgent:     defaultUserAgent,
		Locale:        defaultLocale,
		NativeEnabled: true,
		NativeBaseURL: defaultNativeBaseURL,
		NativeUA:      defaultNativeUA,
		SnapXEnabled:  true,
	}
}

type Provider struct {
	baseURL   string
	userAgent string
	locale    string
	client    *http.Client
	snapx     *snapx.Client

	nativeEnabled bool
	nativeBase    string
	nativeUA      string
	fbCookie      string
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
	if cfg.NativeBaseURL == "" {
		cfg.NativeBaseURL = defaultNativeBaseURL
	}
	if cfg.NativeUA == "" {
		cfg.NativeUA = defaultNativeUA
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	p := &Provider{
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		userAgent: cfg.UserAgent,
		locale:    cfg.Locale,
		client:    client,

		nativeEnabled: cfg.NativeEnabled,
		nativeBase:    strings.TrimRight(cfg.NativeBaseURL, "/"),
		nativeUA:      cfg.NativeUA,
		fbCookie:      strings.TrimSpace(cfg.FacebookCookie),
	}
	if cfg.SnapXEnabled {
		if cfg.SnapX != nil {
			p.snapx = cfg.SnapX
		} else {
			p.snapx = snapx.NewWithConfig(snapx.Config{HTTPClient: client, Timeout: cfg.Timeout})
		}
	}
	return p
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

	if p.nativeEnabled {
		if native, nerr := p.queryNative(ctx, req.URL); nerr == nil && native != nil && len(native.Formats) > 0 {
			native.Platform = downloader.PlatformFacebook
			if native.URL == "" {
				native.URL = req.URL
			}
			return native, nil
		}
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
		if fallback, ferr := p.querySnapX(ctx, req.URL); ferr == nil {
			return fallback, nil
		}
		return nil, classifyClientError(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		if fallback, ferr := p.querySnapX(ctx, req.URL); ferr == nil {
			return fallback, nil
		}
		return nil, classifyClientError(err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if fallback, ferr := p.querySnapX(ctx, req.URL); ferr == nil {
			return fallback, nil
		}
		return nil, classifyHTTPStatus(resp.StatusCode)
	}

	result, err := p.parse(body)
	if err != nil {
		if fallback, ferr := p.querySnapX(ctx, req.URL); ferr == nil {
			return fallback, nil
		}
		return nil, err
	}
	result.Platform = downloader.PlatformFacebook
	if result.URL == "" {
		result.URL = req.URL
	}
	return result, nil
}

func (p *Provider) queryNative(ctx context.Context, inputURL string) (*downloader.DownloadResult, error) {
	clean := normalizeFacebookURL(inputURL)
	fetchURL := p.nativeFetchURL(clean)

	headers := map[string]string{
		"user-agent":      p.nativeUA,
		"accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"accept-language": "en-US,en;q=0.9,id;q=0.8",
		"sec-fetch-dest":  "document",
		"sec-fetch-mode":  "navigate",
		"sec-fetch-site":  "none",
	}
	if p.fbCookie != "" {
		headers["cookie"] = p.fbCookie
	}

	body, status, err := p.do(ctx, http.MethodGet, fetchURL, headers, nil, 15*time.Second)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices || len(body) < 200 {
		return nil, classifyHTTPStatus(status)
	}

	title := collapseSpace(html.UnescapeString(metaContent(body, "og:title")))
	if title == "" {
		title = "Facebook Post"
	}
	desc := collapseSpace(html.UnescapeString(metaContent(body, "og:description")))
	ogImage := html.UnescapeString(metaContent(body, "og:image"))
	ogVideo := html.UnescapeString(metaContent(body, "og:video"))
	if ogVideo == "" {
		ogVideo = html.UnescapeString(metaContent(body, "og:video:url"))
	}
	if ogVideo == "" {
		ogVideo = html.UnescapeString(metaContent(body, "og:video:secure_url"))
	}

	hd := firstNativeURL(body, reFBNativeHD)
	sd := firstNativeURL(body, reFBNativeSD)
	hdImage := firstNativeImage(body)

	formats := make([]downloader.Format, 0, 3)
	var mediaType downloader.MediaType
	if hd != "" || sd != "" || ogVideo != "" || strings.Contains(clean, "/reel/") || strings.Contains(clean, "/watch/") {
		mediaType = downloader.MediaVideo
		if hd != "" {
			formats = append(formats, downloader.Format{Type: downloader.MediaVideo, URL: hd, Quality: "hd", Ext: "mp4"})
		}
		if sd != "" && sd != hd {
			formats = append(formats, downloader.Format{Type: downloader.MediaVideo, URL: sd, Quality: "sd", Ext: "mp4"})
		}
		if len(formats) == 0 && ogVideo != "" {
			formats = append(formats, downloader.Format{Type: downloader.MediaVideo, URL: ogVideo, Quality: "og", Ext: "mp4"})
		}
	} else {
		mediaType = downloader.MediaImage
		if hdImage != "" {
			formats = append(formats, downloader.Format{Type: downloader.MediaImage, URL: hdImage, Quality: "hd", Ext: "jpg"})
		} else if ogImage != "" {
			formats = append(formats, downloader.Format{Type: downloader.MediaImage, URL: ogImage, Quality: "og", Ext: "jpg"})
		}
	}

	if len(formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}

	res := &downloader.DownloadResult{
		URL:       clean,
		Title:     title,
		Thumbnail: orStr(ogImage, hdImage),
		Type:      mediaType,
		Formats:   formats,
		Metadata:  map[string]string{"source": "facebook_native"},
	}
	if desc != "" {
		res.Metadata["description"] = desc
	}
	return res, nil
}

func (p *Provider) nativeFetchURL(inputURL string) string {
	u := normalizeFacebookURL(inputURL)
	if p.nativeBase == "" || p.nativeBase == defaultNativeBaseURL {
		return u
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	base, err := url.Parse(p.nativeBase)
	if err != nil {
		return u
	}
	parsed.Scheme = base.Scheme
	parsed.Host = base.Host
	return parsed.String()
}

func normalizeFacebookURL(raw string) string {
	clean := strings.TrimSpace(raw)
	if i := strings.IndexByte(clean, '?'); i >= 0 {
		clean = clean[:i]
	}
	return strings.TrimRight(clean, "/")
}

func metaContent(htmlText, property string) string {
	quoted := regexp.QuoteMeta(property)
	re1 := regexp.MustCompile(`(?is)<meta[^>]+property=["']` + quoted + `["'][^>]+content=["']([^"']*)["']`)
	if m := re1.FindStringSubmatch(htmlText); m != nil {
		return m[1]
	}
	re2 := regexp.MustCompile(`(?is)<meta[^>]+content=["']([^"']*)["'][^>]+property=["']` + quoted + `["']`)
	if m := re2.FindStringSubmatch(htmlText); m != nil {
		return m[1]
	}
	return ""
}

func firstNativeURL(htmlText string, patterns []*regexp.Regexp) string {
	for _, re := range patterns {
		if m := re.FindStringSubmatch(htmlText); m != nil && len(m) > 1 && m[1] != "" {
			return unescapeFbURL(m[1])
		}
	}
	return ""
}

func firstNativeImage(htmlText string) string {
	for _, re := range reFBNativeImage {
		if m := re.FindStringSubmatch(htmlText); m != nil && len(m) > 1 && m[1] != "" {
			u := unescapeFbURL(m[1])
			if strings.Contains(u, "fbcdn.net") {
				return u
			}
		}
	}
	return ""
}

func unescapeFbURL(u string) string {
	u = strings.ReplaceAll(u, `\u0025`, "%")
	u = strings.ReplaceAll(u, `\u0026`, "&")
	u = strings.ReplaceAll(u, `\u002F`, "/")
	u = strings.ReplaceAll(u, `\u00253D`, "=")
	u = strings.ReplaceAll(u, `\/`, "/")
	return strings.ReplaceAll(u, `\`, "")
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func orStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (p *Provider) do(ctx context.Context, method, target string, headers map[string]string, body []byte, timeout time.Duration) (string, int, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return "", 0, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", 0, classifyClientError(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", 0, classifyClientError(err)
	}
	return string(data), resp.StatusCode, nil
}

func (p *Provider) querySnapX(ctx context.Context, mediaURL string) (*downloader.DownloadResult, error) {
	if p.snapx == nil {
		return nil, downloader.ErrMediaNotFound
	}
	return p.snapx.Facebook(ctx, mediaURL)
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

	reFBNativeHD = []*regexp.Regexp{
		regexp.MustCompile(`"browser_native_hd_url"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`"playable_url_quality_hd"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`"hd_src"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`"hd_src_no_ratelimit"\s*:\s*"([^"]+)"`),
	}
	reFBNativeSD = []*regexp.Regexp{
		regexp.MustCompile(`"browser_native_sd_url"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`"playable_url"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`"sd_src"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`"sd_src_no_ratelimit"\s*:\s*"([^"]+)"`),
	}
	reFBNativeImage = []*regexp.Regexp{
		regexp.MustCompile(`"image"\s*:\s*\{\s*"uri"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`"full_sub_photo"\s*:\s*\{\s*"image"\s*:\s*\{\s*"uri"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`"photo_image"\s*:\s*\{\s*"uri"\s*:\s*"([^"]+)"`),
	}
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
