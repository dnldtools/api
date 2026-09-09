package ninexbuddy

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"io"
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
	defaultSiteURL = "https://9xbuddy.site"

	defaultAPIURL = "https://ab.9xbud.com"

	defaultSigSalt = "jv7g2_DAMNN_DUDE"

	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"

	defaultSearchEngine = "yt"

	defaultTimeout = 30 * time.Second

	// defaultTicketTimeout bounds the per-format ticket polling loop. It must
	// stay well below the HTTP server WriteTimeout (60s) so a slow ticket can
	// never outlive the response socket. 90s was longer than the server could
	// actually wait, so requests died mid-poll anyway.
	defaultTicketTimeout = 20 * time.Second

	// maxTicketPollAttempts caps the number of polling iterations per ticket as
	// a hard backstop even when the timeout hasn't been hit yet.
	maxTicketPollAttempts = 12

	maxResponseBytes = 5 << 20
)

type Config struct {
	SiteURL string

	APIURL string

	SigSalt string

	UserAgent string

	SearchEngine string

	Timeout time.Duration

	TicketTimeout time.Duration

	HTTPClient *http.Client
}

func DefaultConfig() Config {
	return Config{
		SiteURL:       defaultSiteURL,
		APIURL:        defaultAPIURL,
		SigSalt:       defaultSigSalt,
		UserAgent:     defaultUserAgent,
		SearchEngine:  defaultSearchEngine,
		Timeout:       defaultTimeout,
		TicketTimeout: defaultTicketTimeout,
	}
}

type Provider struct {
	siteURL   string
	apiURL    string
	sigSalt   string
	userAgent string

	searchEngine string

	ticketTimeout time.Duration

	client *http.Client
}

var _ downloader.Provider = (*Provider)(nil)

var _ downloader.URLMatcher = (*Provider)(nil)

func New() *Provider {
	return NewWithConfig(DefaultConfig())
}

func NewWithConfig(cfg Config) *Provider {
	if cfg.SiteURL == "" {
		cfg.SiteURL = defaultSiteURL
	}
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}
	if cfg.SigSalt == "" {
		cfg.SigSalt = defaultSigSalt
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}
	if cfg.SearchEngine == "" {
		cfg.SearchEngine = defaultSearchEngine
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.TicketTimeout <= 0 {
		cfg.TicketTimeout = defaultTicketTimeout
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &Provider{
		siteURL:       strings.TrimRight(cfg.SiteURL, "/"),
		apiURL:        strings.TrimRight(cfg.APIURL, "/"),
		sigSalt:       cfg.SigSalt,
		userAgent:     cfg.UserAgent,
		searchEngine:  cfg.SearchEngine,
		ticketTimeout: cfg.TicketTimeout,
		client:        client,
	}
}

func (p *Provider) Name() string { return "9xbuddy" }

func (p *Provider) Platform() downloader.Platform { return downloader.Platform9xbuddy }

func (p *Provider) Type() downloader.ProviderType { return downloader.ProviderExternalAPI }

// MatchesURL is intentionally broad: 9xbuddy is an all-in-one downloader, so it
// accepts any http(s) URL and acts as the fallback for platforms that no other
// provider claims. It is registered last so dedicated providers still win.
func (p *Provider) MatchesURL(u string) bool {
	parsed, err := url.Parse(strings.TrimSpace(u))
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return parsed.Hostname() != ""
	default:
		return false
	}
}

func (p *Provider) Resolve(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	inputURL := strings.TrimSpace(req.URL)
	if inputURL == "" {
		return nil, downloader.ErrInvalidURL
	}
	if !p.MatchesURL(inputURL) {
		return nil, downloader.ErrInvalidURL
	}

	data, err := p.extract(ctx, inputURL)
	if err != nil {
		return nil, err
	}

	result, err := p.buildResult(data)
	if err != nil {
		return nil, err
	}
	result.Platform = downloader.Platform9xbuddy
	result.URL = inputURL
	return result, nil
}

type session struct {
	init        map[string]interface{}
	cssHash     string
	hostname    string
	authToken   string
	accessToken string
}

