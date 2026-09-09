package pinterest

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"rest-api/internal/downloader"
)

const (
	defaultPinssaver = "https://pinssaver.com/api/pin"
	defaultPintsave  = "https://pintsave.net/api/fetch-media"
	defaultUA        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
	maxBody          = 8 << 20
)

type Config struct {
	PinssaverURL    string
	PintsaveURL     string
	OfficialBaseURL string
	Timeout         time.Duration
	UserAgent       string
	HTTPClient      *http.Client
}

func DefaultConfig() Config {
	return Config{PinssaverURL: defaultPinssaver, PintsaveURL: defaultPintsave, Timeout: 30 * time.Second, UserAgent: defaultUA}
}

type Provider struct {
	pinssaver string
	pintsave  string
	official  string
	userAgent string
	client    *http.Client
}

var _ downloader.Provider = (*Provider)(nil)
var _ downloader.URLMatcher = (*Provider)(nil)

func New() *Provider { return NewWithConfig(DefaultConfig()) }

func NewWithConfig(cfg Config) *Provider {
	if cfg.PinssaverURL == "" {
		cfg.PinssaverURL = defaultPinssaver
	}
	if cfg.PintsaveURL == "" {
		cfg.PintsaveURL = defaultPintsave
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUA
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		}}
	}
	return &Provider{
		pinssaver: cfg.PinssaverURL,
		pintsave:  cfg.PintsaveURL,
		official:  strings.TrimRight(cfg.OfficialBaseURL, "/"),
		userAgent: cfg.UserAgent,
		client:    client,
	}
}

func (p *Provider) Name() string { return "pinterest" }

func (p *Provider) Platform() downloader.Platform { return downloader.PlatformPinterest }

func (p *Provider) Type() downloader.ProviderType { return downloader.ProviderExternalAPI }

func (p *Provider) MatchesURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	for _, suffix := range []string{"pinterest.com", "pin.it", "pinterest.co.uk", "pinterest.fr", "pinterest.de", "pinterest.jp", "pinterest.ca"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func (p *Provider) Resolve(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	input := strings.TrimSpace(req.URL)
	if input == "" {
		return nil, downloader.ErrInvalidURL
	}
	canonical, err := p.normalize(ctx, input)
	if err != nil {
		return nil, err
	}

	strategies := []func(context.Context, string) (*downloader.DownloadResult, error){
		p.fromPinssaver,
		p.fromOfficial,
		p.fromPintsave,
	}
	var lastErr error
	for _, run := range strategies {
		res, rerr := run(ctx, canonical)
		if rerr == nil && res != nil && len(res.Formats) > 0 {
			res.Platform = downloader.PlatformPinterest
			res.URL = input
			return res, nil
		}
		if rerr != nil {
			lastErr = rerr
		}
	}
	if lastErr == nil {
		lastErr = downloader.ErrMediaNotFound
	}
	return nil, lastErr
}

func (p *Provider) normalize(ctx context.Context, raw string) (string, error) {
	if regexp.MustCompile(`^\d{8,}$`).MatchString(raw) {
		return "https://www.pinterest.com/pin/" + raw + "/", nil
	}
	u := raw
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return "", downloader.ErrInvalidURL
	}
	host := strings.ToLower(parsed.Hostname())
	if m := regexp.MustCompile(`/pin/(\d+)`).FindStringSubmatch(parsed.Path); len(m) == 2 {
		return "https://www.pinterest.com/pin/" + m[1] + "/", nil
	}
	if host == "pin.it" || strings.HasSuffix(host, ".pin.it") {
		resolved, rerr := p.resolveShort(ctx, parsed.String())
		if rerr != nil {
			return parsed.String(), nil
		}
		return resolved, nil
	}
	return parsed.String(), nil
}

func (p *Provider) resolveShort(ctx context.Context, shortURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, shortURL, nil)
	if err != nil {
		return "", classifyClientError(err)
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "text/html")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", classifyClientError(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	final := resp.Request.URL.String()
	if m := regexp.MustCompile(`/pin/(\d+)`).FindStringSubmatch(final); len(m) == 2 {
		return "https://www.pinterest.com/pin/" + m[1] + "/", nil
	}
	return "", downloader.ErrMediaNotFound
}

