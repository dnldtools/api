package savefrom

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
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
)

const (
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

	defaultOrigin = "https://id.savefrom.net"

	defaultConverterOrigin = "https://du.sf-converter.com"

	// secret is the shared signing secret used by the savefrom worker to sign
	// requests (_s = sha256(url + ts + secret)).
	secret = "b7944d7a59c9cb654228624880e7de59a53842c2d912b449fdf11febcf81cb21"

	// fixedTS mirrors the reference implementation's hardcoded _ts value.
	fixedTS = "1720433117117"

	defaultTimeout = 30 * time.Second

	defaultConverterTimeout = 45 * time.Second

	defaultJSTimeout = 8 * time.Second

	defaultMaxAttempts = 5

	maxResponseBytes = 5 << 20
)

var (
	defaultEndpoints = []string{
		"https://worker.savefrom.net/savefrom.php",
		"https://worker.sf-tools.com/savefrom.php",
	}

	defaultProfiles = []Profile{
		{Origin: "https://id.savefrom.net", Lang: "id", Country: "id"},
		{Origin: "https://en.savefrom.net", Lang: "en", Country: "en"},
		{Origin: "https://sfrom.net", Lang: "en", Country: "id"},
	}

	// errEmptyResult marks the "no media" branch signalled by the packed script
	// calling window.parent.sf.result.showEmptyResult()/show().
	errEmptyResult = stderrors.New("savefrom empty result")

	doneStatuses = map[string]bool{
		"finished":  true,
		"converted": true,
		"completed": true,
	}
)

// Profile describes a savefrom origin/lang/country variation used when rotating
// between worker endpoints.
type Profile struct {
	Origin  string
	Lang    string
	Country string
}

type Config struct {
	// Endpoints are the savefrom worker endpoints (POST form-encoded).
	Endpoints []string

	// ConverterOrigin is the base URL of the sf-converter SSE service.
	ConverterOrigin string

	// Profiles rotate origin/lang/country between attempts.
	Profiles []Profile

	UserAgent string

	// Origin is the referer/origin base used for worker and converter requests.
	Origin string

	Timeout time.Duration

	ConverterTimeout time.Duration

	// JSTimeout caps evaluation of the packed worker response.
	JSTimeout time.Duration

	// MaxAttempts is the number of endpoint/profile rotation attempts.
	MaxAttempts int

	HTTPClient *http.Client
}

func DefaultConfig() Config {
	return Config{
		Endpoints:        append([]string(nil), defaultEndpoints...),
		ConverterOrigin:  defaultConverterOrigin,
		Profiles:         append([]Profile(nil), defaultProfiles...),
		UserAgent:        defaultUserAgent,
		Origin:           defaultOrigin,
		Timeout:          defaultTimeout,
		ConverterTimeout: defaultConverterTimeout,
		JSTimeout:        defaultJSTimeout,
		MaxAttempts:      defaultMaxAttempts,
	}
}

type Provider struct {
	endpoints        []string
	converterOrigin  string
	profiles         []Profile
	userAgent        string
	origin           string
	converterTimeout time.Duration
	jsTimeout        time.Duration
	maxAttempts      int
	client           *http.Client
}

var _ downloader.Provider = (*Provider)(nil)

var _ downloader.URLMatcher = (*Provider)(nil)

func New() *Provider {
	return NewWithConfig(DefaultConfig())
}

