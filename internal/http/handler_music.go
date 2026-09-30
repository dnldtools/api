package http

import (
	"net/http"
	"time"

	apperrors "rest-api/internal/errors"
	"rest-api/internal/music"
)

type musicRequest struct {
	URL     string `json:"url"`
	Quality string `json:"quality"`
}

func handleMusicResolve(svc *music.Service, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req musicRequest
		if err := decodeJSON(r, &req); err != nil {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidJSON, "request body is not valid JSON").WithCause(err))
			return
		}
		if req.URL == "" {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeMissingParameter, "url is required").WithCause(nil))
			return
		}

		result, err := svc.Resolve(r.Context(), req.URL)
		if err != nil {
			errHandler.Handle(w, r, err)
			return
		}
		WriteSuccess(w, r, http.StatusOK, result)
	}
}

func handleMusicDownload(svc *music.Service, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req musicRequest
		if err := decodeJSON(r, &req); err != nil {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidJSON, "request body is not valid JSON").WithCause(err))
			return
		}
		if req.URL == "" {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeMissingParameter, "url is required").WithCause(nil))
			return
		}

		if rc := http.NewResponseController(w); rc != nil {
			_ = rc.SetWriteDeadline(time.Now().Add(svc.Timeout() + time.Minute))
		}

		result, err := svc.Download(r.Context(), req.URL, req.Quality)
		if err != nil {
			errHandler.Handle(w, r, err)
			return
		}
		WriteSuccess(w, r, http.StatusOK, result)
	}
}
