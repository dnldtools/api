package ucshare

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"rest-api/internal/downloader"

	"golang.org/x/sync/errgroup"
)

const (
	defaultAPI = "https://m-intldrive.ucweb.com/1/clouddrive"
	defaultUA  = "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
	origin     = "https://drive.ucweb.com"
	maxBody    = 8 << 20
	pageSize   = 100

	mediaResolveConcurrency = 24
)

type Config struct {
	APIBaseURL string
	Timeout    time.Duration
	UserAgent  string
	HTTPClient *http.Client
}

func DefaultConfig() Config {
	return Config{APIBaseURL: defaultAPI, Timeout: 45 * time.Second, UserAgent: defaultUA}
}

type Provider struct {
	api       string
	userAgent string
	client    *http.Client
}

var _ downloader.Provider = (*Provider)(nil)
var _ downloader.URLMatcher = (*Provider)(nil)

func New() *Provider { return NewWithConfig(DefaultConfig()) }

func NewWithConfig(cfg Config) *Provider {
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = defaultAPI
	}
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
	return &Provider{
		api:       strings.TrimRight(cfg.APIBaseURL, "/"),
		userAgent: cfg.UserAgent,
		client:    client,
	}
}

func (p *Provider) Name() string { return "uc-share" }

func (p *Provider) Platform() downloader.Platform { return downloader.PlatformUCShare }

func (p *Provider) Type() downloader.ProviderType { return downloader.ProviderExternalAPI }

