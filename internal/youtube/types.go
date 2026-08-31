package youtube

// Format describes one output option in the conversion catalog. The Bitrate
// and Premium fields carry request-side metadata used to build the v3 worker
// payload; they are only set for the formats that need them.
type Format struct {
	ID      string `json:"id"`                // catalog preset id, e.g. "mp3-320"
	Type    string `json:"type"`              // "audio" or "video"
	Format  string `json:"format"`            // container/extension, e.g. "mp3", "mp4"
	Quality string `json:"quality,omitempty"` // "320kbps", "720p", ...
	Label   string `json:"label"`             // human-readable label
	Bitrate string `json:"bitrate,omitempty"` // audio MP3 bitrate, e.g. "320k"
	Premium bool   `json:"premium,omitempty"` // true for premium video formats
}

// Catalog is the full list of supported conversion formats, grouped by type.
type Catalog struct {
	Audio []Format `json:"audio"`
	Video []Format `json:"video"`

	// QualityFallback indicates the upstream silently falls back to the best
	// available quality when the requested quality is not available.
	QualityFallback        bool   `json:"quality_fallback"`
	QualityFallbackMessage string `json:"quality_fallback_message,omitempty"`
}

// SearchItem is a single YouTube result returned by the search endpoint.
type SearchItem struct {
	Type         string `json:"type"`
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	UploaderName string `json:"uploader_name,omitempty"`
	UploaderURL  string `json:"uploader_url,omitempty"`
	Duration     int64  `json:"duration,omitempty"`
	ViewCount    int64  `json:"view_count,omitempty"`
	UploadDate   string `json:"upload_date,omitempty"`
}

// SearchResult is the response payload of a search query.
type SearchResult struct {
	Items         []SearchItem `json:"items"`
	NextPageToken string       `json:"next_page_token,omitempty"`
}

// ConvertRequest is the JSON body accepted by POST /v1/youtube/convert. At
// least `url` is required. The output format can be selected either via a
// catalog `preset` id or via the `type`/`format`/`quality`/`bitrate` fields.
// `Track` selects an alternate audio track (the worker default is "origin").
type ConvertRequest struct {
	URL     string `json:"url"`
	Preset  string `json:"preset,omitempty"`
	Type    string `json:"type,omitempty"`
	Format  string `json:"format,omitempty"`
	Quality string `json:"quality,omitempty"`
	Bitrate string `json:"bitrate,omitempty"`
	Track   string `json:"track,omitempty"`
}

// FormatSpec describes a requested or actual output format. It carries only
// the identity fields, so it can describe both catalog presets and probe-based
// actual results without leaking catalog-only metadata (label/default).
type FormatSpec struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Format  string `json:"format"`
	Quality string `json:"quality"`
}

// OutputSpec describes the actually produced file. Its quality is derived from
// probing the download (or the worker's reported selection), never from the
// requested preset alone.
type OutputSpec struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Format      string `json:"format"`
	Quality     string `json:"quality"`
	BitrateKbps int    `json:"bitrate_kbps"`
}

// ConvertResult is the response payload of a successful conversion.
type ConvertResult struct {
	URL            string     `json:"url"`
	Title          string     `json:"title,omitempty"`
	Duration       int64      `json:"duration,omitempty"` // seconds
	Requested      FormatSpec `json:"requested"`
	Output         OutputSpec `json:"output"`
	QualityChanged bool       `json:"quality_changed"`
	QualityNote    string     `json:"quality_note,omitempty"`
	DownloadURL    string     `json:"download_url"`
	Cover          string     `json:"cover"`
}

// outputPayload is the `output` object sent to the upstream worker API. It is
// not exposed to callers directly; Format is the public representation.
type outputPayload struct {
	Type    string `json:"type"`
	Format  string `json:"format"`
	Quality string `json:"quality,omitempty"`
}

// audioPayload carries the v3 audio options for a conversion job. Bitrate is
// only sent for MP3 output; TrackID is only sent for a non-origin track.
type audioPayload struct {
	Bitrate string `json:"bitrate,omitempty"`
	TrackID string `json:"trackId,omitempty"`
}
