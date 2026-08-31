package youtube

import "strings"

// audioFormats and videoFormats define the catalog exposed by the /formats
// endpoint and accepted by resolveFormat. The set of formats and qualities is
// constrained by the upstream convert1s worker validation:
//
//	audio formats: mp3 m4a wav opus flac ogg aac alac
//	video formats: mp4 webm mkv avi flv mov
//	video qualities: 2160p 1440p 1080p 720p 480p 360p 144p
var audioFormats = []Format{
	{ID: "mp3-320", Type: "audio", Format: "mp3", Quality: "320kbps", Label: "MP3 320kbps", Default: true},
	{ID: "mp3-256", Type: "audio", Format: "mp3", Quality: "256kbps", Label: "MP3 256kbps"},
	{ID: "mp3-192", Type: "audio", Format: "mp3", Quality: "192kbps", Label: "MP3 192kbps"},
	{ID: "mp3-128", Type: "audio", Format: "mp3", Quality: "128kbps", Label: "MP3 128kbps"},
	{ID: "m4a-192", Type: "audio", Format: "m4a", Quality: "192kbps", Label: "M4A 192kbps"},
	{ID: "m4a-128", Type: "audio", Format: "m4a", Quality: "128kbps", Label: "M4A 128kbps"},
	{ID: "wav-best", Type: "audio", Format: "wav", Quality: "best", Label: "WAV Best"},
	{ID: "flac-best", Type: "audio", Format: "flac", Quality: "best", Label: "FLAC Best"},
	{ID: "opus-160", Type: "audio", Format: "opus", Quality: "160kbps", Label: "OPUS 160kbps"},
	{ID: "ogg-192", Type: "audio", Format: "ogg", Quality: "192kbps", Label: "OGG 192kbps"},
	{ID: "aac-128", Type: "audio", Format: "aac", Quality: "128kbps", Label: "AAC 128kbps"},
	{ID: "alac-best", Type: "audio", Format: "alac", Quality: "best", Label: "ALAC Best"},
}

var videoFormats = []Format{
	{ID: "mp4-2160", Type: "video", Format: "mp4", Quality: "2160p", Label: "MP4 4K"},
	{ID: "mp4-1440", Type: "video", Format: "mp4", Quality: "1440p", Label: "MP4 2K"},
	{ID: "mp4-1080", Type: "video", Format: "mp4", Quality: "1080p", Label: "MP4 1080p"},
	{ID: "mp4-720", Type: "video", Format: "mp4", Quality: "720p", Label: "MP4 720p", Default: true},
	{ID: "mp4-480", Type: "video", Format: "mp4", Quality: "480p", Label: "MP4 480p"},
	{ID: "mp4-360", Type: "video", Format: "mp4", Quality: "360p", Label: "MP4 360p"},
	{ID: "mp4-144", Type: "video", Format: "mp4", Quality: "144p", Label: "MP4 144p"},
	{ID: "webm-1080", Type: "video", Format: "webm", Quality: "1080p", Label: "WEBM 1080p"},
	{ID: "webm-720", Type: "video", Format: "webm", Quality: "720p", Label: "WEBM 720p"},
	{ID: "webm-480", Type: "video", Format: "webm", Quality: "480p", Label: "WEBM 480p"},
	{ID: "mkv-2160", Type: "video", Format: "mkv", Quality: "2160p", Label: "MKV 4K"},
	{ID: "mkv-1080", Type: "video", Format: "mkv", Quality: "1080p", Label: "MKV 1080p"},
	{ID: "mkv-720", Type: "video", Format: "mkv", Quality: "720p", Label: "MKV 720p"},
	{ID: "avi-1080", Type: "video", Format: "avi", Quality: "1080p", Label: "AVI 1080p"},
	{ID: "avi-720", Type: "video", Format: "avi", Quality: "720p", Label: "AVI 720p"},
	{ID: "flv-720", Type: "video", Format: "flv", Quality: "720p", Label: "FLV 720p"},
	{ID: "flv-480", Type: "video", Format: "flv", Quality: "480p", Label: "FLV 480p"},
	{ID: "mov-1080", Type: "video", Format: "mov", Quality: "1080p", Label: "MOV 1080p"},
	{ID: "mov-720", Type: "video", Format: "mov", Quality: "720p", Label: "MOV 720p"},
}

