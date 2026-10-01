package music

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"rest-api/internal/downloader"
	"rest-api/internal/media"
	"rest-api/internal/r2"
)

type Platform string

const (
	PlatformAmazon     Platform = "amazon"
	PlatformApple      Platform = "apple-music"
	PlatformSoundCloud Platform = "soundcloud"
	PlatformTidal      Platform = "tidal"
)

type Config struct {
	Root             string
	DeviceWVD        string
	AmazonCookie     string
	AppleCookie      string
	SoundCloudCookie string
	TidalToken       string
	TempDir          string
	Timeout          time.Duration
	ResolveTimeout   time.Duration
	NodeBin          string
	PythonBin        string
}

type Track struct {
	ID          string            `json:"id,omitempty"`
	Title       string            `json:"title,omitempty"`
	Artist      string            `json:"artist,omitempty"`
	Album       string            `json:"album,omitempty"`
	TrackNumber int               `json:"track_number,omitempty"`
	DiscNumber  int               `json:"disc_number,omitempty"`
	DurationMs  int64             `json:"duration_ms,omitempty"`
	ISRC        string            `json:"isrc,omitempty"`
	Explicit    bool              `json:"explicit,omitempty"`
	Genre       string            `json:"genre,omitempty"`
	Copyright   string            `json:"copyright,omitempty"`
	ReleaseDate string            `json:"release_date,omitempty"`
	Year        string            `json:"year,omitempty"`
	Version     string            `json:"version,omitempty"`
	Artwork     map[string]string `json:"artwork,omitempty"`
	PreviewURL  string            `json:"preview_url,omitempty"`
	URL         string            `json:"url,omitempty"`
	Ext         string            `json:"ext,omitempty"`
	Size        int64             `json:"size,omitempty"`
	MediaID     int64             `json:"media_id,omitempty"`
	Metadata    map[string]any    `json:"metadata,omitempty"`
	Formats     []TrackFormat     `json:"formats,omitempty"`
}

type TrackFormat struct {
	Type       string `json:"type"`
	URL        string `json:"url"`
	Ext        string `json:"ext,omitempty"`
	Quality    string `json:"quality,omitempty"`
	Codec      string `json:"codec,omitempty"`
	Label      string `json:"label,omitempty"`
	Bitrate    int64  `json:"bitrate,omitempty"`
	BitDepth   int    `json:"bit_depth,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Size       int64  `json:"size,omitempty"`
	MediaID    int64  `json:"media_id,omitempty"`
}

type Format struct {
	Type       string `json:"type"`
	URL        string `json:"url"`
	Ext        string `json:"ext,omitempty"`
	Quality    string `json:"quality,omitempty"`
	Codec      string `json:"codec,omitempty"`
	Label      string `json:"label,omitempty"`
	Bitrate    int64  `json:"bitrate,omitempty"`
	BitDepth   int    `json:"bit_depth,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Size       int64  `json:"size,omitempty"`
	MediaID    int64  `json:"media_id,omitempty"`
	TrackID    string `json:"track_id,omitempty"`
	Title      string `json:"title,omitempty"`
	Artist     string `json:"artist,omitempty"`
}

type Result struct {
	Platform   Platform         `json:"platform"`
	Type       string           `json:"type"`
	URL        string           `json:"url"`
	ID         string           `json:"id,omitempty"`
	Title      string           `json:"title,omitempty"`
	Artist     string           `json:"artist,omitempty"`
	Album      string           `json:"album,omitempty"`
	Year       string           `json:"year,omitempty"`
	TrackCount int              `json:"track_count,omitempty"`
	DurationMs int64            `json:"duration_ms,omitempty"`
	Thumbnail  string           `json:"thumbnail,omitempty"`
	Artwork    map[string]string `json:"artwork,omitempty"`
	Tracks     []Track          `json:"tracks,omitempty"`
	Formats    []Format         `json:"formats,omitempty"`
	Metadata   map[string]any   `json:"metadata,omitempty"`
	Source     string           `json:"source,omitempty"`
}

