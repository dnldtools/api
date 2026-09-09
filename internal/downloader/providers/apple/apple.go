package apple

import (
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
	"sync"
	"time"

	"rest-api/internal/downloader"
)

const (
	defaultAaplBase    = "https://aaplmusicdownloader.com"
	defaultAplmateBase = "https://aplmate.com"
	defaultProxy       = ""
	defaultUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
	defaultQuality     = "m4a"
	maxResponseBytes   = 5 << 20
)

type Config struct {
	AaplBaseURL    string
	AplmateBaseURL string
	// Proxy, when set (e.g. https://cors.siputzx.my.id/), prefixes aapl
	// requests the same way the reference JS client does. Direct (empty)
	// is the default because aapl issues session cookies that most CORS
	// relays drop.
	Proxy string

	Timeout    time.Duration
	UserAgent  string
	Quality    string
	HTTPClient *http.Client
}

func DefaultConfig() Config {
	return Config{
		AaplBaseURL:    defaultAaplBase,
		AplmateBaseURL: defaultAplmateBase,
		Proxy:          defaultProxy,
		Timeout:        45 * time.Second,
		UserAgent:      defaultUserAgent,
		Quality:        defaultQuality,
	}
}

type cookieJar struct {
	mu sync.Mutex
	m  map[string]string
}

func newCookieJar() *cookieJar {
	return &cookieJar{m: make(map[string]string)}
}

func (j *cookieJar) header() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(j.m))
	for k, v := range j.m {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

func (j *cookieJar) store(h http.Header) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, line := range h.Values("Set-Cookie") {
		if line == "" {
			continue
		}
		pair := strings.SplitN(line, ";", 2)[0]
		eq := strings.IndexByte(pair, '=')
		if eq < 1 {
			continue
		}
		name := strings.TrimSpace(pair[:eq])
		value := strings.TrimSpace(pair[eq+1:])
		if value == "" || strings.EqualFold(value, "deleted") {
			delete(j.m, name)
			continue
		}
		j.m[name] = value
	}
}

type Provider struct {
	aapl      string
	aplmate   string
	proxy     string
	userAgent string
	quality   string
	client    *http.Client

	mu           sync.Mutex
	aaplJar      *cookieJar
	aplmateJar   *cookieJar
	aaplReady    bool
	aplmateReady bool
}

var _ downloader.Provider = (*Provider)(nil)

var _ downloader.URLMatcher = (*Provider)(nil)

func New() *Provider {
	return NewWithConfig(DefaultConfig())
}

