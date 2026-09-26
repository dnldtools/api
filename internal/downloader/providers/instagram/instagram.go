package instagram

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
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/snapx"
)

const (
	defaultRelay = "https://cors.siputzx.my.id/"

	defaultSnapinsta = "https://snapinsta.ai/"

	defaultSnapsaveHome   = "https://snapsave.app/download-video-instagram"
	defaultSnapsaveAction = "https://snapsave.app/action.php"

	instasaveAPI    = "https://api.instasave.website/media"
	instasaveOrigin = "https://instasave.website"
	instasaveRef    = "https://instasave.website/"

	defaultIGUA = "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36"

	defaultBrowserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	defaultNativeBase = "https://www.instagram.com"

	defaultNativeUA = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"

	defaultAppID = "936619743392459"

	docID = "8845758582119845"

	maxResponseBytes = 5 << 20
)

type Config struct {
	RelayBaseURL string

	SnapinstaBaseURL string

	// SnapsaveHomeURL is the landing page used to warm the cookie jar before
	// posting to SnapsaveActionURL (snapsave.app).
	SnapsaveHomeURL string

	// SnapsaveActionURL is the form endpoint that returns the packed media
	// response (snapsave.app/action.php).
	SnapsaveActionURL string

	Timeout time.Duration

	IGUserAgent string

	BrowserUserAgent string

	// InstagramCookie is an optional logged-in Instagram session cookie
	// (e.g. `sessionid=...; ds_user_id=...; csrftoken=...`). Public posts and
	// reels resolve without it via the embedded page data and the embed
	// endpoint; the cookie is only used for the legacy GraphQL fallback
	// (e.g. private/age-gated media).
	InstagramCookie string

	HTTPClient *http.Client

	// SnapXEnabled turns on api.snapx.info as a last-resort fallback.
	// DefaultConfig enables it; tests using NewWithConfig stay off unless set.
	SnapXEnabled bool
	SnapX        *snapx.Client

	// NativeEnabled turns on the direct instagram.com fetch with a Googlebot
	// User-Agent (the primary scraper ported from the Instagram all-in-one
	// script). DefaultConfig enables it; tests keep it off unless set so they
	// stay hermetic.
	NativeEnabled bool

	// NativeBaseURL overrides the instagram.com origin used by the native
	// fetch (intended for tests).
	NativeBaseURL string

	// NativeUA is the User-Agent used by the native fetch (defaults to a
	// Googlebot UA, which makes Instagram serve SSR JSON to anonymous clients).
	NativeUA string
}

func DefaultConfig() Config {
	return Config{
		RelayBaseURL:      defaultRelay,
		SnapinstaBaseURL:  defaultSnapinsta,
		SnapsaveHomeURL:   defaultSnapsaveHome,
		SnapsaveActionURL: defaultSnapsaveAction,
		Timeout:           30 * time.Second,
		IGUserAgent:       defaultIGUA,
		BrowserUserAgent:  defaultBrowserUA,
		SnapXEnabled:      true,
		NativeEnabled:     true,
		NativeBaseURL:     defaultNativeBase,
		NativeUA:          defaultNativeUA,
	}
}

