package http

import (
	_ "embed"
	"net/http"
)

// faviconPNG is the site favicon embedded into the binary so the API can
// serve it without depending on an external file on disk.
//
//go:embed assets/favicon.png
var faviconPNG []byte

// handleFavicon serves the embedded favicon for both /favicon.ico and
// /favicon.png. Modern browsers accept an image/png response for the .ico
// path, which keeps the favicon to a single embedded asset.
func handleFavicon() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(faviconPNG)
	}
}
