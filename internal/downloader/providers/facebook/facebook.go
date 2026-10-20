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
	"strconv"
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
		native, nerr := p.queryNative(ctx, req.URL)
		if nerr == nil && native != nil && len(native.Formats) > 0 {
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

	if hd == "" || sd == "" {
		phd, psd := extractProgressiveURLs(body)
		if hd == "" {
			hd = phd
		}
		if sd == "" {
			sd = psd
		}
	}
	if hd == "" && sd == "" {
		hd, sd = parseDashVideoURLs(body)
	}

	audioURL := extractDashAudioURL(body)

	looksLikePhoto := strings.Contains(clean, "/share/p/") || strings.Contains(clean, "/photo/") || strings.Contains(clean, "/photos/")

	formats := make([]downloader.Format, 0, 4)
	var mediaType downloader.MediaType
	if !looksLikePhoto && (hd != "" || sd != "" || ogVideo != "" || strings.Contains(clean, "/reel/") || strings.Contains(clean, "/watch/") || strings.Contains(clean, "/videos/") || strings.Contains(clean, "/share/v/") || strings.Contains(clean, "/stories/")) {
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
		if audioURL != "" {
			formats = append(formats, downloader.Format{Type: downloader.MediaAudio, URL: audioURL, Quality: "audio", Ext: "m4a"})
		}
	} else {
		mediaType = downloader.MediaImage
		photoURIs := extractFBPhotos(body)
		for i, u := range photoURIs {
			quality := "hd"
			if len(photoURIs) > 1 {
				quality = "slide_" + strconv.Itoa(i+1)
			}
			formats = append(formats, downloader.Format{Type: downloader.MediaImage, URL: u, Quality: quality, Ext: "jpg"})
		}
		if len(formats) == 0 && hdImage != "" {
			formats = append(formats, downloader.Format{Type: downloader.MediaImage, URL: hdImage, Quality: "hd", Ext: "jpg"})
		}
		if len(formats) == 0 && ogImage != "" {
			formats = append(formats, downloader.Format{Type: downloader.MediaImage, URL: ogImage, Quality: "og", Ext: "jpg"})
		}
	}

	if len(formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}

	looksLikeVideo := strings.Contains(clean, "/reel/") || strings.Contains(clean, "/watch/") || strings.Contains(clean, "/videos/") || strings.Contains(clean, "/share/v/") || strings.Contains(clean, "/stories/")
	if looksLikeVideo && mediaType != downloader.MediaVideo {
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
	fbMeta := extractFBMetadata(body)
	for k, v := range fbMeta {
		if _, exists := res.Metadata[k]; !exists {
			res.Metadata[k] = v
		}
	}
	if d := fbMeta["duration_ms"]; d != "" {
		if ms, err := strconv.ParseInt(d, 10, 64); err == nil && ms > 0 {
			res.DurationMs = ms
		}
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

func parseDashVideoURLs(body string) (hd, sd string) {
	idx := strings.Index(body, `"manifest_xml":"`)
	if idx < 0 {
		return "", ""
	}
	jsonStart := idx + len(`"manifest_xml":`)
	decoder := json.NewDecoder(strings.NewReader(body[jsonStart:]))
	var xmlStr string
	if decoder.Decode(&xmlStr) != nil {
		return "", ""
	}

	re := regexp.MustCompile(`<BaseURL>([^<]+)</BaseURL>`)
	bases := re.FindAllStringSubmatch(xmlStr, -1)
	if len(bases) == 0 {
		return "", ""
	}

	videoURLs := []string{}
	for _, b := range bases {
		u := html.UnescapeString(b[1])
		if strings.Contains(u, "fbcdn.net") && strings.Contains(u, ".mp4") {
			videoURLs = append(videoURLs, u)
		}
	}
	if len(videoURLs) == 0 {
		return "", ""
	}
	return videoURLs[0], videoURLs[1%len(videoURLs)]
}

func extractProgressiveURLs(body string) (hd, sd string) {
	re := regexp.MustCompile(`"progressive_url"\s*:\s*"([^"]+)"[^}]*?"quality"\s*:\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(body, -1)
	seen := make(map[string]bool)
	for _, m := range matches {
		u := unescapeFbURL(m[1])
		if seen[u] {
			continue
		}
		seen[u] = true
		q := strings.ToUpper(m[2])
		if q == "HD" && hd == "" {
			hd = u
		} else if q == "SD" && sd == "" {
			sd = u
		}
	}
	return hd, sd
}

func extractFBPhotos(body string) []string {
	m := reFBPhotoAttachmentsList.FindStringSubmatch(body)
	if m != nil {
		ids := reFBPhotoIDInList.FindAllStringSubmatch(m[1], -1)
		var uris []string
		for _, idm := range ids {
			uri := extractPhotoURIByID(body, idm[1])
			if uri != "" {
				uris = append(uris, uri)
			}
		}
		if len(uris) > 0 {
			return uris
		}
	}

	if asaURIs := extractSubAttachmentURIs(body); len(asaURIs) > 0 {
		return asaURIs
	}

	if u := extractMainPhotoRendererURI(body); u != "" {
		return []string{u}
	}

	if u := firstNativeImage(body); u != "" {
		return []string{u}
	}
	return nil
}

func extractPhotoURIByID(body, photoID string) string {
	marker := `"id":"` + photoID + `"`
	searchFrom := 0
	for {
		relIdx := strings.Index(body[searchFrom:], marker)
		if relIdx < 0 {
			break
		}
		absIdx := searchFrom + relIdx
		end := absIdx + len(marker) + 3000
		if end > len(body) {
			end = len(body)
		}
		if m := rePhotoURI.FindStringSubmatch(body[absIdx:end]); m != nil {
			return unescapeFbURL(m[1])
		}
		backStart := absIdx - 500
		if backStart < 0 {
			backStart = 0
		}
		backMatches := rePhotoURI.FindAllStringSubmatch(body[backStart:absIdx+len(marker)], -1)
		if len(backMatches) > 0 {
			return unescapeFbURL(backMatches[len(backMatches)-1][1])
		}
		searchFrom = absIdx + len(marker)
	}
	return ""
}

func extractSubAttachmentURIs(body string) []string {
	asaIdx := strings.Index(body, `"all_subattachments"`)
	if asaIdx < 0 {
		return nil
	}
	end := asaIdx + 20000
	if end > len(body) {
		end = len(body)
	}
	nodes := body[asaIdx:end]
	nodeEnd := strings.Index(nodes, `}]`)
	if nodeEnd > 0 {
		nodes = nodes[:nodeEnd]
	}
	ids := reFBSubAttachPhotoID.FindAllStringSubmatch(nodes, -1)
	if len(ids) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var uris []string
	for _, idm := range ids {
		if seen[idm[1]] {
			continue
		}
		seen[idm[1]] = true
		uri := extractPhotoURIByID(body, idm[1])
		if uri != "" {
			uris = append(uris, uri)
		}
	}
	return uris
}

func extractMainPhotoRendererURI(body string) string {
	idx := strings.Index(body, `StoryAttachmentPhotoStyleRenderer`)
	if idx < 0 {
		return ""
	}
	end := idx + 3000
	if end > len(body) {
		end = len(body)
	}
	chunk := body[idx:end]
	if m := reFBPhotoImageURI.FindStringSubmatch(chunk); m != nil {
		return unescapeFbURL(m[1])
	}
	if m := reFBNativeImage[0].FindStringSubmatch(chunk); m != nil {
		u := unescapeFbURL(m[1])
		if strings.Contains(u, "fbcdn.net") {
			return u
		}
	}
	return ""
}

func extractDashAudioURL(body string) string {
	idx := strings.Index(body, `"manifest_xml":"`)
	if idx < 0 {
		return ""
	}
	jsonStart := idx + len(`"manifest_xml":`)
	decoder := json.NewDecoder(strings.NewReader(body[jsonStart:]))
	var xmlStr string
	if decoder.Decode(&xmlStr) != nil {
		return ""
	}
	re := regexp.MustCompile(`(?s)<AdaptationSet[^>]*contentType="audio"[^>]*>.*?<BaseURL>([^<]+)</BaseURL>`)
	m := re.FindStringSubmatch(xmlStr)
	if m == nil {
		return ""
	}
	u := html.UnescapeString(m[1])
	u = strings.ReplaceAll(u, `\/`, "/")
	if strings.Contains(u, "fbcdn.net") {
		return u
	}
	return ""
}

func extractFBMetadata(body string) map[string]string {
	md := make(map[string]string)
	set := func(k, v string) {
		v = strings.TrimSpace(v)
		if v != "" && v != "null" {
			md[k] = v
		}
	}

	if m := reFBVideoID.FindStringSubmatch(body); m != nil {
		set("video_id", m[1])
	}
	if m := reFBOwnerID.FindStringSubmatch(body); m != nil {
		set("author_id", m[1])
	}
	if m := reFBOwnerName.FindStringSubmatch(body); m != nil {
		set("author", m[1])
	}
	if m := reFBCreationTime.FindStringSubmatch(body); m != nil {
		if ts, err := strconv.ParseInt(m[1], 10, 64); err == nil && ts > 0 {
			set("created_at", time.Unix(ts, 0).UTC().Format(time.RFC3339))
		}
	}
	if m := reFBPlayableDuration.FindStringSubmatch(body); m != nil {
		set("duration_ms", m[1])
	}
	if m := reFBCommentCount.FindStringSubmatch(body); m != nil {
		set("comments", m[1])
	}
	if m := reFBShareCount.FindStringSubmatch(body); m != nil {
		set("shares", m[1])
	}
	if m := reFBCaptionText.FindStringSubmatch(body); m != nil {
		caption := unescapeFbJSON(m[1])
		caption = strings.TrimSpace(caption)
		if caption != "" && caption != "Every family has some skeletons in their closet." {
			set("caption", caption)
		}
	}
	if m := reFBThumbURI.FindStringSubmatch(body); m != nil {
		set("thumbnail", unescapeFbURL(m[1]))
	}

	if len(md) == 0 {
		return nil
	}
	return md
}

func unescapeFbJSON(s string) string {
	s = strings.ReplaceAll(s, `\\`, `\`)
	s = strings.ReplaceAll(s, `\n`, " ")
	s = strings.ReplaceAll(s, `\u00a0`, " ")
	s = strings.ReplaceAll(s, `\u2019`, "'")
	s = strings.ReplaceAll(s, `\u201c`, `"`)
	s = strings.ReplaceAll(s, `\u201d`, `"`)
	s = strings.ReplaceAll(s, `\u2026`, "…")
	s = strings.ReplaceAll(s, `\uFFFD`, "")
	s = strings.ReplaceAll(s, `\ufe0f`, "")
	s = strings.ReplaceAll(s, `\u2764`, "❤")
	s = strings.ReplaceAll(s, `\"`, `"`)
	return strings.TrimSpace(s)
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

	reFBVideoID          = regexp.MustCompile(`"video_id"\s*:\s*"(\d+)"`)
	reFBOwnerID          = regexp.MustCompile(`"owner"\s*:\s*\{[^}]*"id"\s*:\s*"(\d+)"`)
	reFBOwnerName        = regexp.MustCompile(`"owner"\s*:\s*\{[^}]*"name"\s*:\s*"([^"]+)"`)
	reFBCreationTime     = regexp.MustCompile(`"creation_time"\s*:\s*(\d+)`)
	reFBPlayableDuration = regexp.MustCompile(`"playable_duration_in_ms"\s*:\s*(\d+)`)
	reFBCommentCount     = regexp.MustCompile(`"total_comment_count"\s*:\s*(\d+)`)
	reFBShareCount       = regexp.MustCompile(`"share_count_reduced"\s*:\s*"([^"]+)"`)
	reFBCaptionText      = regexp.MustCompile(`"creation_story"\s*:[\s\S]*?"text"\s*:\s*"((?:[^"\\]|\\.){5,500})"`)
	reFBThumbURI         = regexp.MustCompile(`"thumbnail_image"\s*:\s*\{[^}]*"uri"\s*:\s*"([^"]+)"`)

	reFBPhotoAttachmentsList = regexp.MustCompile(`\\"photo_attachments_list\\":\[([^\]]+)\]`)
	reFBPhotoIDInList        = regexp.MustCompile(`\\"(\d+)\\"`)
	rePhotoURI               = regexp.MustCompile(`(?s)"uri"\s*:\s*"(https:[^"]+fbcdn[^"]+)"`)
	reFBSubAttachPhotoID    = regexp.MustCompile(`"__typename"\s*:\s*"Photo"[^}]*?"__isNode"\s*:\s*"Photo"[^}]*?"id"\s*:\s*"(\d+)"`)
	reFBPhotoImageURI       = regexp.MustCompile(`"photo_image"\s*:\s*\{[^}]*"uri"\s*:\s*"([^"]+)"`)
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
