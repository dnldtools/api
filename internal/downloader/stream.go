package downloader

import (
	"context"
	"io"
)

// MediaStream is a streamed response for a resolved media URL. The Body must
// be closed by the caller once it is no longer needed.
type MediaStream struct {
	Body        io.ReadCloser
	ContentType string
	Length      int64
}

// MediaStreamer is implemented by providers that can fetch a resolved media
// URL server-side (with any session cookies/headers they hold) and stream the
// bytes back. Providers MUST validate the media URL (e.g. against an allowlist
// of their own CDN hosts) before fetching it to avoid SSRF.
type MediaStreamer interface {
	StreamMedia(ctx context.Context, mediaURL string) (*MediaStream, error)
}
