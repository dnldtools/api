package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultOrigin      = "https://convert1s.com"
	defaultHub         = "https://hub.convert1s.com"
	defaultMeta        = "https://yt-meta.convert1s.com"
	defaultUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
	defaultReqTimeout  = 20 * time.Second
	defaultPollTimeout = 5 * time.Minute
	defaultPollEvery   = 1500 * time.Millisecond
	maxResponseBytes   = 2 << 20
)

// Config holds the upstream endpoints and timeouts used by the YouTube
// service. It is primarily useful for tests; production code uses New().
type Config struct {
	Origin         string
	Hub            string
	Meta           string
	UserAgent      string
	RequestTimeout time.Duration
	PollTimeout    time.Duration
	PollInterval   time.Duration
	HTTPClient     *http.Client
}

// DefaultConfig returns the production upstream configuration.
func DefaultConfig() Config {
	return Config{
		Origin:         defaultOrigin,
		Hub:            defaultHub,
		Meta:           defaultMeta,
		UserAgent:      defaultUserAgent,
		RequestTimeout: defaultReqTimeout,
		PollTimeout:    defaultPollTimeout,
		PollInterval:   defaultPollEvery,
	}
}

// Service implements YouTube search, format catalog, and conversion on top of
// the convert1s upstream. It is a standalone domain service rather than a
// downloader.Provider because search/formats do not fit the
// Resolve(DownloadRequest) contract.
type Service struct {
	cfg    Config
	client *http.Client
}

// New returns a Service using the production defaults.
func New() *Service {
	return NewWithConfig(DefaultConfig())
}