type resolveData struct {
	Platform   Platform
	Type       string
	ID         string
	Title      string
	Artist     string
	Album      string
	Year       string
	TrackCount int
	DurationMs int64
	Artwork    map[string]string
	Thumbnail  string
	Tracks     []Track
	Storefront string
	Raw        map[string]any
}

type AppleFallback func(ctx context.Context, url string) (*downloader.DownloadResult, error)

type Service struct {
	cfg            Config
	r2             *r2.Manager
	media          *media.Store
	logger         *slog.Logger
	appleFallback  AppleFallback
	root           string
	amazonDir      string
	appleDir       string
	soundcloudDir  string
	tidalDir       string
	deviceWVD      string
	amazonKuki     string
	appleUserToken string
	tidalTokenFile string
}

func New(cfg Config, mgr *r2.Manager, store *media.Store, logger *slog.Logger) (*Service, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Minute
	}
	if cfg.ResolveTimeout <= 0 {
		cfg.ResolveTimeout = 90 * time.Second
	}
	if cfg.NodeBin == "" {
		cfg.NodeBin = "node"
	}
	if cfg.PythonBin == "" {
		cfg.PythonBin = "python"
	}
	if cfg.TempDir == "" {
		cfg.TempDir = os.TempDir()
	}
	cfg.AmazonCookie = absPath(cfg.AmazonCookie)
	cfg.AppleCookie = absPath(cfg.AppleCookie)
	cfg.SoundCloudCookie = absPath(cfg.SoundCloudCookie)
	cfg.TidalToken = absPath(cfg.TidalToken)
	cfg.DeviceWVD = absPath(cfg.DeviceWVD)
	if cfg.DeviceWVD != "" && !fileExists(cfg.DeviceWVD) {
		cfg.DeviceWVD = ""
	}
	if logger == nil {
		logger = slog.Default()
	}

	root := strings.TrimRight(strings.TrimSpace(cfg.Root), "/\\")
	if root == "" || !dirExists(root) {
		if root != "" {
			logger.Warn("music root not found, using embedded scrapers", "root", root)
		}
		dir, err := materializeScrapers(cfg.TempDir)
		if err != nil {
			return nil, err
		}
		root = dir
	}

	s := &Service{
		cfg:            cfg,
		r2:             mgr,
		media:          store,
		logger:         logger,
		root:           root,
		amazonDir:      filepath.Join(root, "amazon"),
		appleDir:       filepath.Join(root, "apple"),
		soundcloudDir:  filepath.Join(root, "soundcloud"),
		tidalDir:       filepath.Join(root, "tidal"),
		deviceWVD:      cfg.DeviceWVD,
		tidalTokenFile: cfg.TidalToken,
	}
	if !fileExists(s.devicePath(PlatformAmazon)) || !fileExists(s.devicePath(PlatformApple)) {
		logger.Warn("music: device.wvd missing, amazon/apple full-track downloads will fail", "hint", "set MUSIC_DEVICE_WVD")
	}

	if cfg.AppleCookie != "" {
		tok, err := extractCookieValue(cfg.AppleCookie, "media-user-token")
		if err != nil {
			return nil, fmt.Errorf("music: apple cookie: %w", err)
		}
		s.appleUserToken = tok
	}

	if cfg.AmazonCookie != "" {
		kukiPath, err := convertAmazonKuki(cfg.AmazonCookie, cfg.TempDir)
		if err != nil {
			return nil, fmt.Errorf("music: amazon cookie: %w", err)
		}
		s.amazonKuki = kukiPath
	}

	return s, nil
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func (s *Service) devicePath(p Platform) string {
	if s.deviceWVD != "" {
		return s.deviceWVD
	}
	switch p {
	case PlatformAmazon:
		return filepath.Join(s.amazonDir, "device.wvd")
	case PlatformApple:
		return filepath.Join(s.appleDir, "device.wvd")
	}
	return ""
}

func (s *Service) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Service) DeviceWVD() string {
	if s == nil {
		return ""
	}
	return s.deviceWVD
}

func absPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func (s *Service) SetAppleFallback(fn AppleFallback) {
	s.appleFallback = fn
}

