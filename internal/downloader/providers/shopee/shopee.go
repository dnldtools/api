package shopee

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"rest-api/internal/downloader"
)

const (
	defaultUserAgent = "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Mobile Safari/537.36"

	defaultProxyListURL = "https://niek.github.io/free-proxy-list/"

	defaultExtractURL = "https://shopeenowatermark.com/api/extract"

	defaultProbeURL = "https://httpbin.org/ip"

	defaultMaxValidate = 24

	defaultMaxLiveExtract = 4

	defaultProbeBatch = 8

	maxResponseBytes = 5 << 20

	proxyListTimeout = 20 * time.Second

	probeTimeout = 4 * time.Second

	extractTimeout = 12 * time.Second

	officialTimeout = 15 * time.Second
)

type Config struct {
	UserAgent string

	Timeout time.Duration

	ProxyListURL string

	ExtractURL string

	ProbeURL string

	MaxValidate int

	MaxLiveExtract int

	ProbeBatch int

	HTTPClient *http.Client
}

func DefaultConfig() Config {
	return Config{
		UserAgent:      defaultUserAgent,
		Timeout:        30 * time.Second,
		ProxyListURL:   defaultProxyListURL,
		ExtractURL:     defaultExtractURL,
		ProbeURL:       defaultProbeURL,
		MaxValidate:    defaultMaxValidate,
		MaxLiveExtract: defaultMaxLiveExtract,
		ProbeBatch:     defaultProbeBatch,
	}
}

type Provider struct {
	userAgent string

	client *http.Client

	proxyListURL string

	extractURL string

	probeURL string

	maxValidate int

	maxLiveExtract int

	probeBatch int
}

var _ downloader.Provider = (*Provider)(nil)

var _ downloader.URLMatcher = (*Provider)(nil)

func New() *Provider {
	return NewWithConfig(DefaultConfig())
}

func NewWithConfig(cfg Config) *Provider {
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.ProxyListURL == "" {
		cfg.ProxyListURL = defaultProxyListURL
	}
	if cfg.ExtractURL == "" {
		cfg.ExtractURL = defaultExtractURL
	}
	if cfg.ProbeURL == "" {
		cfg.ProbeURL = defaultProbeURL
	}
	if cfg.MaxValidate <= 0 {
		cfg.MaxValidate = defaultMaxValidate
	}
	if cfg.MaxLiveExtract <= 0 {
		cfg.MaxLiveExtract = defaultMaxLiveExtract
	}
	if cfg.ProbeBatch <= 0 {
		cfg.ProbeBatch = defaultProbeBatch
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &Provider{
		userAgent:      cfg.UserAgent,
		client:         client,
		proxyListURL:   cfg.ProxyListURL,
		extractURL:     cfg.ExtractURL,
		probeURL:       cfg.ProbeURL,
		maxValidate:    cfg.MaxValidate,
		maxLiveExtract: cfg.MaxLiveExtract,
		probeBatch:     cfg.ProbeBatch,
	}
}

func (p *Provider) Name() string { return "shopee" }

func (p *Provider) Platform() downloader.Platform { return downloader.PlatformShopee }

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
	return strings.Contains(host, "shopee.") || strings.Contains(host, "shp.ee")
}

