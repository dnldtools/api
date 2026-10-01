package music

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type localFormat struct {
	path       string
	ext        string
	quality    string
	codec      string
	label      string
	bitrate    int64
	bitDepth   int
	sampleRate int
}

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
		files, err := s.downloadOne(ctx, p, *t, tmpDir, prefix)
		if err != nil {
			s.logger.Warn("music track failed", "platform", p, "id", t.ID, "error", err)
			continue
		}
		t.Formats = nil
		uploaded := 0
		for j, f := range files {
			signed, mediaID, size, err := s.uploadFile(ctx, p, t.ID, j, f.path, f.ext)
			if err != nil {
				s.logger.Warn("music upload failed", "platform", p, "id", t.ID, "ext", f.ext, "error", err)
				continue
			}
			t.Formats = append(t.Formats, TrackFormat{
				Type:       "audio",
				URL:        signed,
				Ext:        f.ext,
				Quality:    f.quality,
				Codec:      f.codec,
				Label:      f.label,
				Bitrate:    f.bitrate,
				BitDepth:   f.bitDepth,
				SampleRate: f.sampleRate,
				Size:       size,
				MediaID:    mediaID,
			})
			if t.URL == "" {
				t.URL = signed
				t.Ext = f.ext
				t.Size = size
				t.MediaID = mediaID
			}
			uploaded++
		}
		if uploaded == 0 {
			s.logger.Warn("music track produced no uploads", "platform", p, "id", t.ID)
			continue
		}
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
		outPrefix := filepath.Join(tmpDir, prefix)
		out, stderr, err := s.run(ctx, s.cfg.PythonBin, s.appleDir, []string{wv, t.ID, "--device", device, "--sf", sf, "--out", outPrefix}, PlatformApple)
		if err != nil {
			s.logger.Warn("music apple track failed", "id", t.ID, "error", err, "stderr", strings.TrimSpace(stderr))
			continue
		}
		raw, perr := parseJSON(out)
		if perr != nil {
			s.logger.Warn("music apple parse failed", "id", t.ID, "error", perr, "stderr", strings.TrimSpace(stderr))
			continue
		}
		if _, present := raw["ok"]; present && !boolv(raw, "ok") {
			s.logger.Warn("music apple no output", "id", t.ID, "stderr", strings.TrimSpace(stderr))
			continue
		}
		files := parseFileList(raw)
		if len(files) == 0 {
			s.logger.Warn("music apple produced no files", "id", t.ID, "stderr", strings.TrimSpace(stderr))
			continue
		}
		t.Formats = nil
		uploaded := 0
		for j, f := range files {
			signed, mediaID, size, uerr := s.uploadFile(ctx, PlatformApple, t.ID, j, f.path, f.ext)
			if uerr != nil {
				s.logger.Warn("music apple upload failed", "id", t.ID, "ext", f.ext, "error", uerr)
				continue
			}
			t.Formats = append(t.Formats, TrackFormat{
				Type:     "audio",
				URL:      signed,
				Ext:      f.ext,
				Quality:  f.quality,
				Codec:    f.codec,
				Label:    f.label,
				Bitrate:  f.bitrate,
				BitDepth: f.bitDepth,
				Size:     size,
				MediaID:  mediaID,
			})
			if t.URL == "" {
				t.URL = signed
				t.Ext = f.ext
				t.Size = size
				t.MediaID = mediaID
			}
			uploaded++
		}
		if uploaded == 0 {
			continue
		}
		ok++
	}
	if ok == 0 {
		return ErrDownloadFailed
	}
	return nil
}

func (s *Service) downloadOne(ctx context.Context, p Platform, t Track, tmpDir, prefix string) ([]localFormat, error) {
	switch p {
	case PlatformAmazon:
		return s.downloadOneAmazon(ctx, t, tmpDir)
	case PlatformSoundCloud:
		return s.downloadOneSoundCloud(ctx, t, tmpDir, prefix)
	case PlatformTidal:
		return s.downloadOneTidal(ctx, t, tmpDir, prefix)
	}
	return nil, ErrUnsupportedPlatform
}