func (p *Provider) loadSiteContext(ctx context.Context) (*session, error) {
	status, body, err := p.do(ctx, http.MethodGet, p.siteURL+"/", map[string]string{
		"user-agent": p.userAgent,
		"accept":     "text/html",
	}, nil)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, classifyHTTPStatus(status)
	}

	htmlStr := string(body)
	start := strings.Index(htmlStr, "window.__INIT__")
	if start < 0 {
		return nil, downloader.ErrProviderInvalidResponse
	}
	eqRel := strings.Index(htmlStr[start:], "=")
	if eqRel < 0 {
		return nil, downloader.ErrProviderInvalidResponse
	}
	eq := start + eqRel
	endRel := strings.Index(htmlStr[eq:], "</script>")
	if endRel < 0 {
		return nil, downloader.ErrProviderInvalidResponse
	}
	end := eq + endRel

	payload := strings.TrimSpace(htmlStr[eq+1 : end])
	payload = strings.TrimRight(payload, "; \t\r\n")
	if payload == "" {
		return nil, downloader.ErrProviderInvalidResponse
	}

	var init map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &init); err != nil {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}

	m := reCSSHash.FindStringSubmatch(htmlStr)
	if len(m) < 2 || m[1] == "" {
		return nil, downloader.ErrProviderInvalidResponse
	}

	hostname := ""
	if u, err := url.Parse(p.siteURL); err == nil {
		hostname = u.Hostname()
	}
	if hostname == "" {
		hostname = "9xbuddy.site"
	}

	return &session{init: init, cssHash: m[1], hostname: hostname}, nil
}

func (p *Provider) makeAuthToken(s *session) string {
	reversedHash := reverseString(s.cssHash)
	uaHead := reverseString(str(s.init["ua"]))
	if len(uaHead) > 10 {
		uaHead = uaHead[:10]
	}
	version := str(s.init["appVersion"])
	payload := s.hostname + reversedHash + uaHead + secretPhrase() + "xbuddy123sudo-" + version + version
	return encrypt(payload, reversedHash)
}

func (p *Provider) apiPost(ctx context.Context, path string, body map[string]interface{}, s *session) (map[string]interface{}, error) {
	headers := map[string]string{
		"accept":             "application/json, text/plain, */*",
		"content-type":       "application/json; charset=UTF-8",
		"origin":             p.siteURL,
		"referer":            p.siteURL + "/",
		"user-agent":         p.userAgent,
		"x-requested-domain": s.hostname,
		"x-requested-with":   "xmlhttprequest",
		"x-auth-token":       s.authToken,
	}
	if s.accessToken != "" {
		headers["x-access-token"] = s.accessToken
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}

	status, data, err := p.do(ctx, http.MethodPost, p.apiURL+"/"+path, headers, payload)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, classifyHTTPStatus(status)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		out = map[string]interface{}{}
	}
	return out, nil
}

func (p *Provider) extract(ctx context.Context, inputURL string) (map[string]interface{}, error) {
	s, err := p.loadSiteContext(ctx)
	if err != nil {
		return nil, err
	}

	s.authToken = p.makeAuthToken(s)

	tokenRes, err := p.apiPost(ctx, "token", map[string]interface{}{}, s)
	if err != nil {
		return nil, err
	}
	s.accessToken = str(tokenRes["access_token"])
	if s.accessToken == "" {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, stderrors.New("token failed"))
	}

	encoded := encodeURIComponent(inputURL)
	sig := encrypt(encoded, s.authToken+p.sigSalt)

	data, err := p.apiPost(ctx, "extract", map[string]interface{}{
		"url":          encoded,
		"_sig":         sig,
		"searchEngine": p.searchEngine,
	}, s)
	if err != nil {
		return nil, err
	}

	p.decodeTree(data, s)
	if err := p.materializeFormats(ctx, data, s); err != nil {
		return nil, err
	}
	return data, nil
}

func (p *Provider) decodeTree(v interface{}, s *session) {
	switch node := v.(type) {
	case []interface{}:
		for i := range node {
			p.decodeTree(node[i], s)
		}
	case map[string]interface{}:
		for k, val := range node {
			if k == "url" {
				if u, ok := val.(string); ok {
					node["url_encoded"] = u
					node["url"] = p.decodeBuddyURL(u, s)
					continue
				}
			}
			p.decodeTree(val, s)
		}
	}
}