func (p *Provider) MatchesURL(raw string) bool {
	text := strings.TrimSpace(raw)
	if text == "" || isSlug(text) {
		return false
	}
	parsed, err := url.Parse(text)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	for _, suffix := range []string{"uc-share.com", "drive.ucweb.com", "drive.uc.cn", "fast.uc.cn"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func pwdFrom(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", downloader.ErrInvalidURL
	}
	if found := urlRe.FindString(text); found != "" {
		text = strings.TrimRight(found, ")].,")
	}
	if isSlug(text) {
		return text, nil
	}
	u, err := url.Parse(text)
	if err != nil {
		return "", downloader.ErrInvalidURL
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", downloader.ErrInvalidURL
	}
	slug := parts[len(parts)-1]
	if slug == "" {
		return "", downloader.ErrInvalidURL
	}
	return slug, nil
}

var (
	urlRe      = regexp.MustCompile(`https?://[^\s]+`)
	mediaExtRe = regexp.MustCompile(`(?i)\.(mp4|mkv|webm|mov|m4v|avi|jpg|jpeg|png|gif|webp|mp3|m4a|aac)$`)
)

func isSlug(s string) bool {
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

func (p *Provider) Resolve(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	pwd, err := pwdFrom(req.URL)
	if err != nil {
		return nil, err
	}

	root, err := p.listDir(ctx, pwd, "", "")
	if err != nil {
		return nil, err
	}
	stoken, _ := nestedString(root, "token_info", "stoken")
	title, _ := nestedString(root, "token_info", "title")
	if stoken == "" {
		return nil, downloader.ErrProviderInvalidResponse
	}
	if title == "" {
		title = pwd
	}

	top := listItems(root)
	items, err := p.collect(ctx, pwd, stoken, title, top)
	if err != nil {
		return nil, err
	}

	files := make([]ucFile, len(items))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(mediaResolveConcurrency)
	for i, item := range items {
		i, item := i, item
		g.Go(func() error {
			dl, preview, dur, _ := p.mediaURL(gctx, pwd, stoken, item.FID, item.Token)
			files[i] = ucFile{
				Name:       item.Name,
				Size:       item.Size,
				Kind:       firstNonEmpty(item.Format, item.Category),
				Download:   dl,
				Preview:    preview,
				DurationMs: dur,
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	formats := make([]downloader.Format, 0, len(files))
	var thumb string
	var durationMs int64
	for _, f := range files {
		if f.Download == "" {
			continue
		}
		formats = append(formats, downloader.Format{
			Type:    mediaTypeOf(f.Kind, f.Name),
			URL:     f.Download,
			Quality: f.Name,
			Ext:     extOf(f.Name),
			Size:    f.Size,
		})
		if thumb == "" && f.Preview != "" {
			thumb = f.Preview
		}
		if durationMs == 0 && f.DurationMs > 0 {
			durationMs = f.DurationMs
		}
	}
	if len(formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}

	primary := formats[0].Type
	same := true
	for _, f := range formats[1:] {
		if f.Type != primary {
			same = false
			break
		}
	}

	shareURL := "https://uc-share.com/s/" + pwd
	result := &downloader.DownloadResult{
		Platform:   downloader.PlatformUCShare,
		URL:        shareURL,
		Title:      title,
		Thumbnail:  thumb,
		DurationMs: durationMs,
		Formats:    formats,
		Metadata: map[string]string{
			"pwd_id":    pwd,
			"share_url": shareURL,
			"files":     strconv.Itoa(len(formats)),
			"source":    "ucweb",
		},
	}
	if same {
		result.Type = primary
	}
	if strings.TrimSpace(req.URL) != "" && !isSlug(strings.TrimSpace(req.URL)) {
		result.URL = strings.TrimSpace(req.URL)
	}
	return result, nil
}

type ucItem struct {
	FID      string
	Token    string
	Name     string
	Dir      bool
	Size     int64
	Category string
	Format   string
}

type ucFile struct {
	Name       string
	Size       int64
	Kind       string
	Download   string
	Preview    string
	DurationMs int64
}

func (p *Provider) collect(ctx context.Context, pwd, stoken, rootName string, top []ucItem) ([]ucItem, error) {
	if len(top) > 0 && allDirs(top) {
		var out []ucItem
		for _, dir := range top {
			nested, err := p.collectDir(ctx, pwd, stoken, dir.FID)
			if err != nil {
				return nil, err
			}
			for i := range nested {
				nested[i].Name = dir.Name + "/" + nested[i].Name
			}
			out = append(out, nested...)
		}
		return out, nil
	}
	return p.collectDir(ctx, pwd, stoken, "")
}

func (p *Provider) collectDir(ctx context.Context, pwd, stoken, pdirFID string) ([]ucItem, error) {
	data, err := p.listDir(ctx, pwd, pdirFID, stoken)
	if err != nil {
		return nil, err
	}
	items := listItems(data)
	out := make([]ucItem, 0, len(items))
	for _, item := range items {
		if item.Dir {
			nested, err := p.collectDir(ctx, pwd, stoken, item.FID)
			if err != nil {
				return nil, err
			}
			for i := range nested {
				nested[i].Name = item.Name + "/" + nested[i].Name
			}
			out = append(out, nested...)
			continue
		}
		if !isMedia(item) {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (p *Provider) listDir(ctx context.Context, pwd, pdirFID, stoken string) (map[string]any, error) {
	body := map[string]any{
		"pwd_id":      pwd,
		"passcode":    "",
		"force":       0,
		"page":        1,
		"size":        pageSize,
		"fetch_total": 1,
	}
	if pdirFID != "" {
		body["pdir_fid"] = pdirFID
		body["stoken"] = stoken
	}
	raw, err := p.apiJSON(ctx, http.MethodPost, "/share/sharepage/v2/detail?pr=UCBrowser&fr=h5", body, stoken)
	if err != nil {
		return nil, err
	}
	data, _ := raw["data"].(map[string]any)
	if data == nil {
		return nil, downloader.ErrProviderInvalidResponse
	}
	return data, nil
}

func (p *Provider) mediaURL(ctx context.Context, pwd, stoken, fid, fidToken string) (string, string, int64, error) {
	q := url.Values{
		"pr":        {"UCBrowser"},
		"fr":        {"h5"},
		"pwd_id":    {pwd},
		"stoken":    {stoken},
		"fid":       {fid},
		"fid_token": {fidToken},
	}
	raw, err := p.apiJSON(ctx, http.MethodGet, "/share/sharepage/video_preview?"+q.Encode(), nil, stoken)
	if err != nil {
		return "", "", 0, err
	}
	data, _ := raw["data"].(map[string]any)
	if data == nil {
		return "", "", 0, downloader.ErrMediaNotFound
	}
	preview, _ := data["preview_url"].(string)
	var playURL string
	if pi, ok := data["play_info"].(map[string]any); ok {
		playURL, _ = pi["url"].(string)
	}
	dur := anyToInt64(data["duration"])
	if dur > 0 && dur < 100000 {
		dur = dur * 1000
	}
	return playURL, preview, dur, nil
}

func (p *Provider) apiJSON(ctx context.Context, method, pathStr string, body map[string]any, stoken string) (map[string]any, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
		}
		rdr = bytes.NewReader(b)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, p.api+pathStr, rdr)
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	httpReq.Header.Set("Accept", "application/json, text/plain, */*")
	httpReq.Header.Set("User-Agent", p.userAgent)
	httpReq.Header.Set("Origin", origin)
	httpReq.Header.Set("Referer", origin+"/")
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if stoken != "" {
		httpReq.Header.Set("X-Clouddrive-St", stoken)
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, classifyClientError(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, classifyClientError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, classifyHTTPStatus(resp.StatusCode)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}
	if code := anyToInt64(parsed["code"]); code != 0 {
		return nil, downloader.ErrProviderInvalidResponse
	}
	return parsed, nil
}

func listItems(data map[string]any) []ucItem {
	detail, _ := data["detail_info"].(map[string]any)
	if detail == nil {
		return nil
	}
	rawList, _ := detail["list"].([]any)
	out := make([]ucItem, 0, len(rawList))
	for _, raw := range rawList {
		m, _ := raw.(map[string]any)
		if m == nil {
			continue
		}
		name, _ := m["file_name"].(string)
		fid, _ := m["fid"].(string)
		tok, _ := m["share_fid_token"].(string)
		cat, _ := m["obj_category"].(string)
		if cat == "" {
			cat, _ = m["category"].(string)
		}
		fmtType, _ := m["format_type"].(string)
		dir, _ := m["dir"].(bool)
		out = append(out, ucItem{
			FID:      fid,
			Token:    tok,
			Name:     name,
			Dir:      dir,
			Size:     anyToInt64(m["size"]),
			Category: cat,
			Format:   fmtType,
		})
	}
	return out
}

func allDirs(items []ucItem) bool {
	if len(items) == 0 {
		return false
	}
	for _, it := range items {
		if !it.Dir {
			return false
		}
	}
	return true
}

func isMedia(item ucItem) bool {
	cat := strings.ToLower(item.Category)
	fmtType := strings.ToLower(item.Format)
	name := strings.ToLower(item.Name)
	if cat == "video" || cat == "image" || cat == "audio" {
		return true
	}
	if strings.HasPrefix(fmtType, "video") || strings.HasPrefix(fmtType, "image") || strings.HasPrefix(fmtType, "audio") {
		return true
	}
	return mediaExtRe.MatchString(name)
}

func mediaTypeOf(kind, name string) downloader.MediaType {
	s := strings.ToLower(kind + " " + name)
	switch {
	case strings.Contains(s, "audio") || strings.HasSuffix(strings.ToLower(name), ".mp3") || strings.HasSuffix(strings.ToLower(name), ".m4a"):
		return downloader.MediaAudio
	case strings.Contains(s, "image") || hasAnySuffix(strings.ToLower(name), ".jpg", ".jpeg", ".png", ".gif", ".webp"):
		return downloader.MediaImage
	default:
		return downloader.MediaVideo
	}
}

func extOf(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	return ext
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func nestedString(m map[string]any, keys ...string) (string, bool) {
	cur := any(m)
	for _, k := range keys {
		obj, _ := cur.(map[string]any)
		if obj == nil {
			return "", false
		}
		cur = obj[k]
	}
	s, ok := cur.(string)
	return s, ok
}

func anyToInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i
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
	if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
		return downloader.ErrProviderUnavailable
	}
	if status == http.StatusNotFound {
		return downloader.ErrMediaNotFound
	}
	return downloader.ErrProviderInvalidResponse
}
