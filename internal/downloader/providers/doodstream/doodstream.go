package doodstream

import (
	"context"
	stderrors "errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"rest-api/internal/downloader"
)

const (
	canonHost = "playmogo.com"
	defaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	maxBody   = 8 << 20
)

var doodHosts = map[string]struct{}{
	"dood.watch": {}, "doodstream.com": {}, "dood.to": {}, "dood.so": {}, "dood.cx": {},
	"dood.la": {}, "dood.ws": {}, "dood.sh": {}, "doodstream.co": {}, "dood.pm": {},
	"dood.wf": {}, "dood.re": {}, "dood.yt": {}, "dooood.com": {}, "dood.stream": {},
	"ds2play.com": {}, "doods.pro": {}, "ds2video.com": {}, "d0o0d.com": {}, "do0od.com": {},
	"d0000d.com": {}, "d000d.com": {}, "dood.li": {}, "dood.work": {}, "dooodster.com": {},
	"vidply.com": {}, "all3do.com": {}, "do7go.com": {}, "doodcdn.io": {}, "doply.net": {},
	"vide0.net": {}, "vvide0.com": {}, "d-s.io": {}, "dsvplay.com": {}, "myvidplay.com": {},
	"playmogo.com": {},
}

var doodHostRe = regexp.MustCompile(`(?i)(?:doo*0*d|ds[2v](?:play|video)|vidply|vide0|vvide0|all3do|do7go|doply|d-s|playmogo|myvidplay)\.`)

type Config struct {
	Timeout    time.Duration
	UserAgent  string
	HTTPClient *http.Client
	ChromeBin  string
}

func DefaultConfig() Config {
	return Config{Timeout: 45 * time.Second, UserAgent: defaultUA}
}

type Provider struct {
	userAgent string
	client    *http.Client
	chromeBin string
}

var _ downloader.Provider = (*Provider)(nil)
var _ downloader.URLMatcher = (*Provider)(nil)

func New() *Provider { return NewWithConfig(DefaultConfig()) }

func NewWithConfig(cfg Config) *Provider {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUA
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	bin := cfg.ChromeBin
	if bin == "" {
		bin = os.Getenv("CHROME_BIN")
	}
	if bin == "" {
		bin = "google-chrome"
	}
	return &Provider{userAgent: cfg.UserAgent, client: client, chromeBin: bin}
}

func (p *Provider) Name() string { return "doodstream" }

func (p *Provider) Platform() downloader.Platform { return downloader.PlatformDoodstream }

func (p *Provider) Type() downloader.ProviderType { return downloader.ProviderExternalAPI }

func (p *Provider) MatchesURL(raw string) bool {
	text := strings.TrimSpace(raw)
	if text == "" || isCode(text) {
		return false
	}
	host, _, err := parseInput(text)
	if err != nil {
		return false
	}
	return isDoodHost(host)
}

func parseInput(raw string) (host, code string, err error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", "", downloader.ErrInvalidURL
	}
	if m := regexp.MustCompile(`https?://[^\s]+`).FindString(text); m != "" {
		text = strings.TrimRight(m, ")].,")
	}
	if isCode(text) {
		return canonHost, strings.ToLower(text), nil
	}
	u, perr := url.Parse(text)
	if perr != nil {
		return "", "", downloader.ErrInvalidURL
	}
	host = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return "", "", downloader.ErrInvalidURL
	}
	return host, strings.ToLower(parts[len(parts)-1]), nil
}