func NewWithConfig(cfg Config) *Provider {
	if cfg.AaplBaseURL == "" {
		cfg.AaplBaseURL = defaultAaplBase
	}
	if cfg.AplmateBaseURL == "" {
		cfg.AplmateBaseURL = defaultAplmateBase
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}
	if cfg.Quality == "" {
		cfg.Quality = defaultQuality
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &Provider{
		aapl:       strings.TrimRight(cfg.AaplBaseURL, "/"),
		aplmate:    strings.TrimRight(cfg.AplmateBaseURL, "/"),
		proxy:      strings.TrimRight(cfg.Proxy, "/"),
		userAgent:  cfg.UserAgent,
		quality:    cfg.Quality,
		client:     client,
		aaplJar:    newCookieJar(),
		aplmateJar: newCookieJar(),
	}
}

func (p *Provider) Name() string { return "apple" }

func (p *Provider) Platform() downloader.Platform { return downloader.PlatformApple }

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
	for _, suffix := range []string{"music.apple.com", "itunes.apple.com"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func (p *Provider) Resolve(ctx context.Context, req downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	appleURL := strings.TrimSpace(req.URL)
	if appleURL == "" {
		return nil, downloader.ErrInvalidURL
	}

	meta, err := p.resolveMeta(ctx, appleURL)
	if err != nil {
		return nil, err
	}

	formats := make([]downloader.Format, 0, len(meta.tracks))
	var durationMs int64
	for _, tr := range meta.tracks {
		dlink, source, derr := p.resolveDlink(ctx, tr)
		if derr != nil {
			continue
		}
		ext := extFromDlink(dlink)
		quality := strings.TrimSpace(tr.Name)
		if quality == "" {
			quality = p.quality
		}
		formats = append(formats, downloader.Format{
			Type:    downloader.MediaAudio,
			URL:     dlink,
			Quality: quality,
			Ext:     ext,
		})
		if durationMs == 0 {
			durationMs = parseDurationMs(tr.Duration)
		}
		if meta.source == "" {
			meta.source = source
		}
	}
	if len(formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}

	title := strings.TrimSpace(meta.album)
	if title == "" && len(meta.tracks) > 0 {
		title = strings.TrimSpace(meta.tracks[0].Name)
	}
	if meta.artist != "" && title != "" && !strings.Contains(title, meta.artist) {
		title = meta.artist + " — " + title
	}

	result := &downloader.DownloadResult{
		Platform:   downloader.PlatformApple,
		URL:        appleURL,
		Title:      title,
		Type:       downloader.MediaAudio,
		Thumbnail:  meta.thumb,
		DurationMs: durationMs,
		Formats:    formats,
		Metadata: map[string]string{
			"artist": meta.artist,
			"album":  meta.album,
			"kind":   meta.kind,
			"source": meta.source,
			"tracks": strconv.Itoa(len(meta.tracks)),
		},
	}
	return result, nil
}

type appleTrack struct {
	Name     string
	Artist   string
	Album    string
	Duration string
	Thumb    string
	URL      string
	aplmate  *aplmatePayload
}

type appleMeta struct {
	source string
	album  string
	artist string
	thumb  string
	kind   string
	tracks []appleTrack
}

type aplmatePayload struct {
	Data  string
	Base  string
	Token string
}

func (p *Provider) resolveMeta(ctx context.Context, appleURL string) (*appleMeta, error) {
	var errs []error
	if err := p.ensureAapl(ctx); err == nil {
		meta, err := p.aaplMeta(ctx, appleURL)
		if err == nil && len(meta.tracks) > 0 {
			return meta, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	} else {
		errs = append(errs, err)
	}
	if err := p.ensureAplmate(ctx); err == nil {
		meta, err := p.aplmateMeta(ctx, appleURL)
		if err == nil && len(meta.tracks) > 0 {
			return meta, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	} else {
		errs = append(errs, err)
	}
	return nil, classifyJoined(errs)
}

func (p *Provider) resolveDlink(ctx context.Context, tr appleTrack) (string, string, error) {
	var errs []error
	if err := p.ensureAapl(ctx); err == nil {
		dlink, err := p.aaplDlink(ctx, tr)
		if err == nil && dlink != "" {
			return dlink, "aapl", nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	} else {
		errs = append(errs, err)
	}
	if err := p.ensureAplmate(ctx); err == nil {
		dlink, err := p.aplmateDlink(ctx, tr)
		if err == nil && dlink != "" {
			return dlink, "aplmate", nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	} else {
		errs = append(errs, err)
	}
	return "", "", classifyJoined(errs)
}

func (p *Provider) ensureAapl(ctx context.Context) error {
	p.mu.Lock()
	ready := p.aaplReady
	p.mu.Unlock()
	if ready {
		return nil
	}
	_, err := p.do(ctx, http.MethodGet, p.aapl, p.aapl+"/", p.proxy, p.aaplJar, "", nil)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.aaplReady = true
	p.mu.Unlock()
	return nil
}

func (p *Provider) ensureAplmate(ctx context.Context) error {
	p.mu.Lock()
	ready := p.aplmateReady
	p.mu.Unlock()
	if ready {
		return nil
	}
	_, err := p.do(ctx, http.MethodGet, p.aplmate, p.aplmate+"/", "", p.aplmateJar, "", nil)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.aplmateReady = true
	p.mu.Unlock()
	return nil
}

func classifyAppleURL(appleURL string) string {
	u, err := url.Parse(appleURL)
	if err != nil {
		return "album"
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	kind := ""
	if len(parts) > 1 {
		kind = parts[1]
	}
	if kind == "song" || u.Query().Get("i") != "" {
		return "song"
	}
	if kind == "artist" || kind == "playlist" {
		return kind
	}
	return "album"
}

func (p *Provider) aaplMeta(ctx context.Context, appleURL string) (*appleMeta, error) {
	kind := classifyAppleURL(appleURL)
	if kind == "song" {
		endpoint := p.aapl + "/api/applesearch.php?url=" + url.QueryEscape(appleURL)
		if strings.Contains(appleURL, "/song/") {
			endpoint = p.aapl + "/api/song_url.php?url=" + url.QueryEscape(appleURL)
		}
		data, err := p.aaplJSON(ctx, endpoint, http.MethodGet, "", nil)
		if err != nil {
			return nil, err
		}
		name, _ := data["name"].(string)
		if strings.TrimSpace(name) == "" {
			return nil, downloader.ErrProviderInvalidResponse
		}
		artist, _ := data["artist"].(string)
		album, _ := data["albumname"].(string)
		thumb, _ := data["thumb"].(string)
		dur, _ := data["duration"].(string)
		trackURL, _ := data["url"].(string)
		if trackURL == "" {
			trackURL = appleURL
		}
		return &appleMeta{
			source: "aapl",
			album:  album,
			artist: artist,
			thumb:  thumb,
			kind:   "song",
			tracks: []appleTrack{{
				Name:     name,
				Artist:   artist,
				Album:    album,
				Duration: dur,
				Thumb:    thumb,
				URL:      trackURL,
			}},
		}, nil
	}

	data, err := p.aaplJSON(ctx, p.aapl+"/api/pl.php?url="+url.QueryEscape(appleURL), http.MethodGet, "", nil)
	if err != nil {
		return nil, err
	}
	ad, _ := data["album_details"].(map[string]any)
	if ad == nil {
		return nil, downloader.ErrProviderInvalidResponse
	}
	album, _ := ad["album"].(string)
	artist, _ := ad["artist"].(string)
	thumb, _ := ad["thumb"].(string)
	count := anyToInt(ad["count"])
	tracks := make([]appleTrack, 0, count)
	limit := count
	if limit <= 0 {
		limit = 64
	}
	for i := 0; i < limit; i++ {
		raw := ad[strconv.Itoa(i)]
		tmap, ok := raw.(map[string]any)
		if !ok || tmap == nil {
			continue
		}
		name, _ := tmap["name"].(string)
		tArtist, _ := tmap["artist"].(string)
		if tArtist == "" {
			tArtist = artist
		}
		tAlbum, _ := tmap["album"].(string)
		if tAlbum == "" {
			tAlbum = album
		}
		dur, _ := tmap["duration"].(string)
		tThumb, _ := tmap["thumb"].(string)
		if tThumb == "" {
			tThumb = thumb
		}
		link, _ := tmap["link"].(string)
		tracks = append(tracks, appleTrack{
			Name:     name,
			Artist:   tArtist,
			Album:    tAlbum,
			Duration: dur,
			Thumb:    tThumb,
			URL:      link,
		})
	}
	if len(tracks) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	return &appleMeta{
		source: "aapl",
		album:  album,
		artist: artist,
		thumb:  thumb,
		kind:   kind,
		tracks: tracks,
	}, nil
}

func (p *Provider) aaplDlink(ctx context.Context, tr appleTrack) (string, error) {
	form := url.Values{}
	form.Set("song_name", strings.ReplaceAll(strings.ReplaceAll(tr.Name, "'", ""), `"`, ""))
	form.Set("artist_name", strings.ReplaceAll(strings.ReplaceAll(tr.Artist, "'", ""), `"`, ""))
	form.Set("url", tr.URL)
	form.Set("token", "na")
	form.Set("zip_download", "false")
	form.Set("quality", p.quality)
	data, err := p.aaplJSON(ctx, p.aapl+"/api/composer/swd.php", http.MethodPost, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	status, _ := data["status"].(string)
	dlink, _ := data["dlink"].(string)
	if !strings.EqualFold(status, "success") || strings.TrimSpace(dlink) == "" {
		return "", downloader.ErrProviderInvalidResponse
	}
	return dlink, nil
}

func (p *Provider) aaplJSON(ctx context.Context, target, method, contentType string, body io.Reader) (map[string]any, error) {
	raw, err := p.do(ctx, method, p.aapl, target, p.proxy, p.aaplJar, contentType, body)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, downloader.ErrProviderInvalidResponse
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(trimmed), &data); err != nil {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}
	if errStr, _ := data["error"].(string); errStr == "403 Forbidden" {
		return nil, downloader.ErrProviderUnavailable
	}
	return data, nil
}

func (p *Provider) aplmateMeta(ctx context.Context, appleURL string) (*appleMeta, error) {
	token, err := p.aplmateVerify(ctx, appleURL)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("url", appleURL)
	form.Set("cf-turnstile-response", token)
	raw, err := p.do(ctx, http.MethodPost, p.aplmate, p.aplmate+"/action", "", p.aplmateJar, "application/x-www-form-urlencoded; charset=UTF-8", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}
	if errFlag, _ := data["error"].(bool); errFlag {
		return nil, downloader.ErrProviderInvalidResponse
	}
	htmlBody, _ := data["html"].(string)
	tracks := parseAplmateTracks(htmlBody, appleURL)
	if len(tracks) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	album := tracks[0].Album
	if album == "" {
		if m := regexp.MustCompile(`title="([^"]+)"`).FindStringSubmatch(htmlBody); len(m) == 2 {
			album = html.UnescapeString(m[1])
		}
	}
	return &appleMeta{
		source: "aplmate",
		album:  album,
		artist: tracks[0].Artist,
		thumb:  tracks[0].Thumb,
		kind:   classifyAppleURL(appleURL),
		tracks: tracks,
	}, nil
}

func (p *Provider) aplmateVerify(ctx context.Context, appleURL string) (string, error) {
	form := url.Values{}
	form.Set("url", appleURL)
	raw, err := p.do(ctx, http.MethodPost, p.aplmate, p.aplmate+"/action/userverify", "", p.aplmateJar, "application/x-www-form-urlencoded; charset=UTF-8", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}
	ok, _ := data["success"].(bool)
	token, _ := data["token"].(string)
	if !ok || token == "" {
		return "", downloader.ErrProviderInvalidResponse
	}
	return token, nil
}

func (p *Provider) aplmateDlink(ctx context.Context, tr appleTrack) (string, error) {
	payload := tr.aplmate
	if payload == nil || payload.Data == "" || payload.Token == "" {
		meta, err := p.aplmateMeta(ctx, tr.URL)
		if err != nil {
			return "", err
		}
		if len(meta.tracks) == 0 || meta.tracks[0].aplmate == nil {
			return "", downloader.ErrProviderInvalidResponse
		}
		payload = meta.tracks[0].aplmate
	}
	form := url.Values{}
	form.Set("data", payload.Data)
	base := payload.Base
	if base == "" {
		base = tr.URL
	}
	form.Set("base", base)
	form.Set("token", payload.Token)
	raw, err := p.do(ctx, http.MethodPost, p.aplmate, p.aplmate+"/action/track", "", p.aplmateJar, "application/x-www-form-urlencoded; charset=UTF-8", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}
	if errFlag, _ := data["error"].(bool); errFlag {
		return "", downloader.ErrProviderInvalidResponse
	}
	htmlBody, _ := data["data"].(string)
	if htmlBody == "" {
		htmlBody, _ = data["html"].(string)
	}
	m := regexp.MustCompile(`https://cdndl\.aplmate\.com/mp3\?token=[^"'\\\s]+`).FindString(htmlBody)
	if m == "" {
		return "", downloader.ErrMediaNotFound
	}
	return strings.ReplaceAll(m, `\u0026`, "&"), nil
}

func parseAplmateTracks(pageHTML, fallbackURL string) []appleTrack {
	blocks := splitHTMLByName(pageHTML, "submitapurl")
	tracks := make([]appleTrack, 0)
	for i := 1; i < len(blocks); i++ {
		chunk := blocks[i]
		data := attrValue(chunk, "data")
		base := attrValue(chunk, "base")
		if base == "" {
			base = fallbackURL
		}
		token := attrValue(chunk, "token")
		name, artist, album, dur, thumb, surl := "", "", "", "", "", base
		if data != "" {
			if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
				var obj map[string]any
				if json.Unmarshal(decoded, &obj) == nil {
					name, _ = obj["name"].(string)
					artist, _ = obj["artist"].(string)
					album, _ = obj["album"].(string)
					dur, _ = obj["duration"].(string)
					thumb, _ = obj["cover"].(string)
					if s, _ := obj["surl"].(string); s != "" {
						surl = s
					}
				}
			}
		}
		if data == "" && token == "" {
			continue
		}
		tracks = append(tracks, appleTrack{
			Name:     name,
			Artist:   artist,
			Album:    album,
			Duration: dur,
			Thumb:    thumb,
			URL:      surl,
			aplmate:  &aplmatePayload{Data: data, Base: base, Token: token},
		})
	}
	return tracks
}

func splitHTMLByName(s, name string) []string {
	re := regexp.MustCompile(`(?i)name=["']` + regexp.QuoteMeta(name) + `["']`)
	idxs := re.FindAllStringIndex(s, -1)
	if len(idxs) == 0 {
		return []string{s}
	}
	out := make([]string, 0, len(idxs)+1)
	prev := 0
	for _, idx := range idxs {
		out = append(out, s[prev:idx[0]])
		prev = idx[0]
	}
	out = append(out, s[prev:])
	return out
}

func attrValue(htmlChunk, name string) string {
	re := regexp.MustCompile(`(?i)name=["']` + regexp.QuoteMeta(name) + `["']\s+value=["']([^"']*)["']`)
	if m := re.FindStringSubmatch(htmlChunk); len(m) == 2 {
		return html.UnescapeString(m[1])
	}
	re = regexp.MustCompile(`(?i)name=["']` + regexp.QuoteMeta(name) + `["']\s+value='([^']*)'`)
	if m := re.FindStringSubmatch(htmlChunk); len(m) == 2 {
		return html.UnescapeString(m[1])
	}
	return ""
}

func (p *Provider) do(ctx context.Context, method, origin, target, proxy string, jar *cookieJar, contentType string, body io.Reader) ([]byte, error) {
	reqURL := target
	if proxy != "" {
		reqURL = proxy + "/" + strings.TrimPrefix(target, "/")
		if !strings.HasPrefix(target, "http") {
			reqURL = proxy + "/" + strings.TrimPrefix(origin+"/"+strings.TrimPrefix(target, "/"), "/")
		}
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
			reqURL = strings.TrimRight(proxy, "/") + "/" + target
		}
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	httpReq.Header.Set("User-Agent", p.userAgent)
	httpReq.Header.Set("Accept", "application/json, text/html, */*")
	httpReq.Header.Set("Origin", origin)
	httpReq.Header.Set("Referer", origin+"/")
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	if strings.Contains(target, "/action") {
		httpReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	if cookie := jar.header(); cookie != "" {
		httpReq.Header.Set("Cookie", cookie)
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, classifyClientError(err)
	}
	defer resp.Body.Close()
	jar.store(resp.Header)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, classifyClientError(err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, classifyHTTPStatus(resp.StatusCode)
	}
	return raw, nil
}

func extFromDlink(dlink string) string {
	lower := strings.ToLower(dlink)
	u, err := url.Parse(dlink)
	if err == nil {
		path := strings.ToLower(u.Path)
		q := strings.ToLower(u.RawQuery)
		switch {
		case strings.HasSuffix(path, ".mp3") || strings.Contains(q, ".mp3") || strings.Contains(path, "/mp3"):
			return "mp3"
		case strings.HasSuffix(path, ".m4a") || strings.Contains(q, ".m4a"):
			return "m4a"
		}
	}
	switch {
	case strings.Contains(lower, ".mp3"):
		return "mp3"
	case strings.Contains(lower, ".m4a"):
		return "m4a"
	default:
		return "m4a"
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

func classifyJoined(errs []error) error {
	if len(errs) == 0 {
		return downloader.ErrMediaNotFound
	}
	hasTimeout := false
	hasUnavailable := false
	hasInvalid := false
	hasNotFound := false
	for _, err := range errs {
		switch {
		case stderrors.Is(err, context.Canceled):
			return context.Canceled
		case stderrors.Is(err, downloader.ErrProviderTimeout):
			hasTimeout = true
		case stderrors.Is(err, downloader.ErrProviderUnavailable):
			hasUnavailable = true
		case stderrors.Is(err, downloader.ErrMediaNotFound):
			hasNotFound = true
		case stderrors.Is(err, downloader.ErrProviderInvalidResponse):
			hasInvalid = true
		}
	}
	if hasNotFound && !hasUnavailable && !hasTimeout && !hasInvalid {
		return downloader.ErrMediaNotFound
	}
	if hasTimeout && !hasUnavailable {
		return downloader.ErrProviderTimeout
	}
	if hasUnavailable {
		return downloader.ErrProviderUnavailable
	}
	if hasInvalid {
		return downloader.ErrProviderInvalidResponse
	}
	if hasNotFound {
		return downloader.ErrMediaNotFound
	}
	return stderrors.Join(append([]error{downloader.ErrProviderUnavailable}, errs...)...)
}

func anyToInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	default:
		return 0
	}
}

func parseDurationMs(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}
	var hours, mins, secs int
	if strings.Contains(s, "h") || strings.Contains(s, "m") || strings.Contains(s, "s") {
		re := regexp.MustCompile(`(\d+)\s*h`)
		if m := re.FindStringSubmatch(s); len(m) == 2 {
			hours, _ = strconv.Atoi(m[1])
		}
		re = regexp.MustCompile(`(\d+)\s*m`)
		if m := re.FindStringSubmatch(s); len(m) == 2 {
			mins, _ = strconv.Atoi(m[1])
		}
		re = regexp.MustCompile(`(\d+)\s*s`)
		if m := re.FindStringSubmatch(s); len(m) == 2 {
			secs, _ = strconv.Atoi(m[1])
		}
		return int64(((hours*60+mins)*60 + secs) * 1000)
	}
	parts := strings.Split(s, ":")
	if len(parts) == 3 {
		hours, _ = strconv.Atoi(parts[0])
		mins, _ = strconv.Atoi(parts[1])
		secs, _ = strconv.Atoi(parts[2])
		return int64(((hours*60+mins)*60 + secs) * 1000)
	}
	if len(parts) == 2 {
		mins, _ = strconv.Atoi(parts[0])
		secs, _ = strconv.Atoi(parts[1])
		return int64((mins*60 + secs) * 1000)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return int64(n) * 1000
}
