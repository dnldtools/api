package http

import (
	"net/http"

	"rest-api/internal/downloader"
	apperrors "rest-api/internal/errors"
)

type downloadRequest struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
}

func handleDownload(svc *downloader.Service, errHandler *ErrorHandler) http.HandlerFunc {
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

		// TikTok formats resolve to direct CDN URLs. Rewrite them to our own
		// streaming-proxy endpoint so clients never have to send upstream
		// cookies/referer headers (and never hit 403 on the CDN directly).
		if result.Platform == downloader.PlatformTikTok {
			rewriteToProxyURLs(result, externalBaseURL(r))
		}

		WriteSuccess(w, r, http.StatusOK, result)
	}
}
