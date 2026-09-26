package threads

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"rest-api/internal/downloader"
)

const (
	defaultAppID   = "238260118697367"
	defaultASBDID  = "129477"
	defaultDocPost = "5587632691339264"
	defaultUA      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
	crawlerUA      = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	alphabet       = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	maxBody        = 8 << 20
	graphqlHosts   = "https://www.threads.com/api/graphql|https://www.threads.net/api/graphql"
	blobWindow     = 200_000
)

var postRe = regexp.MustCompile(`(?i)/post/([A-Za-z0-9_-]+)`)

type Config struct {
	Timeout    time.Duration
	UserAgent  string
	HTTPClient *http.Client
}

type Provider struct {
	userAgent string
	client    *http.Client
}

type mediaItem struct {
	kind string
	url  string
}

type post struct {
	code     string
	caption  string
	username string
	fullName string
	media    []mediaItem
}

var _ downloader.Provider = (*Provider)(nil)

var _ downloader.URLMatcher = (*Provider)(nil)

func New() *Provider { return NewWithConfig(Config{}) }

func NewWithConfig(cfg Config) *Provider {
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUA
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 25 * time.Second
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
	}
	return &Provider{userAgent: cfg.UserAgent, client: hc}
}

func (p *Provider) Name() string { return "threads" }

func (p *Provider) Platform() downloader.Platform { return downloader.PlatformThreads }

func (p *Provider) Type() downloader.ProviderType { return downloader.ProviderNative }

func (p *Provider) MatchesURL(u string) bool {
	parsed, err := url.Parse(strings.TrimSpace(u))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "threads.com" || host == "threads.net" || strings.HasSuffix(host, ".threads.com") || strings.HasSuffix(host, ".threads.net")
}

func (p *Provider) Resolve(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	target := strings.TrimSpace(req.URL)
	target = strings.ReplaceAll(target, "://threads.net", "://www.threads.com")
	target = strings.ReplaceAll(target, "://www.threads.net", "://www.threads.com")

	m := postRe.FindStringSubmatch(target)
	if m == nil {
		return nil, downloader.ErrInvalidURL
	}
	shortcode := m[1]

	page := loadPage(ctx, p.client, target)
	if page == "" {
		return nil, downloader.ErrProviderUnavailable
	}

	lsd := extractLsd(page)
	appID := extractAppId(page)
	blobs := extractJsonBlobs(page)
	posts := collectPosts(blobs)

	if len(posts) == 0 && lsd != "" && shortcode != "" {
		if postID := shortcodeToID(shortcode); postID != "" {
			posts = graphqlPost(ctx, p.client, lsd, appID, postID, target)
		}
	}

	var chosen *post
	for i := range posts {
		if strings.EqualFold(posts[i].code, shortcode) {
			chosen = &posts[i]
			break
		}
	}
	if chosen == nil {
		for i := range posts {
			if len(posts[i].media) > 0 {
				chosen = &posts[i]
				break
			}
		}
	}
	if chosen == nil || len(chosen.media) == 0 {
		return nil, downloader.ErrMediaNotFound
	}

	formats := make([]downloader.Format, 0, len(chosen.media))
	var thumb string
	for i, med := range chosen.media {
		switch med.kind {
		case "video":
			formats = append(formats, downloader.Format{Type: downloader.MediaVideo, URL: med.url, Quality: "video", Ext: "mp4"})
		default:
			formats = append(formats, downloader.Format{Type: downloader.MediaImage, URL: med.url, Quality: fmt.Sprintf("photo-%d", i+1), Ext: "jpg"})
		}
	}

	author := ""
	if chosen.username != "" {
		author = "@" + chosen.username
	} else if chosen.fullName != "" {
		author = chosen.fullName
	}

	res := &downloader.DownloadResult{
		Platform:  downloader.PlatformThreads,
		URL:       target,
		Title:     chosen.caption,
		Thumbnail: thumb,
		Formats:   formats,
		Metadata: map[string]string{
			"source":    "threads",
			"shortcode": shortcode,
			"author":    author,
		},
	}
	res.Type = sameType(formats)
	if res.Thumbnail == "" {
		for _, f := range formats {
			if f.Type == downloader.MediaImage {
				res.Thumbnail = f.URL
				break
			}
		}
	}
	return res, nil
}

func loadPage(ctx context.Context, client *http.Client, target string) string {
	for _, ua := range []string{crawlerUA, defaultUA} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.Contains(string(body), "text_post_app_info") {
			return string(body)
		}
	}
	return ""
}

