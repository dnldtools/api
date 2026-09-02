package http

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"rest-api/internal/downloader"
	apperrors "rest-api/internal/errors"
)

// handleDownloadProxy resolves a media URL server-side and streams the bytes
// back to the client. The client never talks to the upstream CDN directly, so
// upstream cookies/headers (e.g. TikTok's tt_chain_token, referer) are handled
// entirely by this server.
func handleDownloadProxy(svc *downloader.Service, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		rawURL := strings.TrimSpace(q.Get("url"))
		if rawURL == "" {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeMissingParameter, "url is required"))
			return
		}

		typ := strings.ToLower(strings.TrimSpace(q.Get("type")))
		if typ == "" {
			typ = string(downloader.MediaVideo)
		}
		if !validProxyMediaType(typ) {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidRequest, "type must be video, audio, or image"))
			return
		}

		index := 0
		if v := strings.TrimSpace(q.Get("index")); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidRequest, "index must be a non-negative integer"))
				return
			}
			index = n
		}

		result, err := svc.Resolve(r.Context(), downloader.DownloadRequest{URL: rawURL})
		if err != nil {
			errHandler.Handle(w, r, err)
			return
		}
		setPlatform(r.Context(), string(result.Platform))

		format := pickProxyFormat(result, downloader.MediaType(typ), index)
		if format == nil {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeFormatNotAvailable, "requested format is not available"))
			return
		}

		stream, err := svc.StreamMedia(r.Context(), result.Platform, format.URL)
		if err != nil {
			errHandler.Handle(w, r, err)
			return
		}
		defer stream.Body.Close()

		filename := proxyFilename(result, format)
		w.Header().Set("Content-Type", stream.ContentType)
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if stream.Length > 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(stream.Length, 10))
		}
		w.WriteHeader(http.StatusOK)

		if _, err := io.Copy(w, stream.Body); err != nil {
			if errHandler != nil && errHandler.logger != nil {
				errHandler.logger.Warn("proxy stream interrupted", "error", err, "url", rawURL)
			}
		}
	}
}

func validProxyMediaType(typ string) bool {
	switch downloader.MediaType(typ) {
	case downloader.MediaVideo, downloader.MediaAudio, downloader.MediaImage:
		return true
	default:
		return false
	}
}

func pickProxyFormat(result *downloader.DownloadResult, typ downloader.MediaType, index int) *downloader.Format {
	seen := 0
	for i := range result.Formats {
		if result.Formats[i].Type != typ {
			continue
		}
		if seen == index {
			return &result.Formats[i]
		}
		seen++
	}
	return nil
}

func proxyFilename(result *downloader.DownloadResult, format *downloader.Format) string {
	ext := strings.TrimPrefix(format.Ext, ".")
	if ext == "" {
		switch format.Type {
		case downloader.MediaVideo:
			ext = "mp4"
		case downloader.MediaAudio:
			ext = "mp3"
		case downloader.MediaImage:
			ext = "jpg"
		default:
			ext = "bin"
		}
	}

	base := "media"
	if u, err := url.Parse(result.URL); err == nil {
		path := strings.Trim(u.Path, "/")
		if i := strings.LastIndexByte(path, '/'); i >= 0 {
			path = path[i+1:]
		}
		path = sanitizeFilename(path)
		if path != "" {
			base = path
		}
	}
	return fmt.Sprintf("%s.%s", base, ext)
}

func sanitizeFilename(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// externalBaseURL returns the public scheme://host the client used to reach
// this API, honoring common reverse-proxy headers (X-Forwarded-Proto/Host).
func externalBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if v := firstForwarded(r.Header.Get("X-Forwarded-Proto")); v != "" {
		scheme = v
	}
	host := r.Host
	if v := firstForwarded(r.Header.Get("X-Forwarded-Host")); v != "" {
		host = v
	}
	return scheme + "://" + host
}

func firstForwarded(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// proxyFormatURL builds the streaming-proxy URL for one resolved format. The
// index is the zero-based position of the format among formats of the same
// type, matching the proxy endpoint's pickProxyFormat.
func proxyFormatURL(baseURL, sourceURL string, typ downloader.MediaType, index int) string {
	q := url.Values{}
	q.Set("url", sourceURL)
	q.Set("type", string(typ))
	q.Set("index", strconv.Itoa(index))
	return strings.TrimRight(baseURL, "/") + "/v1/downloads/proxy?" + q.Encode()
}

// rewriteToProxyURLs replaces direct upstream CDN URLs with streaming-proxy
// URLs so end users never contact the upstream directly (and never need to
// send upstream cookies/referer headers themselves).
func rewriteToProxyURLs(result *downloader.DownloadResult, baseURL string) {
	seen := map[downloader.MediaType]int{}
	for i := range result.Formats {
		f := &result.Formats[i]
		idx := seen[f.Type]
		seen[f.Type]++
		f.URL = proxyFormatURL(baseURL, result.URL, f.Type, idx)
	}
}
