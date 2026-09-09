package tiktok

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/snapx"
)

const (
	defaultRelay        = "https://cors.siputzx.my.id/"
	defaultSnaptikAPI   = "https://snaptik.net/api/ajaxSearch"
	defaultOfficialBase = "https://www.tiktok.com"
	defaultUserAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	officialUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36 Edg/152.0.0.0"
	maxResponseBytes    = 5 << 20
	officialTimeout     = 15 * time.Second
	snaptikTimeout      = 20 * time.Second
	resolveURLTimeout   = 15 * time.Second
)

type Config struct {
	RelayBaseURL string

	// OfficialBaseURL overrides the base URL used for the direct official
	// rehydration fetch. Defaults to https://www.tiktok.com; intended for tests.
	OfficialBaseURL string

	// ResolveBaseURL, when set, is used instead of a direct fetch to resolve
	// tiktok short links (intended for tests).
	ResolveBaseURL string

	// AllowedMediaHosts are additional host suffixes (or exact hosts) that
	// StreamMedia is allowed to fetch from, on top of the built-in TikTok CDN
	// allowlist (intended for tests).
	AllowedMediaHosts []string

	SnaptikAPIURL string

	Timeout time.Duration

	UserAgent string

	HTTPClient *http.Client

	SnapXEnabled bool
	SnapX        *snapx.Client
}

func DefaultConfig() Config {
	return Config{
		RelayBaseURL:    defaultRelay,
		OfficialBaseURL: defaultOfficialBase,
		SnaptikAPIURL:   defaultSnaptikAPI,
		Timeout:         30 * time.Second,
		UserAgent:       defaultUserAgent,
		SnapXEnabled:    true,
	}
}

type Provider struct {
	relay        string
	officialBase string
	resolveBase  string
	snaptikAPI   string
	userAgent    string
	allowedHosts []string
	client       *http.Client
	snapx        *snapx.Client
}

var _ downloader.Provider = (*Provider)(nil)

var _ downloader.URLMatcher = (*Provider)(nil)

func New() *Provider {
	return NewWithConfig(DefaultConfig())
}