func NewWithConfig(cfg Config) *Provider {
	if len(cfg.Endpoints) == 0 {
		cfg.Endpoints = append([]string(nil), defaultEndpoints...)
	}
	if cfg.ConverterOrigin == "" {
		cfg.ConverterOrigin = defaultConverterOrigin
	}
	if len(cfg.Profiles) == 0 {
		cfg.Profiles = append([]Profile(nil), defaultProfiles...)
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}
	if cfg.Origin == "" {
		cfg.Origin = defaultOrigin
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.ConverterTimeout <= 0 {
		cfg.ConverterTimeout = defaultConverterTimeout
	}
	if cfg.JSTimeout <= 0 {
		cfg.JSTimeout = defaultJSTimeout
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &Provider{
		endpoints:        cfg.Endpoints,
		converterOrigin:  strings.TrimRight(cfg.ConverterOrigin, "/"),
		profiles:         cfg.Profiles,
		userAgent:        cfg.UserAgent,
		origin:           strings.TrimRight(cfg.Origin, "/"),
		converterTimeout: cfg.ConverterTimeout,
		jsTimeout:        cfg.JSTimeout,
		maxAttempts:      cfg.MaxAttempts,
		client:           client,
	}
}

func (p *Provider) Name() string { return "savefrom" }

func (p *Provider) Platform() downloader.Platform { return downloader.PlatformSavefrom }

func (p *Provider) Type() downloader.ProviderType { return downloader.ProviderExternalAPI }

// MatchesURL is intentionally broad: savefrom.net is an all-in-one downloader,
// so it accepts any http(s) URL. It is registered after 9xbuddy so that the
// first generic fallback (9xbuddy) wins auto-detection; savefrom remains
// reachable via an explicit platform hint or by reordering registration.
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

	data, err := p.scrape(ctx, inputURL)
	if err != nil {
		return nil, err
	}

	result := p.buildResult(data, inputURL)
	if len(result.Formats) == 0 {
		return nil, downloader.ErrMediaNotFound
	}
	return result, nil
}

type mediaFile struct {
	Kind      string
	Quality   string
	Ext       string
	MediaType string
	HasAudio  bool
	Itag      string
	Filesize  string
	URL       string
}

type scrapeData struct {
	ID       string
	Title    string
	Source   string
	Duration string
	Thumb    string
	Files    []mediaFile
}

type converterJob struct {
	URL          string
	Quality      string
	Title        string
	Status       string
	ConverterURL string
	TaskID       string
}

func (p *Provider) scrape(ctx context.Context, inputURL string) (*scrapeData, error) {
	targets := urlVariants(inputURL)
	var lastErr error

	for attempt := 0; attempt < p.maxAttempts; attempt++ {
		if attempt > 0 {
			if !sleepCtx(ctx, time.Duration(700+attempt*400)*time.Millisecond) {
				return nil, ctx.Err()
			}
		}

		videoURL := targets[attempt%len(targets)]
		profile := p.profiles[attempt%len(p.profiles)]
		endpoint := p.endpoints[attempt%len(p.endpoints)]

		packed, err := p.fetchPacked(ctx, videoURL, endpoint, profile)
		if err != nil {
			lastErr = err
			continue
		}
		if len(packed) < 200 {
			lastErr = downloader.ErrProviderInvalidResponse
			continue
		}

		raw, err := p.evaluatePacked(packed)
		if err != nil {
			lastErr = err
			continue
		}

		return p.buildScrapeData(ctx, raw, inputURL)
	}

	if lastErr == nil {
		lastErr = downloader.ErrProviderUnavailable
	}
	if stderrors.Is(lastErr, errEmptyResult) {
		return nil, downloader.ErrMediaNotFound
	}
	return nil, lastErr
}

func (p *Provider) buildScrapeData(ctx context.Context, raw interface{}, inputURL string) (*scrapeData, error) {
	rawArr, ok := raw.([]interface{})
	if !ok || len(rawArr) == 0 {
		return nil, downloader.ErrProviderInvalidResponse
	}
	item := asMap(rawArr[0])
	if item == nil {
		return nil, downloader.ErrProviderInvalidResponse
	}

	listed := pickDirectFiles(item)
	files := make([]mediaFile, 0, len(listed))
	for _, f := range listed {
		if f.Kind == "converter" {
			if resolved, err := p.resolveConverter(ctx, f.URL); err == nil {
				files = append(files, mediaFile{
					Kind:      "direct",
					Quality:   orFirst(resolved.Quality, f.Quality),
					Ext:       f.Ext,
					MediaType: f.MediaType,
					HasAudio:  true,
					Itag:      f.Itag,
					Filesize:  f.Filesize,
					URL:       resolved.URL,
				})
				continue
			}
		}
		files = append(files, f)
	}

	meta := asMap(item["meta"])
	return &scrapeData{
		ID:       asText(item["id"]),
		Title:    asText(meta["title"]),
		Source:   orFirst(asText(meta["source"]), inputURL),
		Duration: asText(meta["duration"]),
		Thumb:    asText(item["thumb"]),
		Files:    files,
	}, nil
}

func (p *Provider) fetchPacked(ctx context.Context, target, endpoint string, profile Profile) (string, error) {
	body := buildForm(target, profile).Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return "", stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	req.Header.Set("accept", "*/*")
	req.Header.Set("accept-language", profile.Lang+"-"+strings.ToUpper(profile.Country)+","+profile.Lang+";q=0.9,en;q=0.8")
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("origin", profile.Origin)
	req.Header.Set("referer", profile.Origin+"/")
	req.Header.Set("user-agent", p.userAgent)
	req.Header.Set("sec-fetch-dest", "iframe")
	req.Header.Set("sec-fetch-mode", "navigate")
	req.Header.Set("sec-fetch-site", "same-site")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", classifyClientError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", classifyHTTPStatus(resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", classifyClientError(err)
	}
	return string(data), nil
}

// evaluatePacked runs the obfuscated worker response in a sandboxed JS runtime
// and captures the result via window.parent.sf.videoResult.show()/showRows()
// or by intercepting decodeURIComponent (see reference implementation).
func (p *Provider) evaluatePacked(packed string) (interface{}, error) {
	vm := goja.New()

	var liveResult interface{}
	var emptyResult interface{}
	var decodedScript string

	noop := func(goja.FunctionCall) goja.Value { return goja.Undefined() }

	console := vm.NewObject()
	for _, name := range []string{"log", "warn", "error"} {
		_ = console.Set(name, noop)
	}
	_ = vm.Set("console", console)

	_ = vm.Set("atob", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(base64ToBinary(call.Argument(0).String()))
	})

	for _, name := range []string{"alert", "setTimeout", "setInterval", "clearTimeout", "clearInterval"} {
		_ = vm.Set(name, noop)
	}

	location := vm.NewObject()
	_ = location.Set("hostname", "id.savefrom.net")
	_ = location.Set("href", p.origin+"/")
	_ = vm.Set("location", location)

	getElementByID := func(goja.FunctionCall) goja.Value {
		el := vm.NewObject()
		_ = el.Set("innerHTML", "ok")
		return el
	}

	doc := vm.NewObject()
	body := vm.NewObject()
	_ = body.Set("firstChild", goja.Null())
	_ = body.Set("removeChild", noop)
	_ = doc.Set("body", body)
	_ = doc.Set("getElementById", getElementByID)
	_ = vm.Set("document", doc)

	parentDoc := vm.NewObject()
	parentLoc := vm.NewObject()
	_ = parentLoc.Set("hostname", "id.savefrom.net")
	_ = parentDoc.Set("location", parentLoc)
	_ = parentDoc.Set("getElementById", getElementByID)

	result := vm.NewObject()
	_ = result.Set("showEmptyResult", func(call goja.FunctionCall) goja.Value {
		emptyResult = call.Argument(0).Export()
		return goja.Undefined()
	})
	_ = result.Set("show", func(call goja.FunctionCall) goja.Value {
		emptyResult = call.Argument(0).Export()
		return goja.Undefined()
	})

	videoResult := vm.NewObject()
	_ = videoResult.Set("show", func(call goja.FunctionCall) goja.Value {
		if arr, ok := call.Argument(0).Export().([]interface{}); ok {
			liveResult = arr
		} else {
			liveResult = []interface{}{call.Argument(0).Export()}
		}
		return goja.Undefined()
	})
	_ = videoResult.Set("showRows", func(call goja.FunctionCall) goja.Value {
		liveResult = call.Argument(0).Export()
		return goja.Undefined()
	})

	sf := vm.NewObject()
	_ = sf.Set("finishRequest", noop)
	_ = sf.Set("enableElement", noop)
	_ = sf.Set("result", result)
	_ = sf.Set("videoResult", videoResult)

	parent := vm.NewObject()
	_ = parent.Set("document", parentDoc)
	_ = parent.Set("sf", sf)
	_ = vm.Set("parent", parent)

	_ = vm.Set("frameElement", vm.NewObject())

	_ = vm.Set("decodeURIComponent", func(call goja.FunctionCall) goja.Value {
		input := call.Argument(0).String()
		decoded := input
		if d, err := url.PathUnescape(input); err == nil {
			decoded = d
		}
		if strings.Contains(decoded, "showResult") || strings.Contains(decoded, "videoResult.show") {
			decodedScript = decoded
		}
		return vm.ToValue(decoded)
	})

	glob := vm.GlobalObject()
	_ = vm.Set("window", glob)
	_ = vm.Set("self", glob)
	_ = vm.Set("globalThis", glob)

	timer := time.AfterFunc(p.jsTimeout, func() { vm.Interrupt("savefrom eval timeout") })
	_, err := vm.RunString(packed)
	timer.Stop()
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, err)
	}

	if liveResult != nil {
		return liveResult, nil
	}
	if decodedScript != "" {
		parsed, perr := extractJSONFromDecoded(decodedScript)
		if perr != nil {
			return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, perr)
		}
		if parsed != nil {
			return parsed, nil
		}
	}
	if emptyResult != nil {
		em := asMap(emptyResult)
		msg := orFirst(em["html"], em["message"], em["response_type"])
		if msg == "" {
			if b, merr := json.Marshal(emptyResult); merr == nil {
				msg = string(b)
			} else {
				msg = "empty result"
			}
		}
		return nil, fmt.Errorf("%w: %s", errEmptyResult, msg)
	}
	return nil, downloader.ErrProviderInvalidResponse
}

