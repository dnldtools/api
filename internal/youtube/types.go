package youtube

type Format struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Format  string `json:"format"`
	Quality string `json:"quality,omitempty"`
	Label   string `json:"label"`
	Bitrate string `json:"bitrate,omitempty"`
	Premium bool   `json:"premium,omitempty"`
}

type Catalog struct {
	Audio []Format `json:"audio"`
	Video []Format `json:"video"`

	QualityFallback        bool   `json:"quality_fallback"`
	QualityFallbackMessage string `json:"quality_fallback_message,omitempty"`
}

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

type SearchResult struct {
	Items         []SearchItem `json:"items"`
	NextPageToken string       `json:"next_page_token,omitempty"`
}

type ConvertRequest struct {
	URL     string `json:"url"`
	Preset  string `json:"preset,omitempty"`
	Type    string `json:"type,omitempty"`
	Format  string `json:"format,omitempty"`
	Quality string `json:"quality,omitempty"`
	Bitrate string `json:"bitrate,omitempty"`
	Track   string `json:"track,omitempty"`
}

type FormatSpec struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Format  string `json:"format"`
	Quality string `json:"quality"`
}

type OutputSpec struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Format      string `json:"format"`
	Quality     string `json:"quality"`
	BitrateKbps int    `json:"bitrate_kbps"`
}

type ConvertResult struct {
	URL            string     `json:"url"`
	Title          string     `json:"title,omitempty"`
	Duration       int64      `json:"duration,omitempty"`
	Requested      FormatSpec `json:"requested"`
	Output         OutputSpec `json:"output"`
	QualityChanged bool       `json:"quality_changed"`
	QualityNote    string     `json:"quality_note,omitempty"`
	DownloadURL    string     `json:"download_url"`
	Cover          string     `json:"cover"`
}

type outputPayload struct {
	Type    string `json:"type"`
	Format  string `json:"format"`
	Quality string `json:"quality,omitempty"`
}

type audioPayload struct {
	Bitrate string `json:"bitrate,omitempty"`
	TrackID string `json:"trackId,omitempty"`
}