func (p *Provider) Resolve(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	inputURL := strings.TrimSpace(req.URL)
	if inputURL == "" {
		return nil, downloader.ErrInvalidURL
	}
	if !p.MatchesURL(inputURL) {
		return nil, downloader.ErrInvalidURL
	}

	official, officialErr := p.queryOfficial(ctx, inputURL)

	nowm, nowmErr := p.queryNoWatermark(ctx, inputURL)
	if nowmErr == nil && nowm != nil && len(nowm.Formats) > 0 {
		if officialErr == nil && official != nil {
			if nowm.Title == "" {
				nowm.Title = official.Title
			}
			if nowm.Thumbnail == "" {
				nowm.Thumbnail = official.Thumbnail
			}
			if nowm.DurationMs == 0 {
				nowm.DurationMs = official.DurationMs
			}
			if official.Metadata != nil {
				if nowm.Metadata == nil {
					nowm.Metadata = map[string]string{}
				}
				for k, v := range official.Metadata {
					if _, exists := nowm.Metadata[k]; !exists {
						nowm.Metadata[k] = v
					}
				}
			}
		}
		nowm.Platform = downloader.PlatformShopee
		nowm.URL = inputURL
		return nowm, nil
	}

	if officialErr == nil && official != nil && len(official.Formats) > 0 {
		official.Platform = downloader.PlatformShopee
		official.URL = inputURL
		return official, nil
	}

	if officialErr != nil && stderrors.Is(officialErr, downloader.ErrMediaNotFound) {
		return nil, officialErr
	}
	if nowmErr != nil {
		return nil, nowmErr
	}
	return nil, downloader.ErrProviderUnavailable
}

func (p *Provider) queryOfficial(ctx context.Context, inputURL string) (*downloader.DownloadResult, error) {
	loc, err := p.resolveLocation(ctx, inputURL)
	if err != nil {
		return nil, err
	}

	pageURL := loc
	if redir := queryParam(pageURL, "redir"); redir != "" {
		pageURL = redir
	}

	body, _, status, err := p.do(ctx, http.MethodGet, pageURL, map[string]string{
		"user-agent": p.userAgent,
		"accept":     "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	}, nil, officialTimeout)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, classifyHTTPStatus(status)
	}

	next := parseNextData(body)
	if next == nil {
		return nil, downloader.ErrProviderInvalidResponse
	}

	media := nestedMap(next, "props", "pageProps", "mediaInfo")
	video := asMap(media["video"])
	watermark := str(video["watermarkVideoUrl"])
	if watermark == "" {
		return nil, downloader.ErrMediaNotFound
	}

	result := &downloader.DownloadResult{
		Title:      str(video["caption"]),
		Thumbnail:  str(video["watermarkCoverUrl"]),
		DurationMs: int64(num(video["duration"])),
		Type:       downloader.MediaVideo,
		Formats: []downloader.Format{
			{Type: downloader.MediaVideo, Quality: "watermark", URL: watermark},
		},
	}

	meta := map[string]string{}
	if author := nestedStr(media, "userInfo", "videoUserName"); author != "" {
		meta["author"] = author
	}
	if count := asMap(media["count"]); count != nil {
		meta["like_count"] = strconv.FormatInt(int64(num(count["likeCount"])), 10)
		meta["comment_count"] = strconv.FormatInt(int64(num(count["commentCount"])), 10)
	}
	if len(meta) > 0 {
		result.Metadata = meta
	}

	return result, nil
}

func (p *Provider) resolveLocation(ctx context.Context, target string) (string, error) {
	if officialTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, officialTimeout)
		defer cancel()
	}

	firstLoc := ""
	client := *p.client
	client.Timeout = officialTimeout
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if firstLoc == "" && len(via) > 0 {
			firstLoc = req.URL.String()
		}
		if len(via) >= 10 {
			return stderrors.New("too many redirects")
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return "", stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	req.Header.Set("User-Agent", p.userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", classifyClientError(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if firstLoc == "" {
		return target, nil
	}
	return firstLoc, nil
}

func (p *Provider) queryNoWatermark(ctx context.Context, inputURL string) (*downloader.DownloadResult, error) {
	proxies, err := p.scrapeLiveHTTPProxies(ctx)
	if err != nil {
		return nil, err
	}

	live := p.pickLiveProxies(ctx, proxies)
	if len(live) == 0 {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, stderrors.New("no live proxies"))
	}

	var lastErr error
	for _, proxy := range live {
		raw, err := p.extractViaProxy(ctx, inputURL, proxy)
		if err != nil {
			lastErr = err
			continue
		}

		items := pickMedia(raw)
		if len(items) == 0 {
			lastErr = downloader.ErrMediaNotFound
			continue
		}

		return buildResult(items), nil
	}

	return nil, stderrors.Join(downloader.ErrProviderUnavailable, lastErr)
}