func extractJSONFromDecoded(decoded string) (interface{}, error) {
	const showMarker = "window.parent.sf.videoResult.show("
	const rowsMarker = "window.parent.sf.videoResult.showRows("

	showIdx := strings.Index(decoded, showMarker)
	rowsIdx := strings.Index(decoded, rowsMarker)

	var marker string
	if rowsIdx >= 0 && (showIdx < 0 || rowsIdx < showIdx) {
		marker = rowsMarker
	} else if showIdx >= 0 {
		marker = showMarker
	} else {
		return nil, nil
	}

	payload := decoded[strings.Index(decoded, marker)+len(marker):]
	if payload == "" {
		return nil, nil
	}

	var raw string
	if strings.Contains(marker, "showRows") {
		parts := strings.Split(payload, `"],"`)
		cut := -1
		for i, v := range parts {
			if strings.Contains(v, "window.parent.sf.enableElement") {
				cut = i
				break
			}
		}
		if cut >= 0 {
			raw = strings.Join(parts[:cut], `"],"`) + "]"
		} else {
			raw = strings.Split(payload, ");")[0]
		}
	} else {
		raw = strings.Split(payload, ");")[0]
	}

	var out interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if strings.Contains(marker, "showRows") {
		return out, nil
	}
	return []interface{}{out}, nil
}