func shortcodeToID(code string) string {
	n := new(big.Int)
	base := big.NewInt(64)
	for _, ch := range code {
		i := strings.IndexRune(alphabet, ch)
		if i < 0 {
			return ""
		}
		n.Mul(n, base)
		n.Add(n, big.NewInt(int64(i)))
	}
	return n.String()
}

func extractLsd(html string) string {
	pats := []*regexp.Regexp{
		regexp.MustCompile(`LSD",\[\],\{"token":"([^"]+)"`),
		regexp.MustCompile(`"lsd"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`name="lsd"\s+value="([^"]+)"`),
	}
	for _, p := range pats {
		if m := p.FindStringSubmatch(html); m != nil {
			return m[1]
		}
	}
	return ""
}

func extractAppId(html string) string {
	if m := regexp.MustCompile(`"X-IG-App-ID":"(\d+)"`).FindStringSubmatch(html); m != nil {
		return m[1]
	}
	return defaultAppID
}

func sliceBalancedObject(src string, from, maxLen int) string {
	searchFrom := from
	for searchFrom >= 0 && from-searchFrom <= maxLen {
		start := strings.LastIndex(src[:searchFrom+1], "{")
		if start < 0 {
			return ""
		}
		depth := 0
		inStr := false
		esc := false
		end := -1
		for i := start; i < len(src) && i-start <= maxLen; i++ {
			c := src[i]
			if inStr {
				if esc {
					esc = false
				} else if c == '\\' {
					esc = true
				} else if c == '"' {
					inStr = false
				}
				continue
			}
			switch c {
			case '"':
				inStr = true
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return ""
		}
		if from >= start && from <= end {
			return src[start : end+1]
		}
		searchFrom = start - 1
	}
	return ""
}

func parseLooseJSON(s string) map[string]any {
	var out map[string]any
	if json.Unmarshal([]byte(s), &out) == nil {
		return out
	}
	s2 := strings.ReplaceAll(s, `\"`, `"`)
	s2 = strings.ReplaceAll(s2, `\\`, `\`)
	if json.Unmarshal([]byte(s2), &out) == nil {
		return out
	}
	return nil
}

func extractJsonBlobs(html string) []map[string]any {
	needles := []string{`"text_post_app_info"`, `"image_versions2"`, `"video_versions"`}
	var objects []map[string]any
	seen := map[string]bool{}
	for _, needle := range needles {
		from := 0
		hits := 0
		for hits < 80 {
			idx := strings.Index(html[from:], needle)
			if idx < 0 {
				break
			}
			idx += from
			from = idx + len(needle)
			hits++
			raw := sliceBalancedObject(html, idx, blobWindow)
			if raw == "" {
				continue
			}
			sig := fmt.Sprintf("%d:%s", len(raw), trunc(raw))
			if seen[sig] {
				continue
			}
			seen[sig] = true
			if obj := parseLooseJSON(raw); obj != nil {
				objects = append(objects, obj)
			}
		}
	}
	return objects
}

func trunc(s string) string {
	if len(s) <= 60 {
		return s
	}
	return s[20:60]
}

func pickBestURL(versions any) (string, int64, int64) {
	arr, ok := versions.([]any)
	if !ok || len(arr) == 0 {
		return "", 0, 0
	}
	type cand struct {
		url string
		w   int64
		h   int64
	}
	var scored []cand
	for _, v := range arr {
		mItem, ok := v.(map[string]any)
		if !ok {
			continue
		}
		u := firstString(mItem, "url", "uri", "src")
		if u == "" {
			continue
		}
		scored = append(scored, cand{u, numOf(mItem["width"], mItem["config_width"]), numOf(mItem["height"], mItem["config_height"])})
	}
	if len(scored) == 0 {
		return "", 0, 0
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].w*scored[i].h > scored[j].w*scored[j].h })
	return scored[0].url, scored[0].w, scored[0].h
}

func mediaFromNode(node map[string]any) []mediaItem {
	var out []mediaItem
	if node == nil {
		return out
	}
	var candidates []map[string]any
	if _, ok := node["video_versions"]; ok {
		candidates = append(candidates, node)
	}
	if _, ok := node["image_versions2"]; ok {
		candidates = append(candidates, node)
	}
	if cm, ok := node["carousel_media"].([]any); ok {
		for _, c := range cm {
			if mItem, ok := c.(map[string]any); ok {
				candidates = append(candidates, mItem)
			}
		}
	}
	if mItem, ok := node["media"].(map[string]any); ok {
		candidates = append(candidates, mItem)
	}

	for _, mItem := range candidates {
		if mItem == nil {
			continue
		}
		vURL, _, _ := pickBestURL(mItem["video_versions"])
		iURL, _, _ := imageURL(mItem)
		if vURL != "" {
			out = append(out, mediaItem{kind: "video", url: vURL})
		} else if iURL != "" {
			out = append(out, mediaItem{kind: "photo", url: iURL})
		}
	}
	return out
}

func imageURL(mItem map[string]any) (string, int64, int64) {
	if iv2, ok := mItem["image_versions2"].(map[string]any); ok {
		if cand, ok := iv2["candidates"].([]any); ok {
			if u, w, h := pickBestURL(cand); u != "" {
				return u, w, h
			}
		}
		if u, w, h := pickBestURL(iv2); u != "" {
			return u, w, h
		}
	}
	return pickBestURL(mItem["candidates"])
}

func collectPosts(roots []map[string]any) []post {
	var posts []post
	seen := map[string]bool{}
	for _, root := range roots {
		walk(root, func(node map[string]any) {
			if node == nil {
				return
			}
			_, hasInfo := node["text_post_app_info"]
			_, hasIV2 := node["image_versions2"]
			_, hasVV := node["video_versions"]
			_, hasUser := node["user"]
			_, hasPK := node["pk"]
			if !hasInfo && !((hasIV2 || hasVV) && (hasUser || hasPK)) {
				return
			}
			user, _ := node["user"].(map[string]any)
			if user == nil {
				user, _ = node["owner"].(map[string]any)
			}
			if user == nil {
				user = map[string]any{}
			}
			caption := captionOf(node)
			media := mediaFromNode(node)
			key := firstString(node, "pk", "code")
			if key == "" {
				key = mediaKey(media)
			}
			if seen[key] {
				return
			}
			seen[key] = true
			posts = append(posts, post{
				code:     firstString(node, "code", "shortcode"),
				caption:  caption,
				username: firstString(user, "username"),
				fullName: firstString(user, "full_name"),
				media:    media,
			})
		})
	}
	return posts
}

func captionOf(node map[string]any) string {
	if c, ok := node["caption"].(map[string]any); ok {
		if s := firstString(c, "text"); s != "" {
			return s
		}
	}
	if s := firstString(node, "caption"); s != "" {
		return s
	}
	if info, ok := node["text_post_app_info"].(map[string]any); ok {
		if tf, ok := info["text_fragments"].(map[string]any); ok {
			if s := firstString(tf, "text"); s != "" {
				return s
			}
		}
	}
	return ""
}

func mediaKey(media []mediaItem) string {
	parts := make([]string, len(media))
	for i, mItem := range media {
		parts[i] = mItem.url
	}
	return strings.Join(parts, "|")
}

func walk(v any, fn func(map[string]any)) {
	switch n := v.(type) {
	case map[string]any:
		fn(n)
		for _, child := range n {
			walk(child, fn)
		}
	case []any:
		for _, child := range n {
			walk(child, fn)
		}
	}
}

func graphqlPost(ctx context.Context, client *http.Client, lsd, appID, postID, referer string) []post {
	variables, _ := json.Marshal(map[string]string{"postID": postID})
	body := url.Values{}
	body.Set("lsd", lsd)
	body.Set("variables", string(variables))
	body.Set("doc_id", defaultDocPost)

	for _, endpoint := range strings.Split(graphqlHosts, "|") {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body.Encode()))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Ig-App-Id", appID)
		req.Header.Set("X-Fb-Lsd", lsd)
		req.Header.Set("X-Asbd-Id", defaultASBDID)
		req.Header.Set("X-Fb-Friendly-Name", "BarcelonaPostPageQuery")
		req.Header.Set("Origin", endpoint)
		req.Header.Set("Referer", referer)
		req.Header.Set("Accept", "*/*")
		req.Header.Set("User-Agent", defaultUA)

		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			continue
		}
		text := string(raw)
		if text == "" || strings.HasPrefix(strings.TrimSpace(text), "<") {
			continue
		}
		var parsed map[string]any
		if json.Unmarshal(raw, &parsed) != nil {
			continue
		}
		return collectPosts([]map[string]any{parsed})
	}
	return nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, _ := m[k].(string); strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func numOf(vals ...any) int64 {
	for _, v := range vals {
		if v == nil {
			continue
		}
		switch n := v.(type) {
		case float64:
			return int64(n)
		case float32:
			return int64(n)
		case int:
			return int64(n)
		case int64:
			return n
		case json.Number:
			i, _ := n.Int64()
			return i
		}
	}
	return 0
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