func (s *Service) downloadOneAmazon(ctx context.Context, t Track, tmpDir string) ([]localFormat, error) {
	out, stderr, err := s.run(ctx, s.cfg.NodeBin, s.amazonDir, []string{"amazon.js", "download", t.ID, "--outdir", tmpDir, "--device", s.devicePath(PlatformAmazon)}, PlatformAmazon)
	if err != nil {
		return nil, fmt.Errorf("amazon download %s: %w: %s", t.ID, err, strings.TrimSpace(stderr))
	}
	raw, perr := parseJSON(out)
	if perr != nil {
		return nil, perr
	}
	if _, present := raw["ok"]; present && !boolv(raw, "ok") {
		return nil, fmt.Errorf("amazon %s: %s", t.ID, str(raw, "error"))
	}
	files := parseFileList(raw)
	if len(files) == 0 {
		return nil, fmt.Errorf("amazon %s: no output files", t.ID)
	}
	return files, nil
}

func (s *Service) downloadOneSoundCloud(ctx context.Context, t Track, tmpDir, prefix string) ([]localFormat, error) {
	out, stderr, err := s.run(ctx, s.cfg.NodeBin, s.soundcloudDir, []string{"soundcloud.js", "download", t.ID, "--out", filepath.Join(tmpDir, prefix)}, PlatformSoundCloud)
	if err != nil {
		return nil, fmt.Errorf("soundcloud download %s: %w: %s", t.ID, err, strings.TrimSpace(stderr))
	}
	raw, perr := parseJSON(out)
	if perr != nil {
		return nil, perr
	}
	if _, present := raw["ok"]; present && !boolv(raw, "ok") {
		return nil, fmt.Errorf("soundcloud %s: %s", t.ID, str(raw, "error"))
	}
	files := parseFileList(raw)
	if len(files) == 0 {
		return nil, fmt.Errorf("soundcloud %s: no output files", t.ID)
	}
	return files, nil
}

func (s *Service) downloadOneTidal(ctx context.Context, t Track, tmpDir, prefix string) ([]localFormat, error) {
	out, stderr, err := s.run(ctx, s.cfg.NodeBin, s.tidalDir, []string{"tidal.js", "download", t.ID, "--out", filepath.Join(tmpDir, prefix)}, PlatformTidal)
	if err != nil {
		return nil, fmt.Errorf("tidal download %s: %w: %s", t.ID, err, strings.TrimSpace(stderr))
	}
	raw, perr := parseJSON(out)
	if perr != nil {
		return nil, perr
	}
	if _, present := raw["ok"]; present && !boolv(raw, "ok") {
		return nil, fmt.Errorf("tidal %s: %s", t.ID, str(raw, "error"))
	}
	files := parseFileList(raw)
	if len(files) == 0 {
		return nil, fmt.Errorf("tidal %s: no output files", t.ID)
	}
	return files, nil
}

func parseFileList(raw map[string]any) []localFormat {
	list, _ := raw["files"].([]any)
	var out []localFormat
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		p := str(m, "path")
		if p == "" {
			continue
		}
		if _, statErr := os.Stat(p); statErr != nil {
			continue
		}
		ext := str(m, "ext")
		if ext == "" {
			ext = extFromPath(p)
		}
		out = append(out, localFormat{
			path:       p,
			ext:        ext,
			quality:    str(m, "quality"),
			codec:      str(m, "codec"),
			label:      str(m, "label"),
			bitrate:    int64v(m, "bitrate"),
			bitDepth:   intv(m, "bitDepth"),
			sampleRate: intv(m, "sampleRate"),
		})
	}
	return out
}

func extFromPath(p string) string {
	e := strings.TrimPrefix(filepath.Ext(p), ".")
	if e == "" {
		return "m4a"
	}
	return e
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
