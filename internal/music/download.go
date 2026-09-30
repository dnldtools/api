package music

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) downloadNative(ctx context.Context, p Platform, d *resolveData) error {
	if len(d.Tracks) == 0 {
		return ErrDownloadFailed
	}
	tmpDir, err := os.MkdirTemp(s.cfg.TempDir, "music-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	ok := 0
	for i := range d.Tracks {
		t := &d.Tracks[i]
		prefix := trackPrefix(t, i)
		path, ext, err := s.downloadOne(ctx, p, *t, tmpDir, prefix)
		if err != nil {
			s.logger.Warn("music track failed", "platform", p, "id", t.ID, "error", err)
			continue
		}
		signed, mediaID, size, err := s.uploadFile(ctx, p, t.ID, i, path, ext)
		if err != nil {
			s.logger.Warn("music upload failed", "platform", p, "id", t.ID, "error", err)
			continue
		}
		t.URL = signed
		t.Ext = ext
		t.Size = size
		t.MediaID = mediaID
		ok++
	}
	if ok == 0 {
		return ErrDownloadFailed
	}
	return nil
}

func (s *Service) downloadApple(ctx context.Context, d *resolveData) error {
	if len(d.Tracks) == 0 {
		return ErrDownloadFailed
	}
	tmpDir, err := os.MkdirTemp(s.cfg.TempDir, "music-apple-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	wv := filepath.Join(s.appleDir, "apple_wvdecrypt.py")
	device := s.devicePath(PlatformApple)
	sf := d.Storefront
	if sf == "" {
		sf = "us"
	}

	ok := 0
	for i := range d.Tracks {
		t := &d.Tracks[i]
		prefix := trackPrefix(t, i)
		outPath := filepath.Join(tmpDir, prefix+".m4a")
		_, stderr, err := s.run(ctx, s.cfg.PythonBin, s.appleDir, []string{wv, t.ID, "--device", device, "--sf", sf, "--out", outPath}, PlatformApple)
		if err != nil {
			s.logger.Warn("music apple track failed", "id", t.ID, "error", err, "stderr", strings.TrimSpace(stderr))
			continue
		}
		if _, statErr := os.Stat(outPath); statErr != nil {
			s.logger.Warn("music apple no output", "id", t.ID, "stderr", strings.TrimSpace(stderr))
			continue
		}
		signed, mediaID, size, err := s.uploadFile(ctx, PlatformApple, t.ID, i, outPath, "m4a")
		if err != nil {
			s.logger.Warn("music apple upload failed", "id", t.ID, "error", err)
			continue
		}
		t.URL = signed
		t.Ext = "m4a"
		t.Size = size
		t.MediaID = mediaID
		ok++
	}
	if ok == 0 {
		return ErrDownloadFailed
	}
	return nil
}

func (s *Service) downloadOne(ctx context.Context, p Platform, t Track, tmpDir, prefix string) (path, ext string, err error) {
	switch p {
	case PlatformAmazon:
		return s.downloadOneAmazon(ctx, t, tmpDir)
	case PlatformSoundCloud:
		return s.downloadOneSoundCloud(ctx, t, tmpDir, prefix)
	case PlatformTidal:
		return s.downloadOneTidal(ctx, t, tmpDir, prefix)
	}
	return "", "", ErrUnsupportedPlatform
}

func (s *Service) downloadOneAmazon(ctx context.Context, t Track, tmpDir string) (path, ext string, err error) {
	out, stderr, err := s.run(ctx, s.cfg.NodeBin, s.amazonDir, []string{"amazon.js", "download", t.ID, "--outdir", tmpDir, "--device", s.devicePath(PlatformAmazon)}, PlatformAmazon)
	if err != nil {
		return "", "", fmt.Errorf("amazon download %s: %w: %s", t.ID, err, strings.TrimSpace(stderr))
	}
	raw, perr := parseJSON(out)
	if perr != nil {
		return "", "", perr
	}
	if _, present := raw["ok"]; present && !boolv(raw, "ok") {
		return "", "", fmt.Errorf("amazon %s: %s", t.ID, str(raw, "error"))
	}
	p := str(raw, "out")
	if p == "" {
		return "", "", fmt.Errorf("amazon %s: no output path", t.ID)
	}
	if _, statErr := os.Stat(p); statErr != nil {
		return "", "", fmt.Errorf("amazon %s: output missing: %v", t.ID, statErr)
	}
	ext = strings.TrimPrefix(filepath.Ext(p), ".")
	if ext == "" {
		ext = "flac"
	}
	return p, ext, nil
}

func (s *Service) downloadOneSoundCloud(ctx context.Context, t Track, tmpDir, prefix string) (path, ext string, err error) {
	_, stderr, err := s.run(ctx, s.cfg.NodeBin, s.soundcloudDir, []string{"soundcloud.js", "download", t.ID, "--out", filepath.Join(tmpDir, prefix)}, PlatformSoundCloud)
	if err != nil {
		return "", "", fmt.Errorf("soundcloud download %s: %w: %s", t.ID, err, strings.TrimSpace(stderr))
	}
	p, derr := discoverFile(tmpDir, prefix)
	if derr != nil {
		return "", "", fmt.Errorf("soundcloud %s: %w", t.ID, derr)
	}
	ext = sniffExt(p)
	if ext == "" {
		ext = "mp3"
	}
	return p, ext, nil
}

func (s *Service) downloadOneTidal(ctx context.Context, t Track, tmpDir, prefix string) (path, ext string, err error) {
	q := s.cfg.TidalQuality
	out, stderr, err := s.run(ctx, s.cfg.NodeBin, s.tidalDir, []string{"tidal.js", "download", t.ID, "--out", filepath.Join(tmpDir, prefix), "--quality", q}, PlatformTidal)
	if err != nil {
		return "", "", fmt.Errorf("tidal download %s: %w: %s", t.ID, err, strings.TrimSpace(stderr))
	}
	p := parseSavedPath(string(out))
	if p == "" {
		p, derr := discoverFile(tmpDir, prefix)
		if derr != nil {
			return "", "", fmt.Errorf("tidal %s: %w", t.ID, derr)
		}
		return p, extFromPath(p), nil
	}
	return p, extFromPath(p), nil
}

func extFromPath(p string) string {
	e := strings.TrimPrefix(filepath.Ext(p), ".")
	if e == "" {
		return "m4a"
	}
	return e
}

func parseSavedPath(stdout string) string {
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "saved:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "saved:"))
		}
	}
	return ""
}

func trackPrefix(t *Track, idx int) string {
	n := t.TrackNumber
	if n <= 0 {
		n = idx + 1
	}
	if t.Artist != "" {
		return sanitizeName(fmt.Sprintf("%02d - %s - %s", n, t.Artist, t.Title))
	}
	return sanitizeName(fmt.Sprintf("%02d - %s", n, t.Title))
}