type Provider struct {
	relay          string
	snapinsta      string
	snapsaveHome   string
	snapsaveAction string
	igUA           string
	browserUA      string
	igCookie       string
	client         *http.Client
	snapx          *snapx.Client

	nativeEnabled bool
	nativeBase    string
	nativeUA      string
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
	if cfg.SnapsaveHomeURL == "" {
		cfg.SnapsaveHomeURL = defaultSnapsaveHome
	}
	if cfg.SnapsaveActionURL == "" {
		cfg.SnapsaveActionURL = defaultSnapsaveAction
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
	if cfg.NativeBaseURL == "" {
		cfg.NativeBaseURL = defaultNativeBase
	}
	if cfg.NativeUA == "" {
		cfg.NativeUA = defaultNativeUA
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	p := &Provider{
		relay:          strings.TrimRight(cfg.RelayBaseURL, "/"),
		snapinsta:      strings.TrimRight(cfg.SnapinstaBaseURL, "/"),
		snapsaveHome:   strings.TrimRight(cfg.SnapsaveHomeURL, "/"),
		snapsaveAction: strings.TrimRight(cfg.SnapsaveActionURL, "/"),
		igUA:           cfg.IGUserAgent,
		browserUA:      cfg.BrowserUserAgent,
		igCookie:       strings.TrimSpace(cfg.InstagramCookie),
		client:         client,

		nativeEnabled: cfg.NativeEnabled,
		nativeBase:    strings.TrimRight(cfg.NativeBaseURL, "/"),
		nativeUA:      cfg.NativeUA,
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

	// Try providers in order until one returns media. This keeps the resolver
	// resilient: if one service is down or rejects a URL, the next is tried.
	var strategies []func(context.Context) (*downloader.DownloadResult, error)
	if story {
		// Official GraphQL is login-gated for stories, so rely on scrapers.
		strategies = []func(context.Context) (*downloader.DownloadResult, error){
			func(ctx context.Context) (*downloader.DownloadResult, error) { return p.querySnapsave(ctx, inputURL) },
			func(ctx context.Context) (*downloader.DownloadResult, error) { return p.querySnapinsta(ctx, inputURL) },
			func(ctx context.Context) (*downloader.DownloadResult, error) { return p.queryInstasave(ctx, inputURL) },
		}
	} else {
		strategies = []func(context.Context) (*downloader.DownloadResult, error){}
		if p.nativeEnabled {
			strategies = append(strategies, func(ctx context.Context) (*downloader.DownloadResult, error) {
				return p.queryNative(ctx, shortcode)
			})
		}
		strategies = append(strategies,
			func(ctx context.Context) (*downloader.DownloadResult, error) {
				return p.queryOfficial(ctx, inputURL, shortcode)
			},
			func(ctx context.Context) (*downloader.DownloadResult, error) { return p.queryInstasave(ctx, inputURL) },
			func(ctx context.Context) (*downloader.DownloadResult, error) { return p.querySnapinsta(ctx, inputURL) },
		)
	}
	if p.snapx != nil {
		strategies = append(strategies, func(ctx context.Context) (*downloader.DownloadResult, error) {
			return p.querySnapX(ctx, inputURL)
		})
	}

	var lastErr error
	for _, run := range strategies {
		res, err := run(ctx)
		if err == nil && res != nil && len(res.Formats) > 0 {
			res.Platform = downloader.PlatformInstagram
			res.URL = inputURL
			return res, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = downloader.ErrMediaNotFound
	}
	return nil, lastErr
}

func (p *Provider) querySnapX(ctx context.Context, inputURL string) (*downloader.DownloadResult, error) {
	if p.snapx == nil {
		return nil, downloader.ErrMediaNotFound
	}
	return p.snapx.Instagram(ctx, inputURL)
}

func (p *Provider) queryNative(ctx context.Context, shortcode string) (*downloader.DownloadResult, error) {
	if shortcode == "" {
		return nil, downloader.ErrMediaNotFound
	}

	pageURL := p.nativeBase + "/p/" + shortcode + "/"
	body, status, err := p.do(ctx, http.MethodGet, pageURL, map[string]string{
		"user-agent":      p.nativeUA,
		"accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"accept-language": "en-US,en;q=0.9,id;q=0.8",
	}, nil, nil, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, downloader.ErrMediaNotFound
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices || len(body) < 200 {
		return nil, classifyHTTPStatus(status)
	}

	media := findShortcodeMedia(body, shortcode)
	if media == nil {
		return p.buildNativeFromOG(body, shortcode)
	}

	items := itemsFromOfficial(media)
	if len(items) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	return p.buildResult(media, items), nil
}

// findShortcodeMedia extracts the `shortcode_media` object from a rendered
// Instagram post page. It mirrors the all-in-one script: try the polaris media
// JSON, then the embed-style extra payload, then a generic deep search over
// every JSON object in script tags for the first media node.
func findShortcodeMedia(htmlText, shortcode string) map[string]interface{} {
	marker := `"code":"` + shortcode + `"`
	if idx := strings.Index(htmlText, marker); idx != -1 {
		if key := strings.LastIndex(htmlText[:idx], `"xig_polaris_media":`); key != -1 {
			data := extractJSONObject(htmlText, key)
			if inner, ok := data["if_not_gated_logged_out"].(map[string]interface{}); ok {
				return inner
			}
			return data
		}
	}

	if idx := strings.Index(htmlText, `__additionalDataLoaded('extra'`); idx != -1 {
		data := extractJSONObject(htmlText, idx)
		if media := asMap(data["shortcode_media"]); media != nil {
			return media
		}
		if items := sliceOf(data["items"]); len(items) > 0 {
			if m := asMap(items[0]); m != nil {
				return m
			}
		}
	}

	var candidates []map[string]interface{}
	for _, block := range extractJSONBlocks(htmlText) {
		deepFindIG(block, 0, &candidates)
	}
	return selectIGMedia(candidates, shortcode)
}

func selectIGMedia(candidates []map[string]interface{}, shortcode string) map[string]interface{} {
	if len(candidates) == 0 {
		return nil
	}
	if shortcode != "" {
		for _, c := range candidates {
			if str(c["shortcode"]) == shortcode {
				return c
			}
		}
	}
	for _, c := range candidates {
		if len(itemsFromOfficial(c)) > 0 {
			return c
		}
	}
	return candidates[0]
}

func (p *Provider) buildNativeFromOG(htmlText, shortcode string) (*downloader.DownloadResult, error) {
	title := collapseSpace(html.UnescapeString(metaContent(htmlText, "og:title")))
	ogImage := html.UnescapeString(metaContent(htmlText, "og:image"))
	ogVideo := html.UnescapeString(metaContent(htmlText, "og:video"))
	if ogVideo == "" {
		ogVideo = html.UnescapeString(metaContent(htmlText, "og:video:secure_url"))
	}
	if ogVideo == "" {
		ogVideo = html.UnescapeString(metaContent(htmlText, "og:video:url"))
	}

	var items []mediaItem
	if ogVideo != "" {
		items = append(items, mediaItem{kind: "video", url: ogVideo, thumb: ogImage})
	} else if ogImage != "" {
		items = append(items, mediaItem{kind: "photo", url: ogImage, thumb: ogImage})
	}
	if len(items) == 0 {
		return nil, downloader.ErrMediaNotFound
	}

	post := map[string]interface{}{"shortcode": shortcode, "caption": title}
	return p.buildResult(post, items), nil
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

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func extractJSONBlocks(htmlText string) []map[string]interface{} {
	var blocks []map[string]interface{}
	reScript := regexp.MustCompile(`(?is)<script[^>]*>(.*?)</script>`)
	reAssign := regexp.MustCompile(`(?:=|const|let|var)\s*(\{[\s\S]*?\});`)
	for _, m := range reScript.FindAllStringSubmatch(htmlText, -1) {
		text := strings.TrimSpace(m[1])
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "{") {
			if obj, ok := tryJSON(text); ok {
				blocks = append(blocks, obj)
			}
			continue
		}
		for _, mm := range reAssign.FindAllStringSubmatch(text, -1) {
			if obj, ok := tryJSON(mm[1]); ok {
				blocks = append(blocks, obj)
			}
		}
	}
	return blocks
}

func tryJSON(s string) (map[string]interface{}, bool) {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, false
	}
	return m, true
}

func deepFindIG(node interface{}, depth int, out *[]map[string]interface{}) {
	if depth > 64 || len(*out) >= 200 {
		return
	}
	switch n := node.(type) {
	case map[string]interface{}:
		if isIGMediaCandidate(n) {
			*out = append(*out, n)
		}
		for _, v := range n {
			deepFindIG(v, depth+1, out)
		}
	case []interface{}:
		for _, v := range n {
			deepFindIG(v, depth+1, out)
		}
	}
}

func isIGMediaCandidate(m map[string]interface{}) bool {
	if m == nil {
		return false
	}
	for _, k := range []string{"shortcode", "video_versions", "carousel_media", "edge_sidecar_to_children", "image_versions2"} {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
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
		"cookie":          p.igCookie,
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

	// The GraphQL endpoint below is login-gated for most posts: logged-out
	// clients get a login-required payload. The embed page serves the same
	// `shortcode_media` object to anonymous visitors, so try it as a
	// cookie-free source before GraphQL.
	if res, err := p.queryEmbed(ctx, shortcode); err == nil {
		return res, nil
	}

	csrf := firstMatch(reCSRF, pageBody)
	if csrf == "" {
		csrf = cookieValue(p.igCookie, "csrftoken")
	}
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
		"cookie":      orStr(p.igCookie, jar.header()),
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

// queryEmbed resolves a public post/reel without a session cookie by reading
// the embed page, which serves the same `shortcode_media` object to logged-out
// clients inside window.__additionalDataLoaded('extra', {...}).
func (p *Provider) queryEmbed(ctx context.Context, shortcode string) (*downloader.DownloadResult, error) {
	embedURL := "https://www.instagram.com/p/" + shortcode + "/embed/captioned/"

	body, status, err := p.do(ctx, http.MethodGet, viaRelay(p.relay, embedURL), map[string]string{
		"user-agent": p.browserUA,
		"accept":     "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	}, nil, nil, 8*time.Second)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices || len(body) < 200 {
		return nil, classifyHTTPStatus(status)
	}

	marker := `__additionalDataLoaded('extra'`
	idx := strings.Index(body, marker)
	if idx == -1 {
		return nil, downloader.ErrMediaNotFound
	}
	data := extractJSONObject(body, idx)
	if data == nil {
		return nil, downloader.ErrMediaNotFound
	}

	media := asMap(data["shortcode_media"])
	if media == nil {
		if inner, ok := data["items"].([]interface{}); ok && len(inner) > 0 {
			media = asMap(inner[0])
		}
	}
	if media == nil {
		return nil, downloader.ErrMediaNotFound
	}

	items := itemsFromOfficial(media)
	if len(items) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	return p.buildResult(media, items), nil
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

// querySnapsave resolves a URL via snapsave.app. It mirrors the reference
// Node implementation: GET the landing page to warm cookies, then POST the
// URL to action.php and decode the packed response. It reuses decodeSnapinsta
// because snapsave uses the same eval(function(h,u,n,t,e,r){...}) obfuscation
// and the same rapidcdn.app token payloads.
func (p *Provider) querySnapsave(ctx context.Context, inputURL string) (*downloader.DownloadResult, error) {
	jar := newCookieJar()

	// The home request only warms the cookie jar (Set-Cookie); its body and
	// status are intentionally ignored, matching the reference implementation.
	_, _, err := p.do(ctx, http.MethodGet, p.snapsaveHome, map[string]string{
		"user-agent": p.browserUA,
		"accept":     "text/html",
	}, nil, jar, 8*time.Second)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("url", inputURL)
	form.Set("action", "post")
	form.Set("lang", "en")

	postBody, status, err := p.do(ctx, http.MethodPost, p.snapsaveAction, map[string]string{
		"user-agent":       p.browserUA,
		"content-type":     "application/x-www-form-urlencoded",
		"origin":           "https://snapsave.app",
		"referer":          p.snapsaveHome,
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

// queryInstasave resolves a URL via the instasave.website API, proxied
// through the relay. The response is HTML whose markup is hex-escaped and
// contains cdn.instasave.website tokens; each token is a JWT whose payload
// holds the real media URL.
func (p *Provider) queryInstasave(ctx context.Context, inputURL string) (*downloader.DownloadResult, error) {
	form := url.Values{}
	form.Set("url", inputURL)
	form.Set("lang", "en")

	body, status, err := p.do(ctx, http.MethodPost, viaRelay(p.relay, instasaveAPI), map[string]string{
		"origin":       instasaveOrigin,
		"referer":      instasaveRef,
		"content-type": "application/x-www-form-urlencoded",
		"user-agent":   p.browserUA,
		"accept":       "application/json, text/plain, */*",
	}, []byte(form.Encode()), nil, 8*time.Second)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices || len(body) < 500 {
		return nil, downloader.ErrProviderInvalidResponse
	}

	html := decodeHexEscapes(body)

	downloads := make([]string, 0, 4)
	thumbs := make([]string, 0, 4)
	seen := make(map[string]bool)
	for _, m := range reInstasaveA.FindAllStringSubmatch(html, -1) {
		real := resolveInstasaveToken(m[1])
		if real != "" && !seen[real] {
			seen[real] = true
			downloads = append(downloads, real)
		}
	}
	for _, m := range reInstasaveImg.FindAllStringSubmatch(html, -1) {
		if real := resolveInstasaveToken(m[1]); real != "" {
			thumbs = append(thumbs, real)
		}
	}

	items := make([]mediaItem, 0, len(downloads))
	for i, d := range downloads {
		kind := "photo"
		if reMp4.MatchString(d) {
			kind = "video"
		}
		thumb := ""
		if i < len(thumbs) {
			thumb = thumbs[i]
		}
		items = append(items, mediaItem{kind: kind, url: d, thumb: thumb})
	}
	if len(items) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	return p.buildResult(nil, items), nil
}

func resolveInstasaveToken(token string) string {
	return str(jwtPayload(token)["url"])
}

func decodeHexEscapes(input string) string {
	out := make([]byte, 0, len(input))
	for i := 0; i < len(input); i++ {
		if input[i] == '\\' && i+3 < len(input) && input[i+1] == 'x' {
			hi := hexDigit(input[i+2])
			lo := hexDigit(input[i+3])
			if hi >= 0 && lo >= 0 {
				out = append(out, byte(hi<<4|lo))
				i += 3
				continue
			}
		}
		out = append(out, input[i])
	}
	return string(out)
}

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
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

	if post != nil {
		if d := num(post["video_duration"]); d > 0 {
			result.DurationMs = int64(d * 1000)
		}
		if md := buildMetadata(post); len(md) > 0 {
			result.Metadata = md
		}
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

func buildMetadata(post map[string]interface{}) map[string]string {
	if post == nil {
		return nil
	}

	owner := asMap(post["owner"])
	if owner == nil {
		owner = asMap(post["user"])
	}

	md := make(map[string]string)
	set := func(k, v string) {
		v = strings.TrimSpace(v)
		if v != "" {
			md[k] = v
		}
	}

	set("author", orStr(nestedStr(post, "owner", "username"), nestedStr(post, "user", "username")))
	set("author_name", orStr(nestedStr(post, "owner", "full_name"), nestedStr(post, "user", "full_name")))
	set("author_id", str(owner["id"]))
	set("author_profile_pic", str(owner["profile_pic_url"]))
	if boolVal(owner["is_verified"]) {
		set("author_verified", "true")
	}
	if boolVal(owner["is_private"]) {
		set("author_private", "true")
	}

	set("caption", captionText(post))

	set("likes", orStr(edgeCount(post, "edge_media_preview_like", "edge_liked_by"), formatNum(post["like_count"])))
	set("comments", orStr(edgeCount(post, "edge_media_to_comment", "edge_media_to_parent_comment"), formatNum(post["comment_count"])))
	set("views", formatNum(post["video_view_count"]))
	set("plays", formatNum(post["video_play_count"]))

	if dims := asMap(post["dimensions"]); dims != nil {
		set("width", formatNum(dims["width"]))
		set("height", formatNum(dims["height"]))
	}

	if ts := num(post["taken_at_timestamp"]); ts > 0 {
		set("taken_at", time.Unix(int64(ts), 0).UTC().Format(time.RFC3339))
	}

	if kind := mediaKind(post); kind != "" {
		set("media_type", kind)
	}
	if pt := strings.TrimSpace(str(post["product_type"])); pt != "" {
		set("product_type", pt)
	}
	set("location", nestedStr(post, "location", "name"))
	set("shortcode", str(post["shortcode"]))

	if len(md) == 0 {
		return nil
	}
	return md
}

// edgeCount reads an engagement count from an edge node such as
// `edge_media_preview_like` / `edge_media_to_comment`. It falls back to the
// `count` field of the first key present in the media object.
func edgeCount(post map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if m := nestedMap(post, k); m != nil {
			if c := formatNum(m["count"]); c != "" {
				return c
			}
		}
	}
	return ""
}

func mediaKind(post map[string]interface{}) string {
	if n := num(post["media_type"]); n > 0 {
		switch n {
		case 1:
			return "image"
		case 2:
			return "video"
		case 8:
			return "carousel"
		}
	}
	tn := str(post["__typename"])
	switch {
	case strings.Contains(tn, "Video"):
		return "video"
	case strings.Contains(tn, "Sidecar"), strings.Contains(tn, "Carousel"):
		return "carousel"
	case strings.Contains(tn, "Image"):
		return "image"
	}
	if boolVal(post["is_video"]) {
		return "video"
	}
	return ""
}

// formatNum renders a JSON number or numeric string without trailing zeroes
// (e.g. 123.0 -> "123") and returns "" for zero/nil so empty values are
// omitted from metadata instead of reported as "0".
func formatNum(v interface{}) string {
	if s := strings.TrimSpace(str(v)); s != "" {
		return s
	}
	f := num(v)
	if f == 0 {
		return ""
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
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

// cookieValue extracts a single cookie value from a raw Cookie header string
// (e.g. "csrftoken" from "sessionid=...; csrftoken=abc; ds_user_id=...").
func cookieValue(cookieHeader, name string) string {
	for _, part := range strings.Split(cookieHeader, ";") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, name+"="); ok {
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
	reInstasaveA   = regexp.MustCompile(`<a\s+[^>]*href="https://cdn\.instasave\.website/\?token=([A-Za-z0-9_.\-]+)"`)
	reInstasaveImg = regexp.MustCompile(`<img\s+src="https://cdn\.instasave\.website/\?token=([A-Za-z0-9_.\-]+)"`)
)