func pickDirectFiles(item map[string]interface{}) []mediaFile {
	files := make([]mediaFile, 0)
	seen := make(map[string]bool)

	push := func(f mediaFile) {
		if !isHTTPURL(f.URL) || isPlaceholder(f.URL) || seen[f.URL] {
			return
		}
		seen[f.URL] = true
		files = append(files, f)
	}

	for _, rowRaw := range sliceOf(item["url"]) {
		row := asMap(rowRaw)
		if row == nil {
			continue
		}
		href := str(row["url"])
		if isPlaceholder(href) {
			continue
		}
		if isConverterPayload(href) {
			files = append(files, mediaFile{
				Kind:      "converter",
				Quality:   orFirst(row["quality"], row["subname"]),
				Ext:       orFirst(row["ext"], "mp4"),
				MediaType: orFirst(row["type"], "mp4"),
				HasAudio:  true,
				Filesize:  asText(firstValue(row["filesize"], row["contentLength"])),
				URL:       href,
			})
			continue
		}
		if !isHTTPURL(href) {
			continue
		}
		if strings.Contains(href, "sf-converter.com/proxy") {
			continue
		}

		attrTitle := nestedMapStr(row, "attr", "title")
		videoOnly := boolOf(row["no_audio"]) || strings.Contains(strings.ToLower(attrTitle), "without audio")
		audioOnly := boolOf(row["audio"]) || strings.Contains(strings.ToLower(str(row["type"])), "audio")

		kind := "direct"
		if videoOnly {
			kind = "video-only"
		} else if audioOnly {
			kind = "audio"
		}

		push(mediaFile{
			Kind:      kind,
			Quality:   orFirst(row["quality"], row["subname"]),
			Ext:       str(row["ext"]),
			MediaType: str(row["type"]),
			HasAudio:  !videoOnly,
			Itag:      asText(firstValue(row["itag"])),
			Filesize:  asText(firstValue(row["filesize"], row["contentLength"])),
			URL:       href,
		})
	}

	return files
}

