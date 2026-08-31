package youtube

import "strings"

// audioFormats and videoFormats define the catalog exposed by the /formats
// endpoint and accepted by resolveFormat. The ids mirror the ytmp3.gg /
// convert1s.com UI options exactly; there is no implicit default.
//
//	audio presets:  mp3-320 mp3-192 mp3-128 mp3-64 wav m4a ogg opus flac aac alac
//	video presets:  mp4-2160 mp4-1440 mp4-1080-premium mp4-1080 mp4-720 mp4-480 mp4-360 mp4-144
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

// nonMP3Audio lists the lossless/alternate audio containers that are encoded
// without an audio.bitrate value in the v3 worker payload.
var nonMP3Audio = map[string]bool{
	"wav": true, "m4a": true, "ogg": true, "opus": true,
	"flac": true, "aac": true, "alac": true,
}

// Formats returns a defensive copy of the format catalog.
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

// lookupPreset returns the format for a catalog preset id.
func lookupPreset(preset string) (Format, bool) {
	for _, f := range allFormats() {
		if f.ID == preset {
			return f, true
		}
	}
	return Format{}, false
}

// formatSelection is the result of resolveFormat: the public Format descriptor
// plus the concrete v3 worker payload fragments derived from it.
type formatSelection struct {
	Format  Format
	Output  outputPayload
	Audio   *audioPayload // non-nil only for MP3 (carries the bitrate)
	Premium bool
}

// selectionFrom converts a catalog Format into a formatSelection, translating
// request-side metadata (mp3 bitrate, premium flag) into the v3 payload shape.
func selectionFrom(f Format) formatSelection {
	sel := formatSelection{Format: f, Premium: f.Premium}

	output := outputPayload{Type: f.Type, Format: f.Format}
	if f.Type == "audio" && f.Format == "mp3" && f.Bitrate != "" {
		// MP3 bitrate is sent via audio.bitrate; output.quality is omitted
		// because workers ignore it.
		sel.Audio = &audioPayload{Bitrate: f.Bitrate}
	} else {
		output.Quality = f.Quality
	}
	sel.Output = output
	return sel
}

// resolveFormat maps a ConvertRequest onto a concrete catalog format and the
// matching v3 worker payload. Missing or unknown selectors return
// ErrFormatUnavailable; there is no silent fallback to 320kbps.
//
// Resolution order mirrors the reference scraper:
//  1. `preset` (or `format` when no preset) matching a catalog id exactly.
//  2. Otherwise `type`/`format`/`quality`/`bitrate` are used.
func resolveFormat(req ConvertRequest) (formatSelection, error) {
	preset := strings.ToLower(strings.TrimSpace(req.Preset))
	format := strings.ToLower(strings.TrimSpace(req.Format))
	bitrate := strings.TrimSpace(req.Bitrate)
	quality := strings.TrimSpace(req.Quality)
	typ := strings.ToLower(strings.TrimSpace(req.Type))

	// rawPreset mirrors the reference: the explicit preset id, or the container
	// `format` value when no preset was given (used for the id lookup and for
	// the mp4 type inference).
	rawPreset := preset
	if rawPreset == "" {
		rawPreset = format
	}

	// No selector at all → error (never default silently).
	if rawPreset == "" && bitrate == "" && quality == "" && typ == "" {
		return formatSelection{}, ErrFormatUnavailable
	}

	// Exact catalog id match takes priority (mp3-320, wav, mp4-1080-premium...).
	if rawPreset != "" {
		if f, ok := lookupPreset(rawPreset); ok {
			return selectionFrom(f), nil
		}
	}

	mediaType := "audio"
	if typ == "video" || rawPreset == "mp4" {
		mediaType = "video"
	}

	// Container name, defaulting by media type, minus any "-suffix"
	// (e.g. "mp4-999" → "mp4", "mp3-256" → "mp3").
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
		// Alternate audio containers are selected by bare id (wav, m4a, ...).
		if nonMP3Audio[container] {
			if f, ok := lookupPreset(container); ok {
				return selectionFrom(f), nil
			}
			return formatSelection{}, ErrFormatUnavailable
		}
		// MP3 requires an explicit bitrate (64|128|192|320).
		if bitrate == "" {
			return formatSelection{}, ErrFormatUnavailable
		}
		if f, ok := lookupPreset("mp3-" + normalizeBitrate(bitrate)); ok {
			return selectionFrom(f), nil
		}
		return formatSelection{}, ErrFormatUnavailable
	}

	// Video requires an explicit quality (144..2160).
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

// normalizeBitrate strips an optional kbps/k suffix from a bitrate selector
// ("320kbps" / "320k" / "320" → "320").
func normalizeBitrate(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, "kbps")
	s = strings.TrimSuffix(s, "k")
	return strings.TrimSpace(s)
}

// normalizeTrack maps the request `track` field onto a v3 audio.trackId value.
// "origin" (and empty) mean "keep the default track", so no trackId is sent.
func normalizeTrack(track string) string {
	track = strings.ToLower(strings.TrimSpace(track))
	if track == "" || track == "origin" {
		return ""
	}
	return track
}

// describe builds a public Format descriptor for an arbitrary (already
// resolved) type/format/quality triple, preferring the non-premium catalog
// entry when two entries share the same quality (mp4-1080 vs mp4-1080-premium).
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
