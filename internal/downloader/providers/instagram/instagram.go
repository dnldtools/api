package instagram

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/dop251/goja"

	"rest-api/internal/downloader"
)

const (
	defaultRelay = "https://cors.siputzx.my.id/"

	defaultSnapinsta = "https://snapinsta.ai/"

	defaultIGUA = "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36"

	defaultBrowserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	defaultAppID = "936619743392459"

	docID = "8845758582119845"

	maxResponseBytes = 5 << 20
)

type Config struct {
	RelayBaseURL string

	SnapinstaBaseURL string

	Timeout time.Duration

	IGUserAgent string

	BrowserUserAgent string

	HTTPClient *http.Client
}

func DefaultConfig() Config {
	return Config{
		RelayBaseURL:     defaultRelay,
		SnapinstaBaseURL: defaultSnapinsta,
		Timeout:          30 * time.Second,
		IGUserAgent:      defaultIGUA,
		BrowserUserAgent: defaultBrowserUA,
	}
}

type Provider struct {
	relay     string
	snapinsta string
	igUA      string
	browserUA string
	client    *http.Client
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
	if cfg.SnapinstaBaseURL == "" {
		cfg.SnapinstaBaseURL = defaultSnapinsta
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.IGUserAgent == "" {
		cfg.IGUserAgent = defaultIGUA
	}
	if cfg.BrowserUserAgent == "" {
		cfg.BrowserUserAgent = defaultBrowserUA
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &Provider{
		relay:     strings.TrimRight(cfg.RelayBaseURL, "/"),
		snapinsta: strings.TrimRight(cfg.SnapinstaBaseURL, "/"),
		igUA:      cfg.IGUserAgent,
		browserUA: cfg.BrowserUserAgent,
		client:    client,
	}
}

func (p *Provider) Name() string { return "instagram" }

func (p *Provider) Platform() downloader.Platform { return downloader.PlatformInstagram }

func (p *Provider) Type() downloader.ProviderType { return downloader.ProviderExternalAPI }

func (p *Provider) MatchesURL(u string) bool { return reInstagramURL.MatchString(u) }

func (p *Provider) Resolve(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	inputURL := strings.TrimSpace(req.URL)
	if inputURL == "" {
		return nil, downloader.ErrInvalidURL
	}
	if !p.MatchesURL(inputURL) {
		return nil, downloader.ErrInvalidURL
	}

	shortcode := shortcodeOf(inputURL)
	story := isStory(inputURL)

	if !story {
		if res, err := p.queryOfficial(ctx, inputURL, shortcode); err == nil && res != nil && len(res.Formats) > 0 {
			res.Platform = downloader.PlatformInstagram
			res.URL = inputURL
			return res, nil
		}
	}

	res, err := p.querySnapinsta(ctx, inputURL)
	if err != nil {
		return nil, err
	}
	res.Platform = downloader.PlatformInstagram
	res.URL = inputURL
	return res, nil
}

func (p *Provider) queryOfficial(ctx context.Context, inputURL, shortcode string) (*downloader.DownloadResult, error) {
	if shortcode == "" {
		return nil, downloader.ErrMediaNotFound
	}

	jar := newCookieJar()
	pageURL := "https://www.instagram.com/p/" + shortcode + "/"

	pageBody, status, err := p.do(ctx, http.MethodGet, viaRelay(p.relay, pageURL), map[string]string{
		"user-agent":      p.igUA,
		"accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"accept-language": "id-ID,id;q=0.9,en;q=0.8",
	}, nil, jar, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices || len(pageBody) < 200 {
		return nil, classifyHTTPStatus(status)
	}

	marker := `"code":"` + shortcode + `"`
	idx := strings.Index(pageBody, marker)
	if idx != -1 {
		key := strings.LastIndex(pageBody[:idx], `"xig_polaris_media":`)
		if key != -1 {
			data := extractJSONObject(pageBody, key)
			post := data
			if inner, ok := data["if_not_gated_logged_out"].(map[string]interface{}); ok {
				post = inner
			}
			if items := itemsFromOfficial(post); len(items) > 0 {
				return p.buildResult(post, items), nil
			}
		}
	}

	csrf := firstMatch(reCSRF, pageBody)
	if csrf == "" {
		csrf = jar.get("csrftoken")
	}
	lsd := firstMatch(reLSD, pageBody)
	appID := firstMatch(reAppID, pageBody)
	if appID == "" {
		appID = firstMatch(reAppID2, pageBody)
	}
	if appID == "" {
		appID = defaultAppID
	}

	qs := url.Values{}
	qs.Set("doc_id", docID)
	qs.Set("variables", `{"shortcode":"`+shortcode+`"}`)
	gqlURL := viaRelay(p.relay, "https://www.instagram.com/graphql/query/?"+qs.Encode())

	gqlBody, status, err := p.do(ctx, http.MethodGet, gqlURL, map[string]string{
		"user-agent":  p.igUA,
		"x-ig-app-id": appID,
		"x-csrftoken": csrf,
		"x-fb-lsd":    lsd,
		"accept":      "*/*",
		"referer":     pageURL,
		"cookie":      jar.header(),
	}, nil, jar, 8*time.Second)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, classifyHTTPStatus(status)
	}

	var gql struct {
		Data struct {
			Media map[string]interface{} `json:"xdt_shortcode_media"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(gqlBody), &gql); err != nil {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}

	items := itemsFromOfficial(gql.Data.Media)
	if len(items) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	return p.buildResult(gql.Data.Media, items), nil
}

func (p *Provider) querySnapinsta(ctx context.Context, inputURL string) (*downloader.DownloadResult, error) {
	jar := newCookieJar()

	homeBody, status, err := p.do(ctx, http.MethodGet, p.snapinsta, map[string]string{
		"user-agent": p.browserUA,
		"accept":     "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	}, nil, jar, 8*time.Second)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, downloader.ErrProviderUnavailable
	}
	token := firstMatch(reToken, homeBody)

	form := url.Values{}
	form.Set("url", inputURL)
	form.Set("action", "post")
	form.Set("lang", "en")
	form.Set("token", token)

	postBody, status, err := p.do(ctx, http.MethodPost, p.snapinsta+"/action2.php", map[string]string{
		"user-agent":       p.browserUA,
		"content-type":     "application/x-www-form-urlencoded",
		"referer":          p.snapinsta,
		"x-requested-with": "XMLHttpRequest",
		"cookie":           jar.header(),
	}, []byte(form.Encode()), jar, 15*time.Second)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices || len(postBody) < 100 {
		return nil, downloader.ErrProviderInvalidResponse
	}

	decoded := decodeSnapinsta(postBody)
	if decoded == "" {
		return nil, downloader.ErrProviderInvalidResponse
	}
	if reErrorAPI.MatchString(decoded) && !reRapidCDNApp.MatchString(decoded) {
		return nil, downloader.ErrProviderInvalidResponse
	}

	items := itemsFromSnapinsta(decoded)
	if len(items) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	return p.buildResult(nil, items), nil
}

type mediaItem struct {
	kind  string
	url   string
	thumb string
}

func (p *Provider) buildResult(post map[string]interface{}, items []mediaItem) *downloader.DownloadResult {
	formats := make([]downloader.Format, 0, len(items))
	thumb := ""
	for _, it := range items {
		t := downloader.MediaImage
		if it.kind == "video" {
			t = downloader.MediaVideo
		}
		formats = append(formats, downloader.Format{Type: t, URL: it.url})
		if thumb == "" && it.thumb != "" {
			thumb = it.thumb
		}
	}

	result := &downloader.DownloadResult{
		Title:     captionText(post),
		Thumbnail: thumb,
		Formats:   formats,
	}

	author := captionAuthor(post)
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

func captionAuthor(post map[string]interface{}) string {
	if u := nestedStr(post, "user", "username"); u != "" {
		return u
	}
	return nestedStr(post, "owner", "username")
}

func captionText(post map[string]interface{}) string {
	if c, ok := post["caption"].(string); ok {
		return c
	}
	if cm, ok := post["caption"].(map[string]interface{}); ok {
		if t, ok := cm["text"].(string); ok {
			return t
		}
	}
	edges := nestedSlice(post, "edge_media_to_caption", "edges")
	if len(edges) > 0 {
		return nestedStr(edges[0], "node", "text")
	}
	return ""
}

func itemsFromOfficial(post map[string]interface{}) []mediaItem {
	if post == nil {
		return nil
	}
	var items []mediaItem

	sidecarEdges := nestedSlice(post, "edge_sidecar_to_children", "edges")
	isCarousel := num(post["media_type"]) == 8 ||
		strings.Contains(str(post["__typename"]), "Carousel") ||
		str(post["product_type"]) == "carousel_container" ||
		isSlice(post["carousel_media"]) ||
		len(sidecarEdges) > 0

	push := func(node map[string]interface{}) {
		if node == nil {
			return
		}
		isVideo := num(node["media_type"]) == 2 ||
			boolVal(node["is_video"]) ||
			str(node["video_url"]) != "" ||
			strings.Contains(str(node["__typename"]), "Video") ||
			isSlice(node["video_versions"])
		if isVideo {
			u := unescapeURL(orStr(str(node["video_url"]), firstURL(sliceOf(node["video_versions"]))))
			if u != "" {
				items = append(items, mediaItem{
					kind:  "video",
					url:   u,
					thumb: unescapeURL(orStr(str(node["display_url"]), firstCandidate(asMap(node["image_versions2"])))),
				})
			}
			return
		}
		u := unescapeURL(orStr(str(node["display_url"]), firstCandidate(asMap(node["image_versions2"])), str(node["display_uri"])))
		if u != "" {
			items = append(items, mediaItem{kind: "photo", url: u, thumb: u})
		}
	}

	if isCarousel && isSlice(post["carousel_media"]) {
		for _, n := range sliceOf(post["carousel_media"]) {
			push(asMap(n))
		}
		return items
	}
	if len(sidecarEdges) > 0 {
		for _, e := range sidecarEdges {
			push(nestedMap(asMap(e), "node"))
		}
		return items
	}
	push(post)
	return items
}

func itemsFromSnapinsta(decoded string) []mediaItem {
	matches := reRapidCDN.FindAllStringSubmatch(decoded, -1)
	items := make([]mediaItem, 0, len(matches))
	last := -1
	for _, m := range matches {
		payload := jwtPayload(m[2])
		real := str(payload["url"])
		if real == "" {
			continue
		}
		if m[1] == "thumb" {
			items = append(items, mediaItem{kind: "photo", url: "", thumb: real})
			last = len(items) - 1
			continue
		}
		if last >= 0 && items[last].url == "" {
			items[last].kind = kindFromFilename(str(payload["filename"]))
			items[last].url = real
			continue
		}
		items = append(items, mediaItem{kind: kindFromFilename(str(payload["filename"])), url: real, thumb: ""})
		last = len(items) - 1
	}
	for i := range items {
		if items[i].url == "" {
			items[i].url = items[i].thumb
			items[i].kind = "photo"
		}
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func kindFromFilename(name string) string {
	if reMp4.MatchString(name) {
		return "video"
	}
	return "photo"
}

func decodeSnapinsta(raw string) string {
	substituted := reEval.ReplaceAllString(raw, "__out=(function(h,u,n,t,e,r)")
	if substituted == raw {
		return ""
	}

	vm := goja.New()
	timer := time.AfterFunc(3*time.Second, func() { vm.Interrupt("snapinsta decode timeout") })
	if _, err := vm.RunString(substituted); err != nil {
		timer.Stop()
		return ""
	}
	timer.Stop()

	out := vm.Get("__out")
	if out == nil {
		return ""
	}
	v := out.Export()
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
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

func unescapeURL(u string) string {
	return strings.NewReplacer(
		`\u0026`, "&",
		`\u00253D`, "=",
		`\/`, "/",
	).Replace(u)
}

func shortcodeOf(u string) string {
	if m := reShortcode.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

func isStory(u string) bool {
	return reStory.MatchString(u)
}

func viaRelay(relay, target string) string {
	return strings.TrimRight(relay, "/") + "/" + target
}

func firstMatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func extractJSONObject(s string, from int) map[string]interface{} {
	start := strings.Index(s[from:], "{")
	if start == -1 {
		return nil
	}
	start += from
	depth := 0
	limit := start + 500000
	if limit > len(s) {
		limit = len(s)
	}
	for i := start; i < limit; i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				var m map[string]interface{}
				if err := json.Unmarshal([]byte(s[start:i+1]), &m); err != nil {
					return nil
				}
				return m
			}
		}
	}
	return nil
}

func firstURL(arr []interface{}) string {
	for _, e := range arr {
		if u := str(asMap(e)["url"]); u != "" {
			return u
		}
	}
	return ""
}

func firstCandidate(node map[string]interface{}) string {
	return firstURL(sliceOf(node["candidates"]))
}

func asMap(v interface{}) map[string]interface{} {
	m, _ := v.(map[string]interface{})
	return m
}

func sliceOf(v interface{}) []interface{} {
	s, _ := v.([]interface{})
	return s
}

func isSlice(v interface{}) bool {
	_, ok := v.([]interface{})
	return ok
}

func str(v interface{}) string {
	s, _ := v.(string)
	return s
}

func num(v interface{}) float64 {
	f, _ := v.(float64)
	return f
}

func boolVal(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func orStr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
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

type cookieJar struct {
	keys  []string
	store map[string]string
}

func newCookieJar() *cookieJar {
	return &cookieJar{store: map[string]string{}}
}

func (j *cookieJar) apply(resp *http.Response) {
	for _, c := range resp.Cookies() {
		if c.Name == "" {
			continue
		}
		if _, ok := j.store[c.Name]; !ok {
			j.keys = append(j.keys, c.Name)
		}
		j.store[c.Name] = c.Value
	}
}

func (j *cookieJar) header() string {
	parts := make([]string, 0, len(j.keys))
	for _, k := range j.keys {
		parts = append(parts, k+"="+j.store[k])
	}
	return strings.Join(parts, "; ")
}

func (j *cookieJar) get(name string) string {
	return j.store[name]
}

func (p *Provider) do(ctx context.Context, method, target string, headers map[string]string, body []byte, jar *cookieJar, timeout time.Duration) (string, int, error) {
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

	if jar != nil {
		jar.apply(resp)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", 0, classifyClientError(err)
	}
	return string(data), resp.StatusCode, nil
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
	reInstagramURL = regexp.MustCompile(`(?i)instagram\.com|instagr\.am`)
	reShortcode    = regexp.MustCompile(`(?i)/(?:p|reel|reels|tv)/([A-Za-z0-9_-]{5,})`)
	reStory        = regexp.MustCompile(`(?i)/(?:stories|s)/`)
	reEval         = regexp.MustCompile(`\beval\(function\(h,u,n,t,e,r\)`)
	reRapidCDN     = regexp.MustCompile(`https://d\.rapidcdn\.app/(v2|thumb)\?token=([A-Za-z0-9_.\-]+)`)
	reToken        = regexp.MustCompile(`name="token" value="([^"]*)"`)
	reCSRF         = regexp.MustCompile(`"csrf_token":"([^"]+)"`)
	reLSD          = regexp.MustCompile(`"LSD",\[\],\{"token":"([^"]+)"`)
	reAppID        = regexp.MustCompile(`"X-IG-App-ID":"(\d+)"`)
	reAppID2       = regexp.MustCompile(`"APP_ID":"(\d+)"`)
	reMp4          = regexp.MustCompile(`(?i)\.mp4(\?|$)`)
	reErrorAPI     = regexp.MustCompile(`(?i)Error api`)
	reRapidCDNApp  = regexp.MustCompile(`rapidcdn\.app`)
)