func (p *Provider) decodeBuddyURL(encoded string, s *session) string {
	if encoded == "" {
		return encoded
	}
	if strings.HasPrefix(encoded, "//") {
		return "https:" + encoded
	}
	if reHTTP.MatchString(encoded) {
		return encoded
	}
	if !looksHex(encoded) {
		return encoded
	}

	bin := hex2bin(encoded)
	if bin == nil {
		return encoded
	}

	reversed := reverseBytes(bin)
	key := sorryMate() + strconv.Itoa(len(s.hostname)) + s.cssHash + s.accessToken
	plain := decrypt(string(reversed), key)
	if !isPrintableASCII(plain) {
		return encoded
	}
	if strings.HasPrefix(plain, "//") {
		return "https:" + plain
	}
	return plain
}

type downloadTicket struct {
	uid string
	url string
}

type resolvedMedia struct {
	URL    string
	Size   int64
	Source string
}

// materializeFormats resolves each format's ticket into a direct media URL.
// Format tickets are independent, so they are resolved concurrently with a
// bounded worker pool instead of one-after-another. Each goroutine only mutates
// its own format map, so there is no data race; the shared *session is read-only
// at this point.
func (p *Provider) materializeFormats(ctx context.Context, data map[string]interface{}, s *session) error {
	resp := asMap(data["response"])
	if resp == nil {
		resp = data
	}
	formats := sliceOf(resp["formats"])

	const maxConcurrency = 4
	sem := make(chan struct{}, maxConcurrency)

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
	)

	for _, item := range formats {
		m := asMap(item)
		if m == nil {
			continue
		}

		uid, ticketURL, ok := parseDownloadTicket(str(m["url"]))
		if !ok {
			continue
		}

		m["ticket"] = "/download/" + uid + "/" + ticketURL

		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}

		wg.Add(1)
		go func(m map[string]interface{}, uid, ticketURL string) {
			defer wg.Done()
			defer func() { <-sem }()

			resolved, err := p.resolveTicket(ctx, downloadTicket{uid: uid, url: ticketURL}, s)
			if err != nil {
				if ctx.Err() != nil {
					mu.Lock()
					if first == nil {
						first = ctx.Err()
					}
					mu.Unlock()
					return
				}
				m["downloadable"] = false
				m["download_error"] = err.Error()
				return
			}
			if resolved != nil && resolved.URL != "" {
				m["url"] = resolved.URL
				if resolved.Size > 0 {
					m["size"] = float64(resolved.Size)
				}
				m["downloadable"] = true
			}
		}(m, uid, ticketURL)
	}

	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	return first
}

func (p *Provider) resolveTicket(ctx context.Context, ticket downloadTicket, s *session) (*resolvedMedia, error) {
	ticketCtx, cancel := context.WithTimeout(ctx, p.ticketTimeout)
	defer cancel()

	started := time.Now()
	mode := "inspect"
	var preparedUID string

	for attempts := 0; ; attempts++ {
		if time.Since(started) >= p.ticketTimeout || attempts >= maxTicketPollAttempts {
			return nil, downloader.ErrProviderTimeout
		}

		body := map[string]interface{}{
			"uid":  ticket.uid,
			"url":  ticket.url,
			"mode": mode,
		}
		if preparedUID != "" {
			body["prepared_uid"] = preparedUID
		}

		jsonObj, err := p.apiPost(ticketCtx, "download", body, s)
		if err != nil {
			return nil, err
		}

		if ready := pickResultURL(jsonObj); ready != nil && ready.URL != "" {
			return ready, nil
		}

		if str(jsonObj["state"]) == "choice_required" {
			preparedUID = ""
			if arr := sliceOf(jsonObj["prepared"]); len(arr) > 0 {
				preparedUID = str(asMap(arr[0])["uid"])
			}
			if preparedUID == "" {
				preparedUID = str(asMap(jsonObj["requested"])["uid"])
			}
			if preparedUID != "" {
				mode = "select"
			} else {
				mode = "prepare"
			}
			if !sleepCtx(ticketCtx, 1200*time.Millisecond) {
				return nil, downloader.ErrProviderTimeout
			}
			continue
		}

		if _, hasStatus := jsonObj["status"]; hasStatus {
			if _, hasMessage := jsonObj["message"]; !hasMessage {
				prog, err := p.apiPost(ticketCtx, "progress", map[string]interface{}{"uid": ticket.uid}, s)
				if err != nil {
					return nil, err
				}
				if ready := pickResultURL(prog); ready != nil && ready.URL != "" {
					return ready, nil
				}
				if resp, ok := prog["response"].(map[string]interface{}); ok {
					if ready := pickResultURL(resp); ready != nil && ready.URL != "" {
						return ready, nil
					}
				}
				if !sleepCtx(ticketCtx, 2500*time.Millisecond) {
					return nil, downloader.ErrProviderTimeout
				}
				continue
			}
		}

		if msg := firstMessage(jsonObj); msg != "" {
			return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, stderrors.New(msg))
		}

		if !sleepCtx(ticketCtx, 1500*time.Millisecond) {
			return nil, downloader.ErrProviderTimeout
		}
	}
}