func (p *Provider) resolveConverter(ctx context.Context, convertURL string) (*converterJob, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, convertURL, nil)
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	req.Header.Set("accept", "application/json,text/html;q=0.8")
	req.Header.Set("user-agent", p.userAgent)
	req.Header.Set("referer", p.origin+"/")

	// Mirror fetch(..., { redirect: 'manual' }): keep the 3xx response so the
	// Location header can be inspected instead of following it.
	client := *p.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, classifyClientError(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))

	location := resp.Header.Get("location")
	taskID := taskIDFromLocation(location, p.converterOrigin)
	if taskID == "" {
		return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, stderrors.New("savefrom convert returned no task id"))
	}

	job, err := p.readSSEUntilDone(ctx, taskID)
	if err != nil {
		return nil, err
	}
	downloadURL := str(job["downloadUrl"])
	if !isHTTPURL(downloadURL) {
		return nil, downloader.ErrProviderInvalidResponse
	}
	return &converterJob{
		URL:          downloadURL,
		Quality:      str(job["quality"]),
		Title:        str(job["title"]),
		Status:       str(job["status"]),
		ConverterURL: str(job["converterUrl"]),
		TaskID:       taskID,
	}, nil
}

func (p *Provider) readSSEUntilDone(ctx context.Context, taskID string) (map[string]interface{}, error) {
	sseCtx, cancel := context.WithTimeout(ctx, p.converterTimeout)
	defer cancel()

	target := p.converterOrigin + "/tasks/" + taskID
	req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, stderrors.Join(downloader.ErrProviderUnavailable, err)
	}
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("user-agent", p.userAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, classifyClientError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, classifyHTTPStatus(resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)

	var dataLines []string
	var last map[string]interface{}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if len(dataLines) == 0 {
				continue
			}
			job := parseSSEData(dataLines)
			dataLines = dataLines[:0]
			if job == nil {
				continue
			}
			last = job
			status := strings.ToLower(str(job["status"]))
			if doneStatuses[status] && str(job["downloadUrl"]) != "" {
				return job, nil
			}
			if status == "failed" {
				return nil, stderrors.Join(downloader.ErrProviderInvalidResponse, stderrors.New(orFirst(job["error"], "savefrom conversion failed")))
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, classifyClientError(err)
	}
	if last != nil && str(last["downloadUrl"]) != "" {
		return last, nil
	}
	return nil, downloader.ErrProviderInvalidResponse
}

