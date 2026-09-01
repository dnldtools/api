package youtube

import "strings"

var audioFormats = []Format{
	{ID: "mp3-320", Type: "audio", Format: "mp3", Quality: "320kbps", Bitrate: "320k", Label: "MP3 - 320kbps"},
	{ID: "mp3-192", Type: "audio", Format: "mp3", Quality: "192kbps", Bitrate: "192k", Label: "MP3 - 192kbps"},
	{ID: "mp3-128", Type: "audio", Format: "mp3", Quality: "128kbps", Bitrate: "128k", Label: "MP3 - 128kbps"},
	{ID: "mp3-64", Type: "audio", Format: "mp3", Quality: "64kbps", Bitrate: "64k", Label: "MP3 - 64kbps"},
	{ID: "wav", Type: "audio", Format: "wav", Label: "WAV"},
	{ID: "m4a", Type: "audio", Format: "m4a", Label: "M4A"},
	{ID: "ogg", Type: "audio", Format: "ogg", Label: "OGG"},
	{ID: "opus", Type: "audio", Format: "opus", Label: "Opus"},
	{ID: "flac", Type: "audio", Format: "flac", Label: "FLAC"},
	{ID: "aac", Type: "audio", Format: "aac", Label: "AAC"},
	{ID: "alac", Type: "audio", Format: "alac", Label: "ALAC"},
}

var videoFormats = []Format{
	{ID: "mp4-2160", Type: "video", Format: "mp4", Quality: "2160p", Premium: true, Label: "MP4 - 4K"},
	{ID: "mp4-1440", Type: "video", Format: "mp4", Quality: "1440p", Premium: true, Label: "MP4 - 2K"},
	{ID: "mp4-1080-premium", Type: "video", Format: "mp4", Quality: "1080p", Premium: true, Label: "MP4 - 1080P Premium"},
	{ID: "mp4-1080", Type: "video", Format: "mp4", Quality: "1080p", Label: "MP4 - 1080P"},
	{ID: "mp4-720", Type: "video", Format: "mp4", Quality: "720p", Label: "MP4 - 720P"},
	{ID: "mp4-480", Type: "video", Format: "mp4", Quality: "480p", Label: "MP4 - 480P"},
	{ID: "mp4-360", Type: "video", Format: "mp4", Quality: "360p", Label: "MP4 - 360P"},
	{ID: "mp4-144", Type: "video", Format: "mp4", Quality: "144p", Label: "MP4 - 144P"},
}

var nonMP3Audio = map[string]bool{
	"wav": true, "m4a": true, "ogg": true, "opus": true,
	"flac": true, "aac": true, "alac": true,
}

func Formats() Catalog {
	return Catalog{
		Audio:                  append([]Format(nil), audioFormats...),
		Video:                  append([]Format(nil), videoFormats...),
		QualityFallback:        true,
		QualityFallbackMessage: "The quality may be lowered if the requested quality isn't available.",
	}
}

func allFormats() []Format {
	return append(append([]Format(nil), audioFormats...), videoFormats...)
}

func lookupPreset(preset string) (Format, bool) {
	for _, f := range allFormats() {
		if f.ID == preset {
			return f, true
		}
	}
	return Format{}, false
}

type formatSelection struct {
	Format  Format
	Output  outputPayload
	Audio   *audioPayload
	Premium bool
}

func selectionFrom(f Format) formatSelection {
	sel := formatSelection{Format: f, Premium: f.Premium}

	output := outputPayload{Type: f.Type, Format: f.Format}
	if f.Type == "audio" && f.Format == "mp3" && f.Bitrate != "" {
		sel.Audio = &audioPayload{Bitrate: f.Bitrate}
	} else {
		output.Quality = f.Quality
	}
	sel.Output = output
	return sel
}

func resolveFormat(req ConvertRequest) (formatSelection, error) {
	preset := strings.ToLower(strings.TrimSpace(req.Preset))
	format := strings.ToLower(strings.TrimSpace(req.Format))
	bitrate := strings.TrimSpace(req.Bitrate)
	quality := strings.TrimSpace(req.Quality)
	typ := strings.ToLower(strings.TrimSpace(req.Type))

	rawPreset := preset
	if rawPreset == "" {
		rawPreset = format
	}

	if rawPreset == "" && bitrate == "" && quality == "" && typ == "" {
		return formatSelection{}, ErrFormatUnavailable
	}

	if rawPreset != "" {
		if f, ok := lookupPreset(rawPreset); ok {
			return selectionFrom(f), nil
		}
	}

	mediaType := "audio"
	if typ == "video" || rawPreset == "mp4" {
		mediaType = "video"
	}

	container := format
	if container == "" {
		if mediaType == "video" {
			container = "mp4"
		} else {
			container = "mp3"
		}
	}
	if i := strings.IndexByte(container, '-'); i >= 0 {
		container = container[:i]
	}

	if mediaType == "audio" {
		if nonMP3Audio[container] {
			if f, ok := lookupPreset(container); ok {
				return selectionFrom(f), nil
			}
			return formatSelection{}, ErrFormatUnavailable
		}
		if bitrate == "" {
			return formatSelection{}, ErrFormatUnavailable
		}
		if f, ok := lookupPreset("mp3-" + normalizeBitrate(bitrate)); ok {
			return selectionFrom(f), nil
		}
		return formatSelection{}, ErrFormatUnavailable
	}

	if quality == "" {
		return formatSelection{}, ErrFormatUnavailable
	}
	q := strings.ToLower(strings.TrimSpace(quality))
	q = strings.TrimSuffix(q, "p")
	if q == "" {
		return formatSelection{}, ErrFormatUnavailable
	}
	if f, ok := lookupPreset("mp4-" + q); ok {
		return selectionFrom(f), nil
	}
	return formatSelection{}, ErrFormatUnavailable
}

func normalizeBitrate(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, "kbps")
	s = strings.TrimSuffix(s, "k")
	return strings.TrimSpace(s)
}

func normalizeTrack(track string) string {
	track = strings.ToLower(strings.TrimSpace(track))
	if track == "" || track == "origin" {
		return ""
	}
	return track
}

func describe(typ, format, quality string) Format {
	for _, f := range allFormats() {
		if f.Type == typ && f.Format == format && f.Quality == quality && !f.Premium {
			return f
		}
	}
	for _, f := range allFormats() {
		if f.Type == typ && f.Format == format && f.Quality == quality {
			return f
		}
	}
	return Format{
		ID:      format + "-" + strings.TrimSuffix(quality, "p"),
		Type:    typ,
		Format:  format,
		Quality: quality,
		Label:   strings.ToUpper(format) + " " + quality,
	}
}
