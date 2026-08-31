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

		WriteSuccess(w, r, http.StatusOK, result)
	}
}
