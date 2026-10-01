package music

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func isNotFound(err error, stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "404") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "can't resolve") ||
		strings.Contains(lower, "cannot resolve") ||
		strings.Contains(lower, "resource with requested") ||
		strings.Contains(lower, "no asin") ||
		strings.Contains(lower, "cannot read properties of null") ||
		strings.Contains(lower, "unexpected token '<'") ||
		strings.Contains(lower, "json.parse")
}

func (s *Service) resolve(ctx context.Context, p Platform, url string) (*resolveData, error) {
	switch p {
	case PlatformAmazon:
		return s.resolveAmazon(ctx, url)
	case PlatformApple:
		return s.resolveApple(ctx, url)
	case PlatformSoundCloud:
		return s.resolveSoundCloud(ctx, url)
	case PlatformTidal:
		return s.resolveTidal(ctx, url)
	}
	return nil, ErrUnsupportedPlatform
}

func parseJSON(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("music: parse stdout: %w", err)
	}
	return m, nil
}

func parseJSONArray(data []byte) ([]any, error) {
	var a []any
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("music: parse stdout: %w", err)
	}
	return a, nil
}

func str(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		if v == nil {
			return ""
		}
		return fmt.Sprint(v)
	}
	return ""
}

func intv(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func int64v(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return 0
}

func boolv(m map[string]any, k string) bool {
	if v, ok := m[k]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func durationMsOf(m map[string]any) int64 {
	for _, k := range []string{"durationMs", "durationInMillis"} {
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				return int64(n)
			case string:
				f, _ := strconv.ParseFloat(n, 64)
				return int64(f)
			}
		}
	}
	for _, k := range []string{"durationSec", "durationSeconds", "duration"} {
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				return int64(n * 1000)
			case int:
				return int64(n) * 1000
			case string:
				f, _ := strconv.ParseFloat(n, 64)
				return int64(f * 1000)
			}
		}
	}
	return 0
}