var (
	audioFormatsSet = formatSet(audioFormats)
	videoFormatsSet = formatSet(videoFormats)
	videoQualities  = map[string]bool{
		"2160p": true, "1440p": true, "1080p": true, "720p": true,
		"480p": true, "360p": true, "144p": true,
	}
)

func formatSet(formats []Format) map[string]bool {
	set := make(map[string]bool, len(formats))
	for _, f := range formats {
		set[f.Format] = true
	}
	return set
}

// Formats returns a defensive copy of the format catalog.
func Formats() Catalog {
	return Catalog{
		Audio:                  append([]Format(nil), audioFormats...),
		Video:                  append([]Format(nil), videoFormats...),
		QualityFallback:        true,
		QualityFallbackMessage: "Requested quality unavailable. Falling back to best available.",
	}
}

// lookupPreset returns the format for a catalog preset id.
func lookupPreset(preset string) (Format, bool) {
	for _, f := range audioFormats {
		if f.ID == preset {
			return f, true
		}
	}
	for _, f := range videoFormats {
		if f.ID == preset {
			return f, true
		}
	}
	return Format{}, false
}

// resolveFormat turns a ConvertRequest into the concrete output payload and
// its matching public Format descriptor.
//
// Resolution order:
//  1. If `preset` is set it must match a catalog id exactly.
//  2. Otherwise `type`/`format`/`quality` are used (with sensible defaults)
//     and validated against the catalog constraints.
func resolveFormat(req ConvertRequest) (outputPayload, Format, error) {
	preset := strings.TrimSpace(req.Preset)
	if preset != "" {
		f, ok := lookupPreset(preset)
		if !ok {
			return outputPayload{}, Format{}, ErrFormatUnavailable
		}
		return outputPayload{Type: f.Type, Format: f.Format, Quality: f.Quality}, f, nil
	}

	typ := strings.ToLower(strings.TrimSpace(req.Type))
	format := strings.ToLower(strings.TrimSpace(req.Format))

	if typ == "" {
		if format != "" {
			typ = inferType(format)
		} else {
			typ = "audio"
		}
	}
	if typ != "audio" && typ != "video" {
		return outputPayload{}, Format{}, ErrFormatUnavailable
	}

	if format == "" {
		if typ == "audio" {
			format = "mp3"
		} else {
			format = "mp4"
		}
	}

	allowed := audioFormatsSet
	if typ == "video" {
		allowed = videoFormatsSet
	}
	if !allowed[format] {
		return outputPayload{}, Format{}, ErrFormatUnavailable
	}

	quality := strings.TrimSpace(req.Quality)
	if quality == "" {
		quality = defaultQuality(typ)
	}
	if typ == "video" && !videoQualities[quality] {
		return outputPayload{}, Format{}, ErrFormatUnavailable
	}

	return outputPayload{Type: typ, Format: format, Quality: quality},
		describe(typ, format, quality), nil
}

// inferType guesses the media type from a container format name.
func inferType(format string) string {
	if videoFormatsSet[format] {
		return "video"
	}
	if audioFormatsSet[format] {
		return "audio"
	}
	return ""
}

func defaultQuality(typ string) string {
	if typ == "video" {
		return "720p"
	}
	return "320kbps"
}

// describe builds a public Format descriptor for an arbitrary (already
// validated) type/format/quality triple, reusing the catalog id when present.
func describe(typ, format, quality string) Format {
	for _, f := range audioFormats {
		if f.Type == typ && f.Format == format && f.Quality == quality {
			return f
		}
	}
	for _, f := range videoFormats {
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