func (p *Provider) scrapeLiveHTTPProxies(ctx context.Context) ([]string, error) {
	body, _, status, err := p.do(ctx, http.MethodGet, p.proxyListURL, map[string]string{
		"user-agent": p.userAgent,
		"accept":     "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	}, nil, proxyListTimeout)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, classifyHTTPStatus(status)
	}

	live := make([]string, 0)
	for _, m := range reProxyRow.FindAllStringSubmatch(body, -1) {
		typ := strings.ToUpper(strings.TrimSpace(m[1]))
		addr := strings.TrimSpace(m[2])
		up := strings.Contains(m[3], "✅")
		if up && typ == "HTTP" && reIPPort.MatchString(addr) {
			live = append(live, addr)
		}
	}

	if len(live) == 0 {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, stderrors.New("proxy list has no live HTTP proxies"))
	}

	shuffleStrings(live)
	return live, nil
}

func (p *Provider) pickLiveProxies(ctx context.Context, proxies []string) []string {
	pool := proxies
	if len(pool) > p.maxValidate {
		pool = pool[:p.maxValidate]
	}

	live := make([]string, 0, p.maxLiveExtract)
	for i := 0; i < len(pool); i += p.probeBatch {
		end := i + p.probeBatch
		if end > len(pool) {
			end = len(pool)
		}
		batch := pool[i:end]

		results := make([]string, len(batch))
		var wg sync.WaitGroup
		for j, proxy := range batch {
			wg.Add(1)
			go func(idx int, px string) {
				defer wg.Done()
				if p.isProxyAlive(ctx, px) {
					results[idx] = px
				}
			}(j, proxy)
		}
		wg.Wait()

		for _, px := range results {
			if px != "" {
				live = append(live, px)
				if len(live) >= p.maxLiveExtract {
					return live
				}
			}
		}
	}
	return live
}

func (p *Provider) isProxyAlive(ctx context.Context, proxy string) bool {
	body, status, err := p.doViaProxy(ctx, http.MethodGet, p.probeURL, map[string]string{
		"user-agent": p.userAgent,
		"accept":     "*/*",
	}, nil, proxy, probeTimeout)
	if err != nil {
		return false
	}
	return status == http.StatusOK && (strings.Contains(body, "origin") || strings.Contains(body, "ip"))
}

func (p *Provider) extractViaProxy(ctx context.Context, videoURL, proxy string) (map[string]interface{}, error) {
	form := url.Values{}
	form.Set("url", videoURL)

	body, status, err := p.doViaProxy(ctx, http.MethodPost, p.extractURL, map[string]string{
		"user-agent":   p.userAgent,
		"origin":       "https://shopeenowatermark.com",
		"referer":      "https://shopeenowatermark.com/",
		"content-type": "application/x-www-form-urlencoded",
		"accept":       "application/json, text/plain, */*",
	}, []byte(form.Encode()), proxy, extractTimeout)
	if err != nil {
		return nil, err
	}

	if status == http.StatusTooManyRequests || status == http.StatusForbidden {
		return nil, downloader.ErrProviderUnavailable
	}

	trimmed := strings.TrimSpace(body)
	if trimmed == "" || strings.HasPrefix(trimmed, "<") {
		return nil, downloader.ErrProviderInvalidResponse
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &data); err != nil {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}

	if b, ok := data["success"].(bool); ok && !b {
		msg := str(data["error"])
		if msg == "" {
			msg = "extract failed"
		}
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, stderrors.New(msg))
	}

	return data, nil
}

type mediaItem struct {
	kind    string
	quality string
	url     string
}