func (p *Provider) fromPinssaver(ctx context.Context, pinURL string) (*downloader.DownloadResult, error) {
	u, err := url.Parse(p.pinssaver)
	if err != nil {
		return nil, downloader.ErrProviderUnavailable
	}
	q := u.Query()
	q.Set("url", pinURL)
	u.RawQuery = q.Encode()
	body, err := p.get(ctx, u.String(), map[string]string{
		"Accept": "application/json",
		"Origin": "https://pinssaver.com",
	})
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}
	status := anyToInt(raw["status"])
	if status != 0 && status != 200 {
		return nil, downloader.ErrMediaNotFound
	}
	data, _ := raw["data"].(map[string]any)
	if data == nil {
		return nil, downloader.ErrMediaNotFound
	}
	formats := formatsFromSrc(data["src"])
	if v, _ := data["video"].(string); v != "" {
		formats = prepend(formats, downloader.Format{Type: downloader.MediaVideo, URL: v, Quality: "video", Ext: "mp4"})
	}
	if v, _ := data["video_url"].(string); v != "" {
		formats = prepend(formats, downloader.Format{Type: downloader.MediaVideo, URL: v, Quality: "video", Ext: "mp4"})
	}
	if imgs, ok := data["images"].([]any); ok {
		for _, img := range imgs {
			switch t := img.(type) {
			case string:
				formats = append(formats, imageFmt(t, "image"))
			case map[string]any:
				u, _ := t["url"].(string)
				if u == "" {
					u, _ = t["src"].(string)
				}
				if u == "" {
					u, _ = t["orig"].(string)
				}
				if u != "" {
					formats = append(formats, imageFmt(u, "image"))
				}
			}
		}
	}
	formats = uniqueFormats(formats)
	if len(formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	title, _ := data["title"].(string)
	user, _ := data["username"].(string)
	res := &downloader.DownloadResult{
		Platform: downloader.PlatformPinterest,
		URL:      pinURL,
		Title:    title,
		Formats:  formats,
		Metadata: map[string]string{"source": "pinssaver"},
	}
	if user != "" {
		res.Metadata["username"] = user
	}
	res.Type = sameType(formats)
	if formats[0].Type == downloader.MediaImage {
		res.Thumbnail = formats[0].URL
	}
	return res, nil
}