func (s *Service) Enabled() bool {
	return s != nil && s.root != ""
}

func (s *Service) Timeout() time.Duration {
	if s == nil || s.cfg.Timeout <= 0 {
		return 15 * time.Minute
	}
	return s.cfg.Timeout
}

func (s *Service) envFor(p Platform) []string {
	switch p {
	case PlatformAmazon:
		if s.amazonKuki != "" {
			return []string{"AMAZON_KUKI=" + s.amazonKuki}
		}
	case PlatformApple:
		var env []string
		if s.appleUserToken != "" {
			env = append(env, "APPLE_MUSIC_USER_TOKEN="+s.appleUserToken)
		}
		if d := s.devicePath(PlatformApple); d != "" {
			env = append(env, "APPLE_DEVICE_WVD="+d)
		}
		return env
	case PlatformTidal:
		if s.tidalTokenFile != "" {
			return []string{"TIDAL_TOKEN_FILE=" + s.tidalTokenFile}
		}
	}
	return nil
}

func (s *Service) run(ctx context.Context, bin, dir string, args []string, p Platform) ([]byte, string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), s.envFor(p)...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return []byte(stdout.String()), stderr.String(), err
}

func DetectPlatform(raw string) Platform {
	u := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(u, "music.amazon") || (strings.Contains(u, "amazon.") && strings.Contains(u, "/music")):
		return PlatformAmazon
	case strings.Contains(u, "music.apple") || strings.Contains(u, "itunes.apple"):
		return PlatformApple
	case strings.Contains(u, "soundcloud.com") || strings.Contains(u, "soundcloud.app.goo.gl") || strings.Contains(u, "on.soundcloud"):
		return PlatformSoundCloud
	case strings.Contains(u, "tidal.com") || strings.Contains(u, "listen.tidal"):
		return PlatformTidal
	}
	return ""
}

func (s *Service) Resolve(ctx context.Context, url string) (*Result, error) {
	p := DetectPlatform(url)
	if p == "" {
		return nil, ErrUnsupportedPlatform
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.ResolveTimeout)
	defer cancel()

	data, err := s.resolve(ctx, p, url)
	if err != nil {
		return nil, err
	}
	return resultFromResolve(p, url, data), nil
}

func (s *Service) Download(ctx context.Context, url string) (*Result, error) {
	p := DetectPlatform(url)
	if p == "" {
		return nil, ErrUnsupportedPlatform
	}
	if s.r2 == nil {
		return nil, ErrR2Required
	}

	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	data, err := s.resolve(ctx, p, url)
	if err != nil {
		if p == PlatformApple && s.appleFallback != nil {
			if r, ferr := s.downloadAppleFallback(ctx, url); ferr == nil {
				return r, nil
			}
		}
		return nil, err
	}

	if p == PlatformApple {
		if err := s.downloadApple(ctx, data); err != nil {
			if s.appleFallback != nil {
				if r, ferr := s.downloadAppleFallback(ctx, url); ferr == nil {
					return r, nil
				}
			}
			return nil, err
		}
	} else {
		if err := s.downloadNative(ctx, p, data); err != nil {
			return nil, err
		}
	}

	return resultFromResolve(p, url, data), nil
}