func (p *Provider) buildResult(data *scrapeData, inputURL string) *downloader.DownloadResult {
	result := &downloader.DownloadResult{
		Platform:  downloader.PlatformSavefrom,
		URL:       inputURL,
		Title:     data.Title,
		Thumbnail: data.Thumb,
	}

	if n, err := strconv.ParseInt(strings.TrimSpace(data.Duration), 10, 64); err == nil && n > 0 {
		result.DurationMs = n * 1000
	}

	for _, f := range data.Files {
		result.Formats = append(result.Formats, downloader.Format{
			Type:    mediaTypeFromFile(f),
			URL:     f.URL,
			Quality: f.Quality,
			Ext:     f.Ext,
			Size:    parseSize(f.Filesize),
		})
	}

	if len(result.Formats) == 0 {
		return result
	}

	primary := result.Formats[0].Type
	same := true
	for _, f := range result.Formats[1:] {
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

func mediaTypeFromFile(f mediaFile) downloader.MediaType {
	if f.Kind == "audio" {
		return downloader.MediaAudio
	}
	s := strings.ToLower(strings.TrimSpace(f.MediaType))
	if s == "" {
		s = strings.ToLower(strings.TrimSpace(f.Ext))
	}
	switch {
	case containsAny(s, "audio", "mp3", "m4a", "aac", "wav", "ogg"):
		return downloader.MediaAudio
	case containsAny(s, "image", "jpg", "jpeg", "png", "webp", "gif", "bmp"):
		return downloader.MediaImage
	default:
		return downloader.MediaVideo
	}
}

func buildForm(target string, profile Profile) url.Values {
	ts := time.Now().UnixMilli()
	tsStr := strconv.FormatInt(ts, 10)

	form := url.Values{}
	form.Set("sf_url", target)
	form.Set("sf_submit", "")
	form.Set("new", "2")
	form.Set("lang", profile.Lang)
	form.Set("app", "")
	form.Set("country", profile.Country)
	form.Set("os", "Windows")
	form.Set("browser", "Chrome")
	form.Set("channel", "main")
	form.Set("sf-nomad", "1")
	form.Set("url", target)
	form.Set("ts", tsStr)
	form.Set("_ts", fixedTS)
	form.Set("_tsc", "0")
	form.Set("_s", sha256Hex(target+tsStr+secret))
	form.Set("_x", "1")
	return form
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func urlVariants(u string) []string {
	id := youtubeID(u)
	list := []string{u}
	if id != "" {
		list = append(list,
			"https://www.youtube.com/watch?v="+id,
			"https://youtu.be/"+id,
			"https://www.youtube.com/watch?v="+id+"&hl=en",
		)
	}

	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, v := range list {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func youtubeID(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.Contains(host, "youtu.be") {
		segs := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(segs) > 0 && segs[0] != "" {
			return segs[0]
		}
		return ""
	}
	if v := parsed.Query().Get("v"); v != "" {
		return v
	}
	if m := reYouTubePath.FindStringSubmatch(parsed.Path); len(m) > 2 {
		return m[2]
	}
	return ""
}

func taskIDFromLocation(location, base string) string {
	if location == "" {
		return ""
	}
	u, err := url.Parse(location)
	if err != nil {
		return ""
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return baseURL.ResolveReference(u).Query().Get("t")
}

func parseSSEData(lines []string) map[string]interface{} {
	joined := strings.Join(lines, "")
	if joined == "" {
		return nil
	}
	var job map[string]interface{}
	if err := json.Unmarshal([]byte(joined), &job); err != nil {
		return nil
	}
	return job
}

func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	return 0
}

func base64ToBinary(b64 string) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return ""
	}
	return string(raw)
}

func isHTTPURL(v string) bool {
	return v != "" && reHTTPURL.MatchString(v)
}

func isPlaceholder(v string) bool {
	if v == "" {
		return true
	}
	return strings.HasPrefix(v, "#") ||
		strings.HasPrefix(v, "/local-converter") ||
		strings.Contains(v, "local-converter?data=")
}

func isConverterPayload(v string) bool {
	return v != "" && reConverterPayload.MatchString(v)
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

func asText(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "1"
		}
		return ""
	}
	return ""
}

func boolOf(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func firstValue(values ...interface{}) interface{} {
	for _, v := range values {
		switch t := v.(type) {
		case string:
			if t != "" {
				return t
			}
		case float64:
			if t != 0 {
				return t
			}
		case int64:
			if t != 0 {
				return t
			}
		case int:
			if t != 0 {
				return t
			}
		case bool:
			return t
		}
	}
	return nil
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

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

var (
	reHTTPURL          = regexp.MustCompile(`(?i)^https?://`)
	reConverterPayload = regexp.MustCompile(`sf-converter\.com/convert\?payload=`)
	reYouTubePath      = regexp.MustCompile(`/(shorts|embed|live)/([^/?#]+)`)
)