func (p *Provider) fromPintsave(ctx context.Context, pinURL string) (*downloader.DownloadResult, error) {
	form := url.Values{"url": {pinURL}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.pintsave, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://pintsave.net")
	req.Header.Set("Referer", "https://pintsave.net/download")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, classifyClientError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, classifyClientError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, classifyHTTPStatus(resp.StatusCode)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}
	if raw["error"] != nil || raw["detail"] != nil {
		return nil, downloader.ErrMediaNotFound
	}
	formats := collectPinURLs(raw)
	if media, ok := raw["media"].([]any); ok {
		for _, item := range media {
			m, _ := item.(map[string]any)
			if m == nil {
				continue
			}
			u, _ := m["url"].(string)
			if u == "" {
				continue
			}
			typ, _ := m["type"].(string)
			if strings.Contains(strings.ToLower(typ), "video") || looksVideo(u) {
				formats = append(formats, downloader.Format{Type: downloader.MediaVideo, URL: u, Quality: "video", Ext: "mp4"})
			} else {
				formats = append(formats, imageFmt(u, "image"))
			}
		}
	}
	formats = uniqueFormats(formats)
	if len(formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	title, _ := raw["title"].(string)
	if title == "" {
		title, _ = raw["description"].(string)
	}
	res := &downloader.DownloadResult{
		Platform: downloader.PlatformPinterest,
		URL:      pinURL,
		Title:    title,
		Formats:  formats,
		Type:     sameType(formats),
		Metadata: map[string]string{"source": "pintsave"},
	}
	if u, _ := raw["creator_username"].(string); u != "" {
		res.Metadata["username"] = u
	}
	return res, nil
}

func (p *Provider) fromOfficial(ctx context.Context, pinURL string) (*downloader.DownloadResult, error) {
	page := pinURL
	if p.official != "" {
		if u, err := url.Parse(pinURL); err == nil {
			page = p.official + u.Path
		} else {
			page = p.official
		}
	}
	body, err := p.get(ctx, page, map[string]string{"Accept": "text/html"})
	if err != nil {
		return nil, err
	}
	html := string(body)
	formats := make([]downloader.Format, 0)
	title := metaContent(html, "og:title")
	if v := metaContent(html, "og:video"); v != "" {
		formats = append(formats, downloader.Format{Type: downloader.MediaVideo, URL: v, Quality: "og", Ext: "mp4"})
	}
	if img := metaContent(html, "og:image"); img != "" && strings.Contains(img, "pinimg") {
		formats = append(formats, imageFmt(upgradePinimg(img), "og"))
	}
	for _, id := range []string{"__PWS_DATA__", "__PWS_INITIAL_PROPS__"} {
		blob := scriptJSON(html, id)
		if blob == nil {
			continue
		}
		for _, u := range collectStrings(blob) {
			if !isPinMedia(u) {
				continue
			}
			if looksVideo(u) {
				formats = append(formats, downloader.Format{Type: downloader.MediaVideo, URL: u, Quality: "pws", Ext: "mp4"})
			} else {
				formats = append(formats, imageFmt(upgradePinimg(u), "pws"))
			}
		}
	}
	formats = uniqueFormats(formats)
	if len(formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	return &downloader.DownloadResult{
		Platform:  downloader.PlatformPinterest,
		URL:       pinURL,
		Title:     title,
		Type:      sameType(formats),
		Thumbnail: firstImage(formats),
		Formats:   formats,
		Metadata:  map[string]string{"source": "official"},
	}, nil
}

func (p *Provider) get(ctx context.Context, rawURL string, extra map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "*/*")
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := p.client.Do(req)
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
	return body, nil
}

func formatsFromSrc(src any) []downloader.Format {
	m, _ := src.(map[string]any)
	if m == nil {
		return nil
	}
	order := []string{"orig", "originals", "original", "736x", "564x", "474x", "236x"}
	out := make([]downloader.Format, 0, len(m))
	seen := map[string]struct{}{}
	push := func(key, u string) {
		if u == "" {
			return
		}
		if _, ok := seen[u]; ok {
			return
		}
		seen[u] = struct{}{}
		if looksVideo(u) || strings.Contains(strings.ToLower(key), "video") {
			out = append(out, downloader.Format{Type: downloader.MediaVideo, URL: u, Quality: key, Ext: "mp4"})
			return
		}
		out = append(out, imageFmt(u, key))
	}
	for _, k := range order {
		if s, _ := m[k].(string); s != "" {
			push(k, s)
		}
	}
	for k, v := range m {
		s, _ := v.(string)
		if strings.HasPrefix(s, "http") {
			push(k, s)
		}
	}
	return out
}

func collectPinURLs(v any) []downloader.Format {
	out := make([]downloader.Format, 0)
	var walk func(any)
	walk = func(n any) {
		switch t := n.(type) {
		case string:
			if isPinMedia(t) {
				if looksVideo(t) {
					out = append(out, downloader.Format{Type: downloader.MediaVideo, URL: t, Quality: "pintsave", Ext: "mp4"})
				} else {
					out = append(out, imageFmt(t, "pintsave"))
				}
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		case map[string]any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(v)
	return out
}

func collectStrings(v any) []string {
	out := make([]string, 0)
	var walk func(any)
	walk = func(n any) {
		switch t := n.(type) {
		case string:
			out = append(out, t)
		case []any:
			for _, x := range t {
				walk(x)
			}
		case map[string]any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(v)
	return out
}

func scriptJSON(html, id string) map[string]any {
	re := regexp.MustCompile(`<script[^>]+id="` + regexp.QuoteMeta(id) + `"[^>]*>([^<]+)</script>`)
	m := re.FindStringSubmatch(html)
	if len(m) != 2 {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(m[1]), &raw); err != nil {
		return nil
	}
	return raw
}

func metaContent(html, prop string) string {
	re := regexp.MustCompile(`(?i)<meta[^>]+property="` + regexp.QuoteMeta(prop) + `"[^>]+content="([^"]*)"`)
	m := re.FindStringSubmatch(html)
	if len(m) == 2 {
		return m[1]
	}
	re2 := regexp.MustCompile(`(?i)<meta[^>]+content="([^"]*)"[^>]+property="` + regexp.QuoteMeta(prop) + `"`)
	m = re2.FindStringSubmatch(html)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func upgradePinimg(u string) string {
	re := regexp.MustCompile(`i\.pinimg\.com/(?:\d+x\d+|\d+x)/`)
	return re.ReplaceAllString(u, "i.pinimg.com/originals/")
}

func imageFmt(u, q string) downloader.Format {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(strings.Split(u, "?")[0]), "."))
	if ext == "" {
		ext = "jpg"
	}
	return downloader.Format{Type: downloader.MediaImage, URL: u, Quality: q, Ext: ext}
}

func looksVideo(u string) bool {
	low := strings.ToLower(u)
	return strings.Contains(low, ".mp4") || strings.Contains(low, "v.pinimg.com")
}

func isPinMedia(u string) bool {
	low := strings.ToLower(u)
	return strings.HasPrefix(low, "http") && (strings.Contains(low, "pinimg.com") || strings.Contains(low, ".mp4"))
}

func uniqueFormats(in []downloader.Format) []downloader.Format {
	seen := map[string]struct{}{}
	out := make([]downloader.Format, 0, len(in))
	for _, f := range in {
		key := strings.Split(f.URL, "?")[0]
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	return out
}

func prepend(list []downloader.Format, f downloader.Format) []downloader.Format {
	return append([]downloader.Format{f}, list...)
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

func firstImage(formats []downloader.Format) string {
	for _, f := range formats {
		if f.Type == downloader.MediaImage {
			return f.URL
		}
	}
	return ""
}

func anyToInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
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

func classifyHTTPStatus(status int) error {
	if status == http.StatusTooManyRequests || status >= 500 {
		return downloader.ErrProviderUnavailable
	}
	if status == http.StatusNotFound {
		return downloader.ErrMediaNotFound
	}
	return downloader.ErrProviderInvalidResponse
}