// NewWithConfig returns a Service with the given configuration. Empty values
// are replaced with production defaults.
func NewWithConfig(cfg Config) *Service {
	def := DefaultConfig()
	if cfg.Origin == "" {
		cfg.Origin = def.Origin
	}
	if cfg.Hub == "" {
		cfg.Hub = def.Hub
	}
	if cfg.Meta == "" {
		cfg.Meta = def.Meta
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = def.UserAgent
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = def.RequestTimeout
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = def.PollTimeout
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = def.PollInterval
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return &Service{cfg: cfg, client: client}
}

// Formats returns the conversion format catalog.
func (s *Service) Formats() Catalog { return Formats() }

// Search queries the upstream YouTube search endpoint and returns the matching
// results plus an opaque continuation token.
func (s *Service) Search(ctx context.Context, query string) (*SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, ErrQueryRequired
	}

	target := strings.TrimSuffix(s.cfg.Meta, "/") + "/search?q=" + url.QueryEscape(q)
	data, status, err := s.do(ctx, http.MethodGet, target, nil, nil)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		if msg := upstreamErrorMessage(data); msg != "" {
			return nil, stderrors.Join(ErrProviderInvalidResponse, stderrors.New(msg))
		}
		return nil, classifyHTTPStatus(status)
	}

	var raw struct {
		Items []struct {
			Type         string `json:"type"`
			ID           string `json:"id"`
			Title        string `json:"title"`
			Description  string `json:"description"`
			ThumbnailURL string `json:"thumbnailUrl"`
			UploaderName string `json:"uploaderName"`
			UploaderURL  string `json:"uploaderUrl"`
			Duration     int64  `json:"duration"`
			ViewCount    int64  `json:"viewCount"`
			UploadDate   string `json:"uploadDate"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, stderrors.Join(ErrProviderInvalidResponse, err)
	}

	result := &SearchResult{
		Items:         make([]SearchItem, 0, len(raw.Items)),
		NextPageToken: raw.NextPageToken,
	}
	for _, it := range raw.Items {
		result.Items = append(result.Items, SearchItem{
			Type:         it.Type,
			ID:           it.ID,
			Title:        it.Title,
			Description:  it.Description,
			ThumbnailURL: it.ThumbnailURL,
			UploaderName: it.UploaderName,
			UploaderURL:  it.UploaderURL,
			Duration:     it.Duration,
			ViewCount:    it.ViewCount,
			UploadDate:   it.UploadDate,
		})
	}
	return result, nil
}

// Convert downloads/transcodes a YouTube video into the requested format by
// fanning out to the convert1s worker pool and polling the resulting job
// until it completes.
func (s *Service) Convert(ctx context.Context, req ConvertRequest) (*ConvertResult, error) {
	id, ok := videoID(req.URL)
	if !ok {
		return nil, ErrInvalidURL
	}
	sel, err := resolveFormat(req)
	if err != nil {
		return nil, err
	}
	requested := sel.Format
	watch := watchURL(id)
	job := buildJob(watch, sel, req.Track)

	pollCtx, cancel := context.WithTimeout(ctx, s.cfg.PollTimeout)
	defer cancel()

	workers, err := s.fetchWorkers(pollCtx)
	if err != nil {
		return nil, err
	}
	if len(workers) == 0 {
		return nil, ErrProviderUnavailable
	}

	var lastErr error
	for _, worker := range workers {
		created, err := s.createJob(pollCtx, worker, job)
		if err != nil {
			lastErr = err
			continue
		}

		job, err := s.waitForCompletion(pollCtx, created)
		if err != nil {
			return nil, err
		}

		result := &ConvertResult{
			URL:         watch,
			Title:       job.Title,
			Duration:    job.Duration,
			Requested:   formatSpecFrom(requested),
			DownloadURL: sanitizeDownloadURL(job.DownloadURL),
			Cover:       coverURL(id),
		}
		s.applyQuality(ctx, result, requested, job)
		return result, nil
	}

	return nil, stderrors.Join(ErrProviderUnavailable, lastErr)
}

func (s *Service) fetchWorkers(ctx context.Context) ([]string, error) {
	h, err := s.health(ctx)
	if err != nil {
		return nil, err
	}
	return workerList(h), nil
}

// completedJob carries the raw fields reported by the convert1s worker for a
// finished job, before any probe-based quality resolution.
type completedJob struct {
	Title            string
	Duration         int64
	DownloadURL      string
	RequestedQuality string
	SelectedQuality  string
	QualityChanged   bool
	NeedsReencode    bool
}

// waitForCompletion polls a created job until the upstream marks it completed.
func (s *Service) waitForCompletion(ctx context.Context, created createJobResponse) (completedJob, error) {
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	for {
		polled, err := s.pollStatus(ctx, created.StatusURL)
		if err != nil {
			return completedJob{}, err
		}

		switch polled.Status {
		case "completed":
			job := completedJob{
				Title:            firstNonEmpty(polled.Title, created.Title),
				Duration:         firstNonZero(polled.Duration, created.Duration),
				DownloadURL:      polled.DownloadURL,
				RequestedQuality: created.RequestedQuality,
				SelectedQuality:  firstNonEmpty(polled.SelectedQuality, created.SelectedQuality),
				QualityChanged:   created.QualityChanged || polled.QualityChanged,
				NeedsReencode:    created.NeedsReencode || polled.NeedsReencode,
			}
			if job.DownloadURL == "" {
				return completedJob{}, ErrProviderInvalidResponse
			}
			return job, nil
		case "failed", "error":
			return completedJob{}, ErrMediaNotFound
		}

		select {
		case <-ctx.Done():
			return completedJob{}, ErrProviderTimeout
		case <-ticker.C:
		}
	}
}

func (s *Service) createJob(ctx context.Context, worker string, job createJobRequest) (createJobResponse, error) {
	var out createJobResponse
	body, err := json.Marshal(job)
	if err != nil {
		return out, err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"Origin":       s.cfg.Origin,
		"Referer":      strings.TrimSuffix(s.cfg.Origin, "/") + "/",
	}
	data, status, err := s.do(ctx, http.MethodPost, strings.TrimSuffix(worker, "/")+"/api/download", body, headers)
	if err != nil {
		return out, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		if msg := upstreamErrorMessage(data); msg != "" {
			return out, stderrors.Join(ErrProviderInvalidResponse, stderrors.New(msg))
		}
		return out, classifyHTTPStatus(status)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, stderrors.Join(ErrProviderInvalidResponse, err)
	}
	if out.Error != nil {
		return out, stderrors.Join(ErrProviderInvalidResponse, stderrors.New(out.Error.Message))
	}
	if out.StatusURL == "" {
		return out, ErrProviderInvalidResponse
	}
	return out, nil
}

func (s *Service) pollStatus(ctx context.Context, statusURL string) (pollResponse, error) {
	var out pollResponse
	data, status, err := s.do(ctx, http.MethodGet, relay(statusURL, s.cfg.Hub), nil, nil)
	if err != nil {
		return out, err
	}
	if status == http.StatusNotFound {
		return out, ErrMediaNotFound
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		if msg := upstreamErrorMessage(data); msg != "" {
			return out, stderrors.Join(ErrProviderInvalidResponse, stderrors.New(msg))
		}
		return out, classifyHTTPStatus(status)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, stderrors.Join(ErrProviderInvalidResponse, err)
	}
	if out.Error != nil {
		return out, stderrors.Join(ErrProviderInvalidResponse, stderrors.New(out.Error.Message))
	}
	return out, nil
}

func (s *Service) health(ctx context.Context) (healthResponse, error) {
	var out healthResponse
	data, status, err := s.do(ctx, http.MethodGet, strings.TrimSuffix(s.cfg.Hub, "/")+"/health", nil, nil)
	if err != nil {
		return out, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return out, classifyHTTPStatus(status)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, stderrors.Join(ErrProviderInvalidResponse, err)
	}
	return out, nil
}

// do performs a single HTTP request with the configured per-request timeout.
func (s *Service) do(ctx context.Context, method, target string, body []byte, headers map[string]string) ([]byte, int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()

	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(reqCtx, method, target, rd)
	if err != nil {
		return nil, 0, stderrors.Join(ErrProviderUnavailable, err)
	}
	req.Header.Set("User-Agent", s.cfg.UserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}

	client := s.client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, classifyClientError(err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, 0, classifyClientError(err)
	}
	return data, resp.StatusCode, nil
}

// --- upstream wire types -------------------------------------------------

type healthResponse struct {
	HealthyCount int `json:"healthy_count"`
	Servers      []struct {
		Address string `json:"address"`
		Enabled bool   `json:"enabled"`
		Healthy bool   `json:"healthy"`
		Name    string `json:"name"`
	} `json:"servers"`
}

type createJobRequest struct {
	URL     string        `json:"url"`
	OS      string        `json:"os"`
	Output  outputPayload `json:"output"`
	Audio   *audioPayload `json:"audio,omitempty"`
	Premium bool          `json:"premium,omitempty"`
}

// buildJob assembles the v3 worker payload for a resolved format and an
// optional alternate audio track. The payload always carries `url` and
// `os: "windows"`. The `audio` fragment (bitrate + trackId) is only attached
// to audio conversions: audio.bitrate only for MP3 and audio.trackId only when
// a non-origin track was requested. Video jobs never carry `audio`.
func buildJob(videoURL string, sel formatSelection, track string) createJobRequest {
	job := createJobRequest{
		URL:     videoURL,
		OS:      "windows",
		Output:  sel.Output,
		Premium: sel.Premium,
	}

	if sel.Format.Type != "audio" {
		return job
	}

	var audio *audioPayload
	if sel.Audio != nil {
		cp := *sel.Audio
		audio = &cp
	}
	if id := normalizeTrack(track); id != "" {
		if audio == nil {
			audio = &audioPayload{}
		}
		audio.TrackID = id
	}
	if audio != nil {
		job.Audio = audio
	}
	return job
}

type createJobResponse struct {
	StatusURL            string `json:"statusUrl"`
	Title                string `json:"title"`
	Duration             int64  `json:"duration"`
	RequestedQuality     string `json:"requestedQuality"`
	SelectedQuality      string `json:"selectedQuality"`
	QualityChanged       bool   `json:"qualityChanged"`
	NeedsReencode        bool   `json:"needsReencode"`
	AudioLanguageChanged bool   `json:"audioLanguageChanged"`
	Error                *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type pollResponse struct {
	Status          string `json:"status"`
	Progress        int    `json:"progress"`
	Title           string `json:"title"`
	Duration        int64  `json:"duration"`
	DownloadURL     string `json:"downloadUrl"`
	SelectedQuality string `json:"selectedQuality"`
	QualityChanged  bool   `json:"qualityChanged"`
	NeedsReencode   bool   `json:"needsReencode"`
	Error           *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// --- helpers --------------------------------------------------------------

// workerList extracts healthy worker base URLs from a health response. The
// upstream health endpoint now returns full `address` values, so those are
// used directly (the historical name+suffix pool is no longer needed).
func workerList(h healthResponse) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(h.Servers))
	for _, srv := range h.Servers {
		if !srv.Enabled || !srv.Healthy {
			continue
		}
		addr := strings.TrimSuffix(strings.TrimSpace(srv.Address), "/")
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		out = append(out, addr)
	}
	return out
}

var bareIDRe = regexp.MustCompile(`^[\w-]{11}$`)

// videoID extracts the 11-character YouTube video id from a variety of URL
// shapes (watch, youtu.be, shorts, embed, live, v) or a bare id.
func videoID(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if bareIDRe.MatchString(raw) {
		return raw, true
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(strings.TrimPrefix(host, "www."), "m.")

	switch {
	case host == "youtu.be":
		id := firstPathSegment(u.Path)
		if bareIDRe.MatchString(id) {
			return id, true
		}
	case strings.HasSuffix(host, "youtube.com"), strings.HasSuffix(host, "youtube-nocookie.com"):
		for _, prefix := range []string{"/shorts/", "/embed/", "/live/", "/v/"} {
			if strings.HasPrefix(u.Path, prefix) {
				id := firstPathSegment(strings.TrimPrefix(u.Path, prefix))
				if bareIDRe.MatchString(id) {
					return id, true
				}
			}
		}
		if id := u.Query().Get("v"); bareIDRe.MatchString(id) {
			return id, true
		}
	}
	return "", false
}

// watchURL builds the canonical watch URL for a video id.
func watchURL(id string) string {
	return "https://www.youtube.com/watch?v=" + id
}

// coverURL builds the i.ytimg.com thumbnail (cover) URL for a video id.
func coverURL(id string) string {
	return "https://i.ytimg.com/vi/" + id + "/hqdefault.jpg?source=api.dnld.app"
}

// relay rewrites worker status URLs on *.shop / *.site hosts through the hub
// relay endpoint, mirroring the reference scraper's relay logic. Other hosts
// are returned unchanged.
func relay(target, hub string) string {
	u, err := url.Parse(target)
	if err != nil {
		return target
	}
	host := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(host, ".shop") && !strings.HasSuffix(host, ".site") {
		return target
	}

	relayed := strings.TrimSuffix(hub, "/") + "/relay/" + u.Host + u.Path
	if u.RawQuery != "" {
		relayed += "?" + u.RawQuery
	}
	return relayed
}

func firstPathSegment(path string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(path), "/")
	if i := strings.IndexAny(trimmed, "/?"); i >= 0 {
		trimmed = trimmed[:i]
	}
	return trimmed
}

func upstreamErrorMessage(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return ""
	}
	if e.Error.Message != "" {
		return e.Error.Message
	}
	return e.Message
}

func classifyClientError(err error) error {
	switch {
	case stderrors.Is(err, context.Canceled):
		return context.Canceled
	case stderrors.Is(err, context.DeadlineExceeded):
		return ErrProviderTimeout
	}
	var netErr net.Error
	if stderrors.As(err, &netErr) && netErr.Timeout() {
		return ErrProviderTimeout
	}
	return stderrors.Join(ErrProviderUnavailable, err)
}

func classifyHTTPStatus(status int) error {
	if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
		return ErrProviderUnavailable
	}
	return ErrProviderInvalidResponse
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonZero(values ...int64) int64 {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

// formatSpecFrom converts a catalog Format into the compact identity spec used
// in the convert response.
func formatSpecFrom(f Format) FormatSpec {
	return FormatSpec{ID: f.ID, Type: f.Type, Format: f.Format, Quality: f.Quality}
}

// applyQuality resolves the actually produced output quality from the probed
// download file, falling back to the worker's reported selection when probing
// is unavailable. It never reports the requested preset as the actual output.
func (s *Service) applyQuality(ctx context.Context, res *ConvertResult, requested Format, job completedJob) {
	probe := s.probe(ctx, res.DownloadURL, requested.Format)

	actualQuality := requested.Quality
	actualBitrate := probe.BitrateKbps

	switch {
	case probe.BitrateKbps > 0 && requested.Format == "mp3":
		// MP3 quality is expressed as its encoded bitrate, so reflect the
		// probed value in the quality string.
		actualQuality = kbpsString(probe.BitrateKbps)
	case probe.BitrateKbps > 0:
		// WAV/FLAC: the probed bitrate is informational only; these formats
		// carry no bitrate-based quality, so quality stays as requested.
	case job.SelectedQuality != "":
		actualQuality = job.SelectedQuality
		if requested.Type == "audio" {
			if b, ok := parseKbps(job.SelectedQuality); ok {
				actualBitrate = b
			}
		}
	}

	desc := describe(requested.Type, requested.Format, actualQuality)
	res.Output = OutputSpec{
		ID:          desc.ID,
		Type:        desc.Type,
		Format:      desc.Format,
		Quality:     desc.Quality,
		BitrateKbps: actualBitrate,
	}

	res.QualityChanged = actualQuality != requested.Quality || job.QualityChanged
	if res.QualityChanged {
		res.QualityNote = qualityNote(requested.Quality, actualQuality)
	}
}

// qualityNote produces the human-readable note shown when the actual output
// quality differs from the requested one.
func qualityNote(requested, actual string) string {
	if actual == "" || actual == requested {
		return ""
	}
	return "Converted, quality adjusted to " + actual
}

func kbpsString(bitrate int) string {
	return strconv.Itoa(bitrate) + "kbps"
}

func parseKbps(s string) (int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, "kbps")
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// sanitizeDownloadURL normalises any literal \u0026 escape that survived JSON
// decoding (e.g. double-encoded upstream URLs) into a real ampersand so the
// returned URL is immediately usable.
func sanitizeDownloadURL(u string) string {
	return strings.ReplaceAll(u, `\u0026`, "&")
}