func pickResultURL(v interface{}) *resolvedMedia {
	jsonObj := asMap(v)
	if jsonObj == nil {
		return nil
	}

	if top := normalizeMediaURL(jsonObj["url"]); top != "" && !looksHex(top) && !strings.Contains(top, "/download/") {
		return &resolvedMedia{URL: top, Size: int64(num(jsonObj["size"]))}
	}

	if ve := asMap(jsonObj["video_edit"]); ve != nil {
		editURL := normalizeMediaURL(orFirst(nestedMapStr(ve, "formats", "url"), ve["url"]))
		if editURL != "" {
			return &resolvedMedia{URL: editURL, Size: int64(num(ve["size"])), Source: "video_edit"}
		}
	}

	if resp, ok := jsonObj["response"].(map[string]interface{}); ok {
		if r := pickResultURL(resp); r != nil {
			return r
		}
	}

	if hls := asMap(jsonObj["direct_hls"]); hls != nil {
		if u := normalizeMediaURL(hls["url"]); u != "" {
			return &resolvedMedia{URL: u, Size: int64(num(hls["size"])), Source: "direct_hls"}
		}
	}

	if arr := sliceOf(jsonObj["prepared"]); len(arr) > 0 {
		if first := asMap(arr[0]); first != nil {
			if u := normalizeMediaURL(first["url"]); u != "" {
				return &resolvedMedia{URL: u, Size: int64(num(first["size"])), Source: "prepared"}
			}
		}
	}

	return nil
}

func (p *Provider) buildResult(data map[string]interface{}) (*downloader.DownloadResult, error) {
	resp := asMap(data["response"])
	if resp == nil {
		resp = data
	}

	formats := make([]downloader.Format, 0)
	for _, item := range sliceOf(resp["formats"]) {
		m := asMap(item)
		if m == nil {
			continue
		}
		if downloadable, ok := m["downloadable"].(bool); ok && !downloadable {
			continue
		}
		u := normalizeMediaURL(m["url"])
		if u == "" || !reHTTP.MatchString(u) {
			continue
		}
		formats = append(formats, downloader.Format{
			Type:    mediaTypeFromString(str(m["type"])),
			URL:     u,
			Quality: str(m["quality"]),
			Ext:     orFirst(m["ext"], m["format"]),
			Size:    int64(num(m["size"])),
		})
	}

	if len(formats) == 0 {
		if msg := orFirst(resp["message"], resp["error"], data["message"], data["error"]); msg != "" {
			return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, stderrors.New(msg))
		}
		return nil, downloader.ErrMediaNotFound
	}

	title := orFirst(resp["title"], data["title"])
	if title == "" {
		if video := asMap(resp["video"]); video != nil {
			title = orFirst(video["title"], video["caption"], video["name"])
		}
	}

	thumb := orFirst(resp["thumbnail"], resp["thumb"], resp["cover"], resp["image"], data["thumbnail"])
	if thumb == "" {
		if video := asMap(resp["video"]); video != nil {
			thumb = orFirst(video["thumbnail"], video["thumb"], video["cover"], video["image"])
		}
	}

	result := &downloader.DownloadResult{
		Title:      title,
		Thumbnail:  thumb,
		DurationMs: int64(num(resp["duration_ms"])),
		Formats:    formats,
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

func (p *Provider) do(ctx context.Context, method, target string, headers map[string]string, body []byte) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return 0, nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, nil, classifyClientError(err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, classifyClientError(err)
	}
	return resp.StatusCode, data, nil
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

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func encodeURIComponent(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '!' || c == '~' || c == '*' || c == '\'' || c == '(' || c == ')' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&0x0f])
	}
	return b.String()
}

func sorryMate() string { return "SORRY_MATE" }

func secretPhrase() string {
	codes := []int{90, 84, 94, 100, 81, 81, 74, 89, 100, 70, 83, 83, 84, 76, 100, 89, 84, 83, 100, 82, 78, 100, 74, 89, 70, 82, 100, 94, 87, 87, 84, 88}
	b := make([]byte, len(codes))
	for i, c := range codes {
		b[i] = byte(c - 5)
	}
	return reverseString(string(b))
}

