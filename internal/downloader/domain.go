package downloader

import "strings"

type Platform string

const (
	PlatformFacebook   Platform = "facebook"
	PlatformInstagram  Platform = "instagram"
	PlatformTikTok     Platform = "tiktok"
	PlatformShopee     Platform = "shopee"
	PlatformApple      Platform = "apple"
	PlatformUCShare    Platform = "uc-share"
	PlatformDoodstream Platform = "doodstream"
	PlatformPinterest  Platform = "pinterest"
	PlatformYouTube    Platform = "youtube"
	Platform9xbuddy    Platform = "9xbuddy"
	PlatformSavefrom   Platform = "savefrom"
)

type MediaType string

const (
	MediaVideo MediaType = "video"
	MediaAudio MediaType = "audio"
	MediaImage MediaType = "image"
)

type Format struct {
	Type    MediaType `json:"type"`
	URL     string    `json:"url"`
	Quality string    `json:"quality,omitempty"`
	Ext     string    `json:"ext,omitempty"`
	Size    int64     `json:"size,omitempty"`
}

type DownloadRequest struct {
	URL      string   `json:"url"`
	Platform Platform `json:"platform,omitempty"`
}

func (r DownloadRequest) Validate() error {
	if strings.TrimSpace(r.URL) == "" {
		return ErrInvalidURL
	}
	return nil
}

type DownloadResult struct {
	Platform   Platform          `json:"platform"`
	URL        string            `json:"url"`
	Title      string            `json:"title,omitempty"`
	Type       MediaType         `json:"type,omitempty"`
	Thumbnail  string            `json:"thumbnail,omitempty"`
	DurationMs int64             `json:"duration_ms,omitempty"`
	Formats    []Format          `json:"formats,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}