func resultFromResolve(p Platform, url string, d *resolveData) *Result {
	res := &Result{
		Platform:   p,
		Type:       d.Type,
		URL:        url,
		ID:         d.ID,
		Title:      d.Title,
		Artist:     d.Artist,
		Album:      d.Album,
		Year:       d.Year,
		TrackCount: d.TrackCount,
		DurationMs: d.DurationMs,
		Thumbnail:  d.Thumbnail,
		Artwork:    d.Artwork,
		Tracks:     d.Tracks,
		Metadata:   d.Raw,
		Source:     "native",
	}
	for i := range res.Tracks {
		t := &res.Tracks[i]
		for j := range t.Formats {
			f := &t.Formats[j]
			res.Formats = append(res.Formats, Format{
				Type:       f.Type,
				URL:        f.URL,
				Ext:        f.Ext,
				Quality:    f.Quality,
				Codec:      f.Codec,
				Label:      formatFallbackLabel(f.Ext, f.Quality, f.Label),
				Bitrate:    f.Bitrate,
				BitDepth:   f.BitDepth,
				SampleRate: f.SampleRate,
				Size:       f.Size,
				MediaID:    f.MediaID,
				TrackID:    t.ID,
				Title:      t.Title,
				Artist:     t.Artist,
			})
		}
		if len(t.Formats) == 0 && t.URL != "" {
			res.Formats = append(res.Formats, Format{
				Type:    "audio",
				URL:     t.URL,
				Ext:     t.Ext,
				Label:   formatFallbackLabel(t.Ext, "", ""),
				Size:    t.Size,
				MediaID: t.MediaID,
				TrackID: t.ID,
				Title:   t.Title,
				Artist:  t.Artist,
			})
		}
	}
	if res.Title == "" {
		res.Title = d.Title
	}
	if res.Thumbnail == "" && len(d.Artwork) > 0 {
		res.Thumbnail = d.Artwork["original"]
		if res.Thumbnail == "" {
			res.Thumbnail = d.Artwork["1280"]
		}
		if res.Thumbnail == "" {
			res.Thumbnail = d.Artwork["640"]
		}
	}
	return res
}

func formatFallbackLabel(ext, quality, label string) string {
	if label != "" {
		return label
	}
	kind := strings.ToUpper(strings.TrimPrefix(ext, "."))
	if kind == "" {
		kind = "AUDIO"
	}
	if quality != "" {
		return kind + " · " + quality
	}
	return kind
}

func (s *Service) uploadFile(ctx context.Context, p Platform, trackID string, index int, localPath, ext string) (signedURL string, mediaID int64, size int64, err error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", 0, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, 0, err
	}
	key := objectKey(p, trackID, index, ext)
	up, err := s.r2.Upload(ctx, key, f, st.Size(), mimeByExt(ext))
	if err != nil {
		return "", 0, 0, err
	}
	signed, err := s.r2.PresignOn(ctx, up.Account, up.ObjectKey)
	if err != nil {
		return "", 0, 0, err
	}
	if s.media != nil {
		rec := &media.Record{
			Platform:    string(p),
			SourceURL:   "music://" + trackID,
			ObjectKey:   up.ObjectKey,
			Bucket:      up.Bucket,
			Account:     up.Account,
			ContentType: mimeByExt(ext),
			Size:        st.Size(),
			Status:      media.StatusReady,
		}
		if err := s.media.Create(ctx, rec); err != nil {
			s.logger.Warn("music media record failed", "platform", p, "error", err)
		} else {
			mediaID = rec.ID
		}
	}
	return signed.URL, mediaID, st.Size(), nil
}