func NewWithConfig(cfg Config) *Provider {
	if cfg.RelayBaseURL == "" {
		cfg.RelayBaseURL = defaultRelay
	}
	if cfg.OfficialBaseURL == "" {
		cfg.OfficialBaseURL = defaultOfficialBase
	}
	if cfg.SnaptikAPIURL == "" {
		cfg.SnaptikAPIURL = defaultSnaptikAPI
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}
	client := cfg.HTTPClient
	if client == nil {
		jar, _ := cookiejar.New(nil)
		client = &http.Client{Timeout: cfg.Timeout, Jar: jar}
	}
	p := &Provider{
		relay:        strings.TrimRight(cfg.RelayBaseURL, "/"),
		officialBase: strings.TrimRight(cfg.OfficialBaseURL, "/"),
		resolveBase:  strings.TrimRight(cfg.ResolveBaseURL, "/"),
		snaptikAPI:   cfg.SnaptikAPIURL,
		userAgent:    cfg.UserAgent,
		allowedHosts: cfg.AllowedMediaHosts,
		client:       client,
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

func (p *Provider) Name() string { return "tiktok" }

func (p *Provider) Platform() downloader.Platform { return downloader.PlatformTikTok }

func (p *Provider) Type() downloader.ProviderType { return downloader.ProviderExternalAPI }

func (p *Provider) MatchesURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	for _, suffix := range []string{"tiktok.com", "douyin.com", "iesdouyin.com"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func (p *Provider) Resolve(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	inputURL := strings.TrimSpace(req.URL)
	if inputURL == "" {
		return nil, downloader.ErrInvalidURL
	}
	if !p.MatchesURL(inputURL) {
		return nil, downloader.ErrInvalidURL
	}

	resolved, err := p.resolveTikTokURL(ctx, inputURL)
	if err != nil {
		return nil, err
	}

	// Official rehydration is the primary path: it is tried first and only
	// falls back to snaptik when the official scrape fails.
	offRes, offErr := p.queryOfficial(ctx, resolved)
	if offErr == nil && offRes != nil && len(offRes.Formats) > 0 {
		offRes.Platform = downloader.PlatformTikTok
		offRes.URL = resolved
		return offRes, nil
	}

	snapRes, snapErr := p.querySnaptik(ctx, resolved)
	if snapErr == nil && snapRes != nil && len(snapRes.Formats) > 0 {
		snapRes.Platform = downloader.PlatformTikTok
		snapRes.URL = resolved
		return snapRes, nil
	}

	if p.snapx != nil {
		sxRes, sxErr := p.snapx.TikTok(ctx, resolved)
		if sxErr == nil && sxRes != nil && len(sxRes.Formats) > 0 {
			sxRes.Platform = downloader.PlatformTikTok
			sxRes.URL = resolved
			return sxRes, nil
		}
		if sxErr != nil {
			snapErr = sxErr
		}
	}

	if snapErr != nil {
		return nil, snapErr
	}
	return nil, offErr
}

func (p *Provider) resolveTikTokURL(ctx context.Context, inputURL string) (string, error) {
	u := strings.TrimSpace(inputURL)
	if strings.HasPrefix(u, "http://") {
		u = "https://" + u[len("http://"):]
	}
	if !isShortLink(u) {
		return u, nil
	}

	target := u
	if p.resolveBase != "" {
		target = p.resolveBase + "/" + u
	}

	// Resolve the short link directly (follow redirects) so the redirect lands
	// on the canonical tiktok.com URL carrying the numeric video/photo id.
	body, finalURL, status, err := p.do(ctx, http.MethodGet, target, map[string]string{
		"user-agent":      officialUserAgent,
		"accept":          "text/html",
		"accept-language": "en-US,en;q=0.9",
	}, nil, resolveURLTimeout)
	if err != nil {
		return "", err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", classifyHTTPStatus(status)
	}

	// Prefer the redirected URL when it's a tiktok.com page with a numeric id.
	if id := firstMatch(reVideoID, finalURL); id != "" && strings.Contains(finalURL, "tiktok.com") {
		clean := finalURL
		if i := strings.IndexByte(clean, '?'); i >= 0 {
			clean = clean[:i]
		}
		return clean, nil
	}

	picked := html.UnescapeString(orStr(
		firstMatch(reCanonical1, body),
		firstMatch(reCanonical2, body),
		firstMatch(reOGURL, body),
	))
	if picked == "" {
		picked = finalURL
	}
	if picked == "" {
		picked = u
	}

	stripped := strings.TrimPrefix(picked, "https://")
	stripped = strings.TrimPrefix(stripped, "http://")
	stripped = strings.TrimPrefix(stripped, "www.")
	if reNonMediaPage.MatchString(stripped) {
		return "", downloader.ErrMediaNotFound
	}
	return picked, nil
}

func (p *Provider) queryOfficial(ctx context.Context, target string) (*downloader.DownloadResult, error) {
	// Direct fetch to TikTok (no relay), mirroring the reference scraper.
	fetchURL := p.officialBase + officialPath(target)
	body, _, status, err := p.do(ctx, http.MethodGet, fetchURL, map[string]string{
		"user-agent":      officialUserAgent,
		"accept":          "text/html",
		"accept-language": "en-US,en;q=0.9",
	}, nil, officialTimeout)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, classifyHTTPStatus(status)
	}
	if len(body) < 200 {
		return nil, downloader.ErrProviderInvalidResponse
	}

	raw := firstMatch(reRehydration, body)
	if raw == "" {
		return nil, downloader.ErrProviderInvalidResponse
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}

	detail := nestedMap(data, "__DEFAULT_SCOPE__", "webapp.video-detail")
	if detail == nil {
		return nil, downloader.ErrProviderInvalidResponse
	}

	item := nestedMap(detail, "itemInfo", "itemStruct")
	if item == nil || str(item["id"]) == "" {
		return nil, downloader.ErrMediaNotFound
	}

	video := asMap(item["video"])
	images := make([]string, 0)
	for _, img := range nestedSlice(item, "imagePost", "images") {
		list := nestedSlice(asMap(img), "imageURL", "urlList")
		if len(list) > 0 {
			if u := str(list[0]); u != "" {
				images = append(images, u)
			}
		}
	}

	items := make([]mediaItem, 0, len(images)+2)
	if mp4 := pickPlayURL(video); mp4 != "" {
		items = append(items, mediaItem{kind: "video", url: mp4, thumb: orStr(str(video["cover"]), str(video["originCover"]))})
	}
	for _, img := range images {
		items = append(items, mediaItem{kind: "photo", url: img, thumb: img})
	}
	if mp3 := str(asMap(item["music"])["playUrl"]); mp3 != "" {
		items = append(items, mediaItem{kind: "audio", url: mp3})
	}
	if len(items) == 0 {
		return nil, downloader.ErrMediaNotFound
	}

	author := asMap(item["author"])
	title := str(item["desc"])
	cover := orStr(str(video["cover"]), str(video["originCover"]))
	if cover == "" && len(images) > 0 {
		cover = images[0]
	}

	return p.buildResult(title, cover, orStr(str(author["uniqueId"]), str(author["nickname"])), items), nil
}

func (p *Provider) querySnaptik(ctx context.Context, target string) (*downloader.DownloadResult, error) {
	form := url.Values{}
	form.Set("q", target)
	form.Set("lang", "en")

	body, _, status, err := p.do(ctx, http.MethodPost, viaRelay(p.relay, p.snaptikAPI), map[string]string{
		"user-agent":       p.userAgent,
		"origin":           "https://snaptik.net",
		"referer":          "https://snaptik.net/en",
		"x-requested-with": "XMLHttpRequest",
		"content-type":     "application/x-www-form-urlencoded; charset=UTF-8",
		"accept":           "application/json, text/javascript, */*; q=0.01",
	}, []byte(form.Encode()), snaptikTimeout)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, classifyHTTPStatus(status)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}

	msg := str(parsed["msg"])
	if str(parsed["status"]) != "ok" {
		if num(parsed["statusCode"]) == 326 || strings.Contains(msg, "snaptikpro.net") {
			return nil, downloader.ErrMediaNotFound
		}
		return nil, downloader.ErrProviderInvalidResponse
	}

	htmlData := str(parsed["data"])
	if htmlData == "" {
		return nil, downloader.ErrProviderInvalidResponse
	}

	title := html.UnescapeString(firstMatch(reH3, htmlData))
	title = strings.TrimSpace(title)
	cover := html.UnescapeString(firstMatch(reImg, htmlData))

	items := make([]mediaItem, 0)
	for _, m := range reAnchor.FindAllStringSubmatch(htmlData, -1) {
		href := html.UnescapeString(m[1])
		if strings.HasPrefix(href, "/") {
			href = "https://snaptik.net" + href
		}
		if !reAllowedHost.MatchString(href) {
			continue
		}
		if reSnaptikPro.MatchString(href) {
			continue
		}

		direct := jwtURL(href)
		label := strings.ReplaceAll(reTag.ReplaceAllString(m[2], ""), "&nbsp;", " ")
		label = strings.ToLower(strings.TrimSpace(html.UnescapeString(label)))

		kind := "video"
		switch {
		case reAudioLabel.MatchString(label):
			kind = "audio"
		case rePhotoLabel.MatchString(label):
			kind = "photo"
		}

		thumb := cover
		if kind == "photo" {
			thumb = direct
		}
		items = append(items, mediaItem{kind: kind, url: direct, thumb: thumb})
	}

	if len(items) == 0 {
		return nil, downloader.ErrMediaNotFound
	}

	author := firstMatch(reAtHandle, target)
	return p.buildResult(title, cover, author, items), nil
}

func (p *Provider) buildResult(title, cover, author string, items []mediaItem) *downloader.DownloadResult {
	formats := make([]downloader.Format, 0, len(items))
	thumb := cover
	for _, it := range items {
		formats = append(formats, downloader.Format{Type: mediaType(it.kind), URL: it.url})
		if thumb == "" && it.thumb != "" {
			thumb = it.thumb
		}
	}

	result := &downloader.DownloadResult{
		Title:     title,
		Thumbnail: thumb,
		Formats:   formats,
	}
	if author != "" {
		result.Metadata = map[string]string{"author": author}
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
	return result
}

type mediaItem struct {
	kind  string
	url   string
	thumb string
}

func mediaType(kind string) downloader.MediaType {
	switch kind {
	case "video":
		return downloader.MediaVideo
	case "audio":
		return downloader.MediaAudio
	default:
		return downloader.MediaImage
	}
}

// officialPath derives the canonical TikTok page path for a video URL,
// mirroring the reference scraper: strip query parameters and, for inputs that
// are not already tiktok.com URLs (e.g. a raw video id), use the stable
// /@i/video/<id> route.
func officialPath(input string) string {
	u := strings.TrimSpace(input)
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	if !strings.Contains(u, "tiktok.com") {
		if id := firstMatch(reVideoID, u); id != "" {
			return "/@i/video/" + id
		}
	}
	if parsed, err := url.Parse(u); err == nil && parsed.Path != "" {
		return parsed.Path
	}
	return u
}

func (p *Provider) do(ctx context.Context, method, target string, headers map[string]string, body []byte, timeout time.Duration) (string, string, int, error) {
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
		return "", "", 0, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", 0, classifyClientError(err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", "", 0, classifyClientError(err)
	}

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return string(data), finalURL, resp.StatusCode, nil
}

// StreamMedia fetches a resolved media URL server-side using this provider's
// client. Cookies captured while resolving the page (e.g. tt_chain_token) live
// in the client's cookie jar and are attached automatically, and the referer/origin
// headers TikTok's CDN expects are set here so downstream clients never need them.
func (p *Provider) StreamMedia(ctx context.Context, mediaURL string) (*downloader.MediaStream, error) {
	if !p.allowedMediaHost(mediaURL) {
		return nil, downloader.ErrInvalidURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	req.Header.Set("user-agent", officialUserAgent)
	req.Header.Set("accept", "*/*")
	req.Header.Set("referer", "https://www.tiktok.com/")
	req.Header.Set("origin", "https://www.tiktok.com")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, classifyClientError(err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		resp.Body.Close()
		return nil, classifyHTTPStatus(resp.StatusCode)
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

func viaRelay(relay, target string) string {
	return strings.TrimRight(relay, "/") + "/" + target
}

func isShortLink(u string) bool {
	return reShortLink.MatchString(u)
}

// mediaHostSuffixes are the only hosts StreamMedia will fetch from, guarding
// against SSRF via a tampered media URL.
var mediaHostSuffixes = []string{
	"tiktok.com",
	"tiktokcdn.com",
	"tiktokcdn-us.com",
	"tiktokcdn-eu.com",
	"tokcdn.com",
	"tiktokv.com",
	"muscdn.com",
	"douyin.com",
	"iesdouyin.com",
	"ibytedtos.com",
	"byteimg.com",
	"ibyteimg.com",
	"snapcdn.app",
	"tik-cdn.com",
	"snaptik.net",
}

func (p *Provider) allowedMediaHost(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	for _, suffix := range mediaHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	for _, suffix := range p.allowedHosts {
		suffix = strings.ToLower(strings.TrimSpace(suffix))
		if suffix == "" {
			continue
		}
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func jwtURL(href string) string {
	parsed, err := url.Parse(href)
	if err != nil {
		return href
	}
	token := parsed.Query().Get("token")
	if token == "" {
		return href
	}
	payload := jwtPayload(token)
	if u := str(payload["url"]); u != "" {
		return u
	}
	return href
}

func jwtPayload(token string) map[string]interface{} {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

func pickPlayURL(video map[string]interface{}) string {
	for _, b := range sliceOf(video["bitrateInfo"]) {
		list := nestedSlice(asMap(b), "PlayAddr", "UrlList")
		if len(list) > 0 {
			if u := str(list[0]); u != "" {
				return u
			}
		}
		if len(list) > 1 {
			if u := str(list[1]); u != "" {
				return u
			}
		}
	}
	if u := str(video["playAddr"]); u != "" {
		return u
	}
	if u := str(video["downloadAddr"]); u != "" {
		return u
	}
	if list := nestedSlice(video, "PlayAddrStruct", "UrlList"); len(list) > 0 {
		return str(list[0])
	}
	return ""
}

var (
	reShortLink    = regexp.MustCompile(`(?i)^(https?://)?(?:(?:vm|vt|m)\.tiktok\.com|v\.douyin\.com)/`)
	reVideoID      = regexp.MustCompile(`(\d{15,})`)
	reCanonical1   = regexp.MustCompile(`(?i)rel=["']canonical["'][^>]+href=["']([^"']+)`)
	reCanonical2   = regexp.MustCompile(`(?i)href=["']([^"']+)["'][^>]+rel=["']canonical["']`)
	reOGURL        = regexp.MustCompile(`(?i)property=["']og:url["'][^>]+content=["']([^"']+)`)
	reNonMediaPage = regexp.MustCompile(`(?i)tiktok\.com/?$|/foryou|/following|/live|/discover`)
	reRehydration  = regexp.MustCompile(`(?s)id="__UNIVERSAL_DATA_FOR_REHYDRATION__"[^>]*>(\{.*?\})</script>`)
	reH3           = regexp.MustCompile(`(?is)<h3>(.*?)</h3>`)
	reImg          = regexp.MustCompile(`(?is)<img[^>]+src="([^"]+)"`)
	reAnchor       = regexp.MustCompile(`(?is)<a\s+(?:[^>]*?\s+)?href="([^"]+)"[^>]*>(.*?)</a>`)
	reTag          = regexp.MustCompile(`<[^>]+>`)
	reAllowedHost  = regexp.MustCompile(`(?i)snapcdn\.app|tik-cdn\.com|snaptik\.net/api/`)
	reSnaptikPro   = regexp.MustCompile(`(?i)snaptikpro\.net`)
	reAudioLabel   = regexp.MustCompile(`mp3|audio`)
	rePhotoLabel   = regexp.MustCompile(`photo|image|pic|slide|jpg|jpeg|png`)
	reAtHandle     = regexp.MustCompile(`@([A-Za-z0-9_.]+)`)
)

func firstMatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func asMap(v interface{}) map[string]interface{} {
	m, _ := v.(map[string]interface{})
	return m
}

func sliceOf(v interface{}) []interface{} {
	s, _ := v.([]interface{})
	return s
}

func str(v interface{}) string {
	s, _ := v.(string)
	return s
}

func num(v interface{}) float64 {
	f, _ := v.(float64)
	return f
}

func orStr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func nestedMap(v interface{}, keys ...string) map[string]interface{} {
	cur := v
	for _, k := range keys {
		m := asMap(cur)
		if m == nil {
			return nil
		}
		cur = m[k]
	}
	return asMap(cur)
}

func nestedSlice(v interface{}, keys ...string) []interface{} {
	cur := v
	for _, k := range keys {
		m := asMap(cur)
		if m == nil {
			return nil
		}
		cur = m[k]
	}
	return sliceOf(cur)
}