func artworkMap(m map[string]any) map[string]string {
	out := map[string]string{}
	var best string
	var bestW float64
	for _, key := range []string{"artwork", "cover"} {
		v, ok := m[key]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case map[string]any:
			for k, val := range t {
				if s, ok := val.(string); ok && s != "" {
					out[k] = s
				}
			}
		case []any:
			for _, e := range t {
				em, ok := e.(map[string]any)
				if !ok {
					continue
				}
				u := asString(em["url"])
				if u == "" {
					continue
				}
				var w float64
				switch n := em["width"].(type) {
				case float64:
					w = n
				case string:
					w, _ = strconv.ParseFloat(n, 64)
				}
				if w > bestW {
					bestW = w
					best = u
				}
			}
		}
	}
	if best != "" {
		out["original"] = best
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func pickThumb(a map[string]string) string {
	for _, k := range []string{"1280", "750", "640", "500", "original", "large", "320", "300", "160", "120"} {
		if v, ok := a[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

func (s *Service) resolveAmazon(ctx context.Context, url string) (*resolveData, error) {
	out, stderr, err := s.run(ctx, s.cfg.NodeBin, s.amazonDir, []string{"amazon.js", "resolve", url, "--limit", "100"}, PlatformAmazon)
	if err != nil {
		if isNotFound(err, stderr) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(stderr))
		}
		return nil, fmt.Errorf("music: amazon resolve: %w: %s", err, strings.TrimSpace(stderr))
	}
	raw, err := parseJSON(out)
	if err != nil {
		return nil, err
	}
	d := &resolveData{
		Platform: PlatformAmazon,
		Type:     str(raw, "type"),
		ID:       str(raw, "id"),
		Title:    str(raw, "title"),
		Artist:   str(raw, "artist"),
		Artwork:  artworkMap(raw),
		Raw:      raw,
	}
	d.Thumbnail = pickThumb(d.Artwork)
	d.Year = firstN(str(raw, "releaseDate"), 4)
	d.TrackCount = intv(raw, "trackCount")
	if d.TrackCount == 0 {
		if results, ok := raw["results"].([]any); ok {
			d.TrackCount = len(results)
		} else if d.Type == "track" {
			d.TrackCount = 1
		}
	}
	d.DurationMs = durationMsOf(raw)

	switch d.Type {
	case "track":
		d.Tracks = []Track{trackFromAmazon(raw)}
	case "album", "playlist":
		d.Album = str(raw, "title")
		if d.Artist == "" {
			d.Artist = firstArtist(raw)
		}
		for _, e := range asList(raw["results"]) {
			if em, ok := e.(map[string]any); ok {
				d.Tracks = append(d.Tracks, trackFromAmazon(em))
			}
		}
	case "artist":
	default:
	}
	return d, nil
}

func trackFromAmazon(m map[string]any) Track {
	artist := firstArtist(m)
	album := ""
	if al, ok := m["album"].(map[string]any); ok {
		album = str(al, "title")
	}
	return Track{
		ID:          str(m, "id"),
		Title:       str(m, "title"),
		Artist:      artist,
		Album:       album,
		TrackNumber: intv(m, "trackNumber"),
		DiscNumber:  intv(m, "discNumber"),
		DurationMs:  durationMsOf(m),
		ISRC:        str(m, "isrc"),
		Artwork:     artworkMap(m),
		PreviewURL:  str(m, "previewUrl"),
		Metadata:    m,
	}
}

func firstArtist(m map[string]any) string {
	if arts, ok := m["artists"].([]any); ok && len(arts) > 0 {
		if a0, ok := arts[0].(map[string]any); ok {
			return str(a0, "name")
		}
	}
	return ""
}

func (s *Service) resolveApple(ctx context.Context, url string) (*resolveData, error) {
	typ, id, sf, songID := parseAppleURL(url)

	if typ == "song" || (typ == "album" && songID != "") {
		sid := id
		if songID != "" {
			sid = songID
		}
		out, stderr, err := s.run(ctx, s.cfg.NodeBin, s.appleDir, []string{"apple.js", "song", sid, "--sf", sf}, PlatformApple)
		if err != nil {
			if isNotFound(err, stderr) {
				return nil, fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(stderr))
			}
			return nil, fmt.Errorf("music: apple song: %w: %s", err, strings.TrimSpace(stderr))
		}
		raw, err := parseJSON(out)
		if err != nil {
			return nil, err
		}
		t := trackFromApple(raw)
		return &resolveData{
			Platform:   PlatformApple,
			Type:       "track",
			ID:         t.ID,
			Title:      t.Title,
			Artist:     t.Artist,
			Album:      t.Album,
			DurationMs: t.DurationMs,
			Artwork:    t.Artwork,
			Thumbnail:  pickThumb(t.Artwork),
			Tracks:     []Track{t},
			Storefront: sf,
			Raw:        raw,
		}, nil
	}

	if typ == "album" {
		metaOut, stderr, err := s.run(ctx, s.cfg.NodeBin, s.appleDir, []string{"apple.js", "album", id, "--sf", sf}, PlatformApple)
		if err != nil {
			if isNotFound(err, stderr) {
				return nil, fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(stderr))
			}
			return nil, fmt.Errorf("music: apple album: %w: %s", err, strings.TrimSpace(stderr))
		}
		meta, err := parseJSON(metaOut)
		if err != nil {
			return nil, err
		}
		tracksOut, stderr, err := s.run(ctx, s.cfg.NodeBin, s.appleDir, []string{"apple.js", "album-tracks", id, "--sf", sf}, PlatformApple)
		if err != nil {
			if isNotFound(err, stderr) {
				return nil, fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(stderr))
			}
			return nil, fmt.Errorf("music: apple album-tracks: %w: %s", err, strings.TrimSpace(stderr))
		}
		arr, err := parseJSONArray(tracksOut)
		if err != nil {
			return nil, err
		}
		d := &resolveData{
			Platform:   PlatformApple,
			Type:       "album",
			ID:         id,
			Title:      str(meta, "title"),
			Artist:     str(meta, "artist"),
			Album:      str(meta, "title"),
			Year:       firstN(str(meta, "releaseDate"), 4),
			TrackCount: intv(meta, "trackCount"),
			Artwork:    artworkMap(meta),
			Storefront: sf,
			Raw:        meta,
		}
		d.Thumbnail = pickThumb(d.Artwork)
		for _, e := range arr {
			if em, ok := e.(map[string]any); ok {
				t := trackFromApple(em)
				if t.Album == "" {
					t.Album = d.Title
				}
				if t.Artist == "" {
					t.Artist = d.Artist
				}
				d.Tracks = append(d.Tracks, t)
			}
		}
		return d, nil
	}

	if typ == "playlist" {
		tracksOut, stderr, err := s.run(ctx, s.cfg.NodeBin, s.appleDir, []string{"apple.js", "playlist", id, "--sf", sf}, PlatformApple)
		if err != nil {
			if isNotFound(err, stderr) {
				return nil, fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(stderr))
			}
			return nil, fmt.Errorf("music: apple playlist: %w: %s", err, strings.TrimSpace(stderr))
		}
		arr, err := parseJSONArray(tracksOut)
		if err != nil {
			return nil, err
		}
		d := &resolveData{
			Platform:   PlatformApple,
			Type:       "playlist",
			ID:         id,
			Title:      "Playlist",
			Storefront: sf,
			Raw:        map[string]any{"type": "playlist", "id": id, "tracks": arr},
		}
		for _, e := range arr {
			if em, ok := e.(map[string]any); ok {
				t := trackFromApple(em)
				d.Tracks = append(d.Tracks, t)
			}
		}
		if len(d.Tracks) > 0 {
			d.Title = d.Tracks[0].Artist + " — Playlist"
		}
		d.TrackCount = len(d.Tracks)
		return d, nil
	}

	if typ == "artist" {
		out, stderr, err := s.run(ctx, s.cfg.NodeBin, s.appleDir, []string{"apple.js", "artist", id, "--sf", sf}, PlatformApple)
		if err != nil {
			if isNotFound(err, stderr) {
				return nil, fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(stderr))
			}
			return nil, fmt.Errorf("music: apple artist: %w: %s", err, strings.TrimSpace(stderr))
		}
		raw, err := parseJSON(out)
		if err != nil {
			return nil, err
		}
		a := artworkMap(raw)
		return &resolveData{
			Platform:   PlatformApple,
			Type:       "artist",
			ID:         id,
			Title:      str(raw, "name"),
			Artist:     str(raw, "name"),
			Artwork:    a,
			Thumbnail:  pickThumb(a),
			Storefront: sf,
			Raw:        raw,
		}, nil
	}

	return nil, ErrUnsupportedPlatform
}