func isCode(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func isDoodHost(host string) bool {
	if _, ok := doodHosts[host]; ok {
		return true
	}
	return doodHostRe.MatchString(host)
}

func (p *Provider) Resolve(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	host, code, err := parseInput(req.URL)
	if err != nil {
		return nil, err
	}
	if host != "" && !isDoodHost(host) {
		return nil, downloader.ErrInvalidURL
	}

	scheme := "https"
	port := ""
	if u0, perr := url.Parse(strings.TrimSpace(req.URL)); perr == nil && u0.Host != "" {
		if u0.Scheme == "http" || u0.Scheme == "https" {
			scheme = u0.Scheme
		}
		if _, p, err := net.SplitHostPort(u0.Host); err == nil {
			port = p
		}
	}

	hosts := []string{host}
	if scheme == "https" && host != canonHost {
		hosts = unique(host, canonHost)
	}
	var pageHTML, pageURL, embedURL string
	var lastErr error
	for _, h := range hosts {
		authority := h
		if port != "" && h == host {
			authority = net.JoinHostPort(h, port)
		}
		u := scheme + "://" + authority + "/d/" + code
		html, gerr := p.getPage(ctx, u, nil)
		if gerr != nil {
			lastErr = gerr
			continue
		}
		if !blocked(html) && strings.Contains(strings.ToLower(html), "<title>") {
			pageHTML = html
			pageURL = u
			embedURL = scheme + "://" + authority + "/e/" + code
			break
		}
		lastErr = downloader.ErrProviderUnavailable
	}
	if pageHTML == "" {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, downloader.ErrProviderUnavailable
	}

	embedHTML, _ := p.getPage(ctx, embedURL, nil)
	title := firstNonEmpty(meta(pageHTML, "og:title"), titleOf(pageHTML))
	preview := meta(pageHTML, "og:image")
	splash := firstNonEmpty(meta(embedHTML, "og:image"), preview)
	video, verr := p.resolveVideo(ctx, embedHTML, embedURL)
	if verr != nil {
		return nil, verr
	}
	if video == "" {
		return nil, downloader.ErrMediaNotFound
	}

	originURL, _ := url.Parse(pageURL)
	origin := originURL.Scheme + "://" + originURL.Host
	text := visible(pageHTML)

	result := &downloader.DownloadResult{
		Platform:  downloader.PlatformDoodstream,
		URL:       pageURL,
		Title:     title,
		Type:      downloader.MediaVideo,
		Thumbnail: firstNonEmpty(preview, splash),
		Formats: []downloader.Format{{
			Type:    downloader.MediaVideo,
			URL:     video,
			Quality: "cdn",
			Ext:     "mp4",
		}},
		Metadata: map[string]string{
			"host":       originURL.Hostname(),
			"file_code":  code,
			"page":       pageURL,
			"embed":      embedURL,
			"referer":    origin + "/",
			"origin":     origin,
			"user_agent": p.userAgent,
			"duration":   pick(text, `\b(\d{1,2}:\d{2}(?::\d{2})?)\b`),
			"size":       pick(text, `(?i)\b(\d+(?:\.\d+)?\s*(?:KB|MB|GB|TB))\b`),
			"source":     "doodstream",
		},
	}
	return result, nil
}

func (p *Provider) resolveVideo(ctx context.Context, embedHTML, embedURL string) (string, error) {
	if strings.TrimSpace(embedHTML) == "" {
		return "", downloader.ErrMediaNotFound
	}
	m := regexp.MustCompile(`/pass_md5/[^'"]+`).FindString(embedHTML)
	if m == "" {
		if blocked(embedHTML) {
			return "", downloader.ErrProviderUnavailable
		}
		return "", downloader.ErrMediaNotFound
	}
	passURL, err := url.Parse(embedURL)
	if err != nil {
		return "", downloader.ErrProviderInvalidResponse
	}
	rel, err := url.Parse(m)
	if err != nil {
		return "", downloader.ErrProviderInvalidResponse
	}
	full := passURL.ResolveReference(rel).String()
	token := m[strings.LastIndex(m, "/")+1:]
	raw, err := p.getPage(ctx, full, map[string]string{
		"Referer":          embedURL,
		"X-Requested-With": "XMLHttpRequest",
	})
	if err != nil {
		return "", err
	}
	base := strings.TrimSpace(strings.Split(raw, "\n")[0])
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") || blocked(base) {
		return "", downloader.ErrProviderInvalidResponse
	}
	return base + rand10() + "?token=" + token + "&expiry=" + itoa(time.Now().UnixMilli()), nil
}

func (p *Provider) getPage(ctx context.Context, pageURL string, extra map[string]string) (string, error) {
	html, err := p.fetchHTTP(ctx, pageURL, extra)
	if err == nil && !blocked(html) {
		return html, nil
	}
	dumped, derr := p.chromeDump(ctx, pageURL)
	if derr == nil && dumped != "" && !blocked(dumped) {
		return dumped, nil
	}
	if err != nil {
		return "", err
	}
	if blocked(html) {
		return html, downloader.ErrProviderUnavailable
	}
	return html, nil
}

func (p *Provider) fetchHTTP(ctx context.Context, pageURL string, extra map[string]string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", "https://"+canonHost+"/")
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", classifyClientError(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", classifyClientError(err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", downloader.ErrProviderUnavailable
	}
	if resp.StatusCode == http.StatusForbidden {
		return string(raw), downloader.ErrProviderUnavailable
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", downloader.ErrMediaNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return string(raw), downloader.ErrProviderInvalidResponse
	}
	return string(raw), nil
}

func (p *Provider) chromeDump(ctx context.Context, pageURL string) (string, error) {
	bin := p.chromeBin
	if bin == "" {
		return "", downloader.ErrProviderUnavailable
	}
	if _, err := exec.LookPath(bin); err != nil {
		return "", downloader.ErrProviderUnavailable
	}
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--virtual-time-budget=25000",
		"--timeout=30000",
		"--user-agent=" + p.userAgent,
		"--dump-dom",
		pageURL,
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", downloader.ErrProviderUnavailable
	}
	return string(out), nil
}

func blocked(html string) bool {
	head := html
	if len(head) > 1500 {
		head = head[:1500]
	}
	return head == "" || regexp.MustCompile(`(?i)Just a moment|cf-mitigated|Performing security verification`).MatchString(head)
}

func meta(html, name string) string {
	re1 := regexp.MustCompile(`(?i)<meta[^>]+(?:name|property)=["']` + regexp.QuoteMeta(name) + `["'][^>]+content=["']([^"']+)["']`)
	if m := re1.FindStringSubmatch(html); len(m) == 2 {
		return m[1]
	}
	re2 := regexp.MustCompile(`(?i)<meta[^>]+content=["']([^"']+)["'][^>]+(?:name|property)=["']` + regexp.QuoteMeta(name) + `["']`)
	if m := re2.FindStringSubmatch(html); len(m) == 2 {
		return m[1]
	}
	return ""
}

func titleOf(html string) string {
	m := regexp.MustCompile(`(?i)<title>([^<]+)</title>`).FindStringSubmatch(html)
	if len(m) != 2 {
		return ""
	}
	return strings.TrimSpace(regexp.MustCompile(`(?i)\s*-\s*(DoodStream|Playmogo|Dood).*$`).ReplaceAllString(m[1], ""))
}

func visible(html string) string {
	s := regexp.MustCompile(`(?is)<script[\s\S]*?</script>`).ReplaceAllString(html, " ")
	s = regexp.MustCompile(`(?is)<style[\s\S]*?</style>`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func pick(text, expr string) string {
	m := regexp.MustCompile(expr).FindStringSubmatch(text)
	if len(m) >= 2 {
		return m[1]
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

func unique(vals ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func rand10() string {
	const abc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 10)
	for i := range b {
		b[i] = abc[rand.Intn(len(abc))]
	}
	return string(b)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
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
