package http

import (
	"context"
	"net/http"
	"strings"

	"rest-api/internal/downloader"
	apperrors "rest-api/internal/errors"
	"rest-api/internal/storage"
)

type downloadRequest struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
}

func handleDownload(svc *downloader.Service, uploader *storage.Uploader, errHandler *ErrorHandler, publicBaseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req downloadRequest
		if err := decodeJSON(r, &req); err != nil {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidJSON, "request body is not valid JSON").WithCause(err))
			return
		}

		setPlatform(r.Context(), req.Platform)

		result, err := svc.Resolve(r.Context(), downloader.DownloadRequest{
			Platform: downloader.Platform(req.Platform),
			URL:      req.URL,
		})
		if err != nil {
			errHandler.Handle(w, r, err)
			return
		}

		if uploader != nil && uploader.ShouldMirror(result.Platform) {
			go uploader.Mirror(context.Background(), svc, cloneDownloadResult(result))
		} else if result.Platform == downloader.PlatformTikTok {
			base := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
			if base == "" {
				base = externalBaseURL(r)
			}
			rewriteToProxyURLs(result, base)
		}

		WriteSuccess(w, r, http.StatusOK, result)
	}
}

func cloneDownloadResult(src *downloader.DownloadResult) *downloader.DownloadResult {
	if src == nil {
		return nil
	}
	clone := *src
	if src.Formats != nil {
		clone.Formats = make([]downloader.Format, len(src.Formats))
		copy(clone.Formats, src.Formats)
	}
	if src.Metadata != nil {
		clone.Metadata = make(map[string]string, len(src.Metadata))
		for k, v := range src.Metadata {
			clone.Metadata[k] = v
		}
	}
	return &clone
}