const b64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func encode64(raw string) string {
	out := make([]byte, 0, (len(raw)+2)/3*4)
	prev := 0
	mod := 0
	for i := 0; i < len(raw); i++ {
		code := int(raw[i])
		mod = i % 3
		switch mod {
		case 0:
			out = append(out, b64Alphabet[code>>2])
		case 1:
			out = append(out, b64Alphabet[((prev&3)<<4)|(code>>4)])
		default:
			out = append(out, b64Alphabet[((prev&15)<<2)|(code>>6)], b64Alphabet[code&63])
		}
		prev = code
	}
	if mod == 0 {
		out = append(out, b64Alphabet[(prev&3)<<4], '=', '=')
	} else if mod == 1 {
		out = append(out, b64Alphabet[(prev&15)<<2], '=')
	}
	return string(out)
}

func decode64(input string) []byte {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return r
	}, input)

	if clean == "" {
		return []byte{}
	}
	if !reB64.MatchString(clean) || len(clean)%4 != 0 {
		return nil
	}

	s := strings.TrimRight(clean, "=")
	if s == "" {
		return []byte{}
	}

	out := make([]byte, 0, len(s)*3/4+1)
	prev := 0
	for i := 0; i < len(s); i++ {
		idx := strings.IndexByte(b64Alphabet, s[i])
		if idx < 0 {
			return nil
		}
		switch i % 4 {
		case 1:
			out = append(out, byte((prev<<2)|(idx>>4)))
		case 2:
			out = append(out, byte(((prev&15)<<4)|(idx>>2)))
		case 3:
			out = append(out, byte(((prev&3)<<6)|idx))
		}
		prev = idx
	}
	return out
}

func encrypt(text, key string) string {
	if key == "" {
		return ""
	}
	out := make([]byte, 0, len(text))
	for i := 0; i < len(text); i++ {
		out = append(out, byte(int(text[i])+int(key[charIndex(i, key)])))
	}
	return encode64(string(out))
}

func decrypt(b64, key string) string {
	if key == "" {
		return ""
	}
	raw := decode64(b64)
	if raw == nil {
		return ""
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		out = append(out, byte(int(raw[i])-int(key[charIndex(i, key)])))
	}
	return string(out)
}

func charIndex(i int, key string) int {
	return ((i % len(key)) + len(key) - 1) % len(key)
}

func hex2bin(s string) []byte {
	if len(s)%2 != 0 {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

func looksHex(s string) bool {
	return len(s) >= 16 && reHex.MatchString(s)
}

func isPrintableASCII(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func parseDownloadTicket(value string) (uid, ticketURL string, ok bool) {
	if value == "" {
		return "", "", false
	}
	path := value
	if strings.HasPrefix(value, "http") {
		if u, err := url.Parse(value); err == nil {
			path = u.Path
		}
	}
	m := reDownloadTicket.FindStringSubmatch(path)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

func normalizeMediaURL(v interface{}) string {
	s, ok := v.(string)
	if !ok || s == "" {
		return ""
	}
	if strings.HasPrefix(s, "//") {
		return "https:" + s
	}
	return s
}

func firstMessage(m map[string]interface{}) string {
	if s := str(m["message"]); s != "" {
		return s
	}
	return str(m["error"])
}

func mediaTypeFromString(t string) downloader.MediaType {
	s := strings.ToLower(strings.TrimSpace(t))
	switch {
	case containsAny(s, "audio", "mp3", "m4a", "aac", "wav", "ogg"):
		return downloader.MediaAudio
	case containsAny(s, "image", "jpg", "jpeg", "png", "webp", "gif", "bmp"):
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

func reverseString(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

func reverseBytes(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[len(b)-1-i] = b[i]
	}
	return out
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

func orFirst(values ...interface{}) string {
	for _, v := range values {
		if s := str(v); s != "" {
			return s
		}
	}
	return ""
}

func nestedMapStr(v interface{}, keys ...string) string {
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

var (
	reHTTP           = regexp.MustCompile(`(?i)^https?://`)
	reHex            = regexp.MustCompile(`(?i)^[0-9a-f]+$`)
	reB64            = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)
	reCSSHash        = regexp.MustCompile(`/build/(?:assets/)?main\.([^"]+?)\.css`)
	reDownloadTicket = regexp.MustCompile(`^/download/([^/]+)/(.+)$`)
)