func pickMedia(raw map[string]interface{}) []mediaItem {
	items := make([]mediaItem, 0)
	push := func(kind, quality string, v interface{}) {
		if u := str(v); u != "" {
			items = append(items, mediaItem{kind: kind, quality: quality, url: u})
		}
	}

	if raw == nil {
		return items
	}

	data := asMap(raw["data"])
	if data == nil {
		data = asMap(raw["result"])
	}
	if data == nil {
		data = raw
	}

	push("video", "no_watermark", orFirstStr(data["no_watermark"], data["nowatermark"], data["video_url"], data["url"]))
	push("video", "watermark", orFirstStr(data["watermark"], data["watermark_url"]))
	push("photo", "cover", orFirstStr(data["cover"], data["thumbnail"]))

	if videos := sliceOf(data["videos"]); len(videos) > 0 {
		for _, v := range videos {
			if s, ok := v.(string); ok && s != "" {
				push("video", "video", s)
				continue
			}
			if m := asMap(v); m != nil {
				q := str(m["quality"])
				if q == "" {
					q = "video"
				}
				push("video", q, m["url"])
			}
		}
	}

	if len(items) == 0 {
		for k, v := range data {
			if u := str(v); u != "" && strings.HasPrefix(u, "http") && reMediaURL.MatchString(u) {
				push("video", k, u)
			}
		}
	}

	return items
}

func buildResult(items []mediaItem) *downloader.DownloadResult {
	formats := make([]downloader.Format, 0, len(items))
	for _, it := range items {
		formats = append(formats, downloader.Format{
			Type:    mediaType(it.kind),
			URL:     it.url,
			Quality: it.quality,
		})
	}
	if len(formats) == 0 {
		return nil
	}

	result := &downloader.DownloadResult{Formats: formats}

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

func (p *Provider) do(ctx context.Context, method, target string, headers map[string]string, body []byte, timeout time.Duration) (string, string, int, error) {
	return p.doWithClient(ctx, p.client, method, target, headers, body, timeout)
}

func (p *Provider) doViaProxy(ctx context.Context, method, target string, headers map[string]string, body []byte, proxy string, timeout time.Duration) (string, int, error) {
	b, _, status, err := p.doWithClient(ctx, p.proxyClient(proxy), method, target, headers, body, timeout)
	return b, status, err
}

func (p *Provider) doWithClient(ctx context.Context, client *http.Client, method, target string, headers map[string]string, body []byte, timeout time.Duration) (string, string, int, error) {
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

	resp, err := client.Do(req)
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

func (p *Provider) proxyClient(proxy string) *http.Client {
	u, err := url.Parse("http://" + proxy)
	if err != nil {
		return p.client
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(u)
	return &http.Client{Transport: transport, Timeout: p.client.Timeout}
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

var (
	reProxyRow = regexp.MustCompile(`<tr>\s*<td>([^<]+)</td>\s*<td>[^<]*</td>\s*<td><code>([^<]+)</code></td>\s*<td>([^<]+)</td>`)
	reIPPort   = regexp.MustCompile(`^\d{1,3}(?:\.\d{1,3}){3}:\d+$`)
	reNextData = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__"[^>]*>(\{.*?\})</script>`)
	reMediaURL = regexp.MustCompile(`(?i)mp4|susercontent|vod\.`)
)

func firstMatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func parseNextData(html string) map[string]interface{} {
	raw := firstMatch(reNextData, html)
	if raw == "" {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
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

func nestedStr(v interface{}, keys ...string) string {
	cur := v
	for _, k := range keys {
		m := asMap(cur)
		if m == nil {
			return ""
		}
		cur = m[k]
	}
	return str(cur)
}

func orFirstStr(values ...interface{}) string {
	for _, v := range values {
		if u := str(v); u != "" {
			return u
		}
	}
	return ""
}

func queryParam(raw, key string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Query().Get(key)
}

func shuffleStrings(list []string) {
	rand.Shuffle(len(list), func(i, j int) {
		list[i], list[j] = list[j], list[i]
	})
}