var appleURLRe = regexp.MustCompile(`(?:music\.apple\.com|geo\.music\.apple\.com|itunes\.apple\.com)/(?:([a-z]{2})/)?(song|album|artist|playlist|music-video|music-videos)/(?:[^/]+/)?(?:id)?([A-Za-z0-9._-]+)`)

func parseAppleURL(u string) (typ, id, sf, songID string) {
	sf = "us"
	if i := strings.Index(u, "?i="); i != -1 {
		rest := u[i+3:]
		songID = strings.Split(strings.Split(rest, "&")[0], "#")[0]
	}
	m := appleURLRe.FindStringSubmatch(u)
	if m == nil {
		return "", "", sf, songID
	}
	if m[1] != "" {
		sf = strings.ToLower(m[1])
	}
	id = m[3]
	if strings.HasPrefix(id, "id") && isDigits(id[2:]) {
		id = id[2:]
	}
	return strings.ToLower(m[2]), id, sf, songID
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func trackFromApple(m map[string]any) Track {
	a := artworkMap(m)
	return Track{
		ID:          str(m, "id"),
		Title:       str(m, "title"),
		Artist:      str(m, "artist"),
		Album:       str(m, "album"),
		TrackNumber: intv(m, "trackNumber"),
		DiscNumber:  intv(m, "discNumber"),
		DurationMs:  durationMsOf(m),
		ISRC:        str(m, "isrc"),
		Explicit:    boolv(m, "explicit"),
		Artwork:     a,
		PreviewURL:  str(m, "previewUrl"),
		Metadata:    m,
	}
}

func (s *Service) resolveSoundCloud(ctx context.Context, url string) (*resolveData, error) {
	if target, err := followRedirect(ctx, url); err == nil && target != "" && target != url {
		url = target
	}
	out, stderr, err := s.run(ctx, s.cfg.NodeBin, s.soundcloudDir, []string{"soundcloud.js", url}, PlatformSoundCloud)
	if err != nil {
		if isNotFound(err, stderr) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(stderr))
		}
		return nil, fmt.Errorf("music: soundcloud resolve: %w: %s", err, strings.TrimSpace(stderr))
	}
	raw, err := parseJSON(out)
	if err != nil {
		return nil, err
	}
	typ := str(raw, "type")
	d := &resolveData{
		Platform: PlatformSoundCloud,
		Type:     typ,
		ID:       fmt.Sprint(int64v(raw, "id")),
		Title:    str(raw, "title"),
		Artwork:  artworkMap(raw),
		Raw:      raw,
	}
	d.Thumbnail = pickThumb(d.Artwork)
	switch typ {
	case "track":
		d.Artist = str(raw, "artist")
		d.DurationMs = durationMsOf(raw)
		d.Tracks = []Track{trackFromSoundCloud(raw)}
	case "playlist":
		d.Artist = str(raw, "user")
		d.TrackCount = intv(raw, "trackCount")
		for _, e := range asList(raw["tracks"]) {
			if em, ok := e.(map[string]any); ok {
				d.Tracks = append(d.Tracks, trackFromSoundCloud(em))
			}
		}
	default:
	}
	return d, nil
}