func (s *Service) uploadRemote(ctx context.Context, p Platform, trackID string, index int, remoteURL, ext string) (signedURL string, mediaID int64, size int64, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return "", 0, 0, err
	}
	req.Header.Set("user-agent", mirrorUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, 0, fmt.Errorf("music: upstream %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(s.cfg.TempDir, "music-mirror-*")
	if err != nil {
		return "", 0, 0, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	size, err = io.Copy(tmp, resp.Body)
	if err != nil {
		return "", 0, 0, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return "", 0, 0, err
	}
	key := objectKey(p, trackID, index, ext)
	up, err := s.r2.Upload(ctx, key, tmp, size, mimeByExt(ext))
	if err != nil {
		return "", 0, 0, err
	}
	signed, err := s.r2.PresignOn(ctx, up.Account, up.ObjectKey)
	if err != nil {
		return "", 0, 0, err
	}
	if s.media != nil {
		rec := &media.Record{
			Platform:    string(p),
			SourceURL:   remoteURL,
			ObjectKey:   up.ObjectKey,
			Bucket:      up.Bucket,
			Account:     up.Account,
			ContentType: mimeByExt(ext),
			Size:        size,
			Status:      media.StatusReady,
		}
		if err := s.media.Create(ctx, rec); err != nil {
			s.logger.Warn("music media record failed", "platform", p, "error", err)
		} else {
			mediaID = rec.ID
		}
	}
	return signed.URL, mediaID, size, nil
}

func (s *Service) downloadAppleFallback(ctx context.Context, url string) (*Result, error) {
	res, err := s.appleFallback(ctx, url)
	if err != nil {
		return nil, err
	}
	out := &Result{
		Platform: PlatformApple,
		Type:     "album",
		URL:      url,
		Title:    res.Title,
		Source:   "fallback",
		Metadata: map[string]any{"artist": res.Metadata["artist"], "album": res.Metadata["album"]},
	}
	out.Thumbnail = res.Thumbnail
	out.DurationMs = res.DurationMs
	if len(res.Formats) > 0 {
		out.Type = "track"
	}
	for i, f := range res.Formats {
		trackID := fmt.Sprintf("%s-%d", url, i)
		ext := strings.TrimPrefix(f.Ext, ".")
		if ext == "" {
			ext = "m4a"
		}
		signed, mediaID, size, err := s.uploadRemote(ctx, PlatformApple, trackID, i, f.URL, ext)
		if err != nil {
			s.logger.Warn("apple fallback mirror failed", "error", err)
			continue
		}
		tr := Track{
			ID:      trackID,
			Title:   f.Quality,
			URL:     signed,
			Ext:     ext,
			Size:    size,
			MediaID: mediaID,
		}
		out.Tracks = append(out.Tracks, tr)
		out.Formats = append(out.Formats, Format{
			Type:    "audio",
			URL:     signed,
			Ext:     ext,
			Size:    size,
			MediaID: mediaID,
		})
	}
	if len(out.Formats) == 0 {
		return nil, ErrDownloadFailed
	}
	return out, nil
}

func objectKey(p Platform, trackID string, index int, ext string) string {
	sum := sha256.Sum256([]byte(string(p) + ":" + trackID))
	h := hex.EncodeToString(sum[:8])
	e := strings.TrimPrefix(ext, ".")
	if e == "" {
		e = "bin"
	}
	return fmt.Sprintf("music/%s/%s-%d.%s", p, h, index, e)
}

func mimeByExt(ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "mp3":
		return "audio/mpeg"
	case "m4a", "mp4":
		return "audio/mp4"
	case "flac":
		return "audio/flac"
	case "opus", "ogg":
		return "audio/ogg"
	case "m3u8":
		return "application/vnd.apple.mpegurl"
	default:
		return "application/octet-stream"
	}
}

const mirrorUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"

var ErrUnsupportedPlatform = errors.New("music: unsupported platform")
var ErrR2Required = errors.New("music: r2 mirroring is not configured")
var ErrDownloadFailed = errors.New("music: download failed")
var ErrNotFound = errors.New("music: resource not found")

func extractCookieValue(path, name string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var entries []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Name == name && e.Value != "" {
			return e.Value, nil
		}
	}
	return "", fmt.Errorf("cookie %q not found", name)
}

func convertAmazonKuki(path, tempDir string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var entries []struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return "", err
	}
	kuki := make(map[string]map[string]string, len(entries))
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		kuki[e.Name] = map[string]string{"value": e.Value, "domain": e.Domain}
	}
	out, err := json.Marshal(kuki)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(tempDir, "amazon-kuki-*.json")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func sanitizeName(s string) string {
	re := regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)
	return strings.TrimSpace(re.ReplaceAllString(s, "_"))
}

func discoverFile(dir, prefix string) (string, error) {
	if p := filepath.Join(dir, prefix); fileExists(p) {
		return p, nil
	}
	matches, err := filepath.Glob(filepath.Join(dir, prefix+".*"))
	if err != nil {
		return "", err
	}
	for _, m := range matches {
		if strings.Contains(m, ".tagging") || strings.HasSuffix(m, ".cover.jpg") {
			continue
		}
		return m, nil
	}
	return "", fmt.Errorf("music: no output file for %q", prefix)
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func sniffExt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, 12)
	n, _ := io.ReadFull(f, head)
	if n >= 12 && string(head[4:8]) == "ftyp" {
		return "m4a"
	}
	return "mp3"
}
