package http

import (
	"net/http"
	"strings"

	apperrors "rest-api/internal/errors"
	"rest-api/internal/youtube"
)

func handleYouTubeSearch(svc *youtube.Service, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "youtube is not configured"))
			return
		}

		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeMissingParameter, "query parameter q is required"))
			return
		}

		pageToken := strings.TrimSpace(r.URL.Query().Get("page_token"))

		result, err := svc.Search(r.Context(), q, pageToken)
		if err != nil {
			errHandler.Handle(w, r, err)
			return
		}

		WriteSuccess(w, r, http.StatusOK, result)
	}
}

func handleYouTubeFormats(svc *youtube.Service, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "youtube is not configured"))
			return
		}

		WriteSuccess(w, r, http.StatusOK, svc.Formats())
	}
}

func handleYouTubeConvert(svc *youtube.Service, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "youtube is not configured"))
			return
		}

		var req youtube.ConvertRequest
		if err := decodeJSON(r, &req); err != nil {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidJSON, "request body is not valid JSON").WithCause(err))
			return
		}

		setPlatform(r.Context(), "youtube")

		result, err := svc.Convert(r.Context(), req)
		if err != nil {
			errHandler.Handle(w, r, err)
			return
		}

		message := http.StatusText(http.StatusOK) // "OK"
		if result.QualityChanged {
			message = result.QualityNote
			if message == "" {
				message = "Converted, quality adjusted"
			}
		}

		WriteSuccessWithMessage(w, r, http.StatusOK, message, result)
	}
}