func trackFromSoundCloud(m map[string]any) Track {
	released := str(m, "releaseDate")
	var isrc string
	if pub, ok := m["publisherMetadata"].(map[string]any); ok {
		isrc = str(pub, "isrc")
	}
	return Track{
		ID:          fmt.Sprint(int64v(m, "id")),
		Title:       str(m, "title"),
		Artist:      str(m, "artist"),
		DurationMs:  durationMsOf(m),
		ISRC:        isrc,
		Genre:       str(m, "genre"),
		ReleaseDate: released,
		Year:        firstN(released, 4),
		Artwork:     artworkMap(m),
		Metadata:    m,
	}
}

func (s *Service) resolveTidal(ctx context.Context, url string) (*resolveData, error) {
	out, stderr, err := s.run(ctx, s.cfg.NodeBin, s.tidalDir, []string{"tidal.js", url}, PlatformTidal)
	if err != nil {
		if isNotFound(err, stderr) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(stderr))
		}
		return nil, fmt.Errorf("music: tidal resolve: %w: %s", err, strings.TrimSpace(stderr))
	}
	raw, err := parseJSON(out)
	if err != nil {
		return nil, err
	}
	typ := str(raw, "type")
	d := &resolveData{
		Platform: PlatformTidal,
		Type:     typ,
		ID:       fmt.Sprint(int64v(raw, "id")),
		Title:    str(raw, "title"),
		Artist:   str(raw, "artist"),
		Artwork:  artworkMap(raw),
		Raw:      raw,
	}
	d.Thumbnail = pickThumb(d.Artwork)
	d.Year = firstN(str(raw, "year"), 4)
	d.TrackCount = intv(raw, "trackCount")

	switch typ {
	case "track":
		d.Album = str(raw, "album")
		d.DurationMs = durationMsOf(raw)
		d.Tracks = []Track{trackFromTidal(raw)}
	case "album", "playlist":
		d.Album = str(raw, "title")
		for _, e := range asList(raw["tracks"]) {
			if em, ok := e.(map[string]any); ok {
				d.Tracks = append(d.Tracks, trackFromTidal(em))
			}
		}
		if d.TrackCount == 0 {
			d.TrackCount = len(d.Tracks)
		}
	case "artist":
	default:
	}
	return d, nil
}

func trackFromTidal(m map[string]any) Track {
	a := artworkMap(m)
	return Track{
		ID:          fmt.Sprint(int64v(m, "id")),
		Title:       str(m, "title"),
		Artist:      str(m, "artist"),
		Album:       str(m, "album"),
		TrackNumber: intv(m, "trackNumber"),
		DiscNumber:  intv(m, "discNumber"),
		DurationMs:  durationMsOf(m),
		ISRC:        str(m, "isrc"),
		Explicit:    boolv(m, "explicit"),
		Copyright:   str(m, "copyright"),
		Version:     str(m, "version"),
		Artwork:     a,
		Metadata:    m,
	}
}

func asList(v any) []any {
	if v == nil {
		return nil
	}
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

func firstN(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	return s
}

func followRedirect(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("music: bad url")
	}
	host := strings.ToLower(u.Hostname())
	shortHosts := []string{"on.soundcloud.com", "soundcloud.app.goo.gl", "goo.gl", "m.soundcloud.com"}
	isShort := false
	for _, h := range shortHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			isShort = true
			break
		}
	}
	if !isShort {
		return raw, nil
	}
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("user-agent", mirrorUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	final := resp.Request.URL.String()
	if final == "" || final == raw {
		return raw, nil
	}
	return final, nil
}
