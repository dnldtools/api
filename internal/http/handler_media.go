package http

import (
	"errors"
	"net/http"
	"strconv"

	apperrors "rest-api/internal/errors"
	"rest-api/internal/media"
	"rest-api/internal/r2"
)

type mediaResponse struct {
	ID          int64  `json:"id"`
	URL         string `json:"url"`
	ExpiresIn   int64  `json:"expires_in"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Status      string `json:"status"`
}

func handleMediaGet(store *media.Store, mgr *r2.Manager, errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			errHandler.Handle(w, r, apperrors.Validation(apperrors.CodeInvalidRequest, "invalid id"))
			return
		}

		rec, err := store.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, media.ErrNotFound) {
				errHandler.Handle(w, r, apperrors.NotFound(apperrors.CodeResourceNotFound, "media not found"))
				return
			}
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to get media").WithCause(err))
			return
		}

		if rec.Status != media.StatusReady {
			errHandler.Handle(w, r, apperrors.Conflict(apperrors.CodeConflict, "media is not ready"))
			return
		}

		signed, err := mgr.PresignOn(r.Context(), rec.Account, rec.ObjectKey)
		if err != nil {
			errHandler.Handle(w, r, apperrors.Internal(apperrors.CodeInternalError, "failed to presign media").WithCause(err))
			return
		}

		WriteSuccess(w, r, http.StatusOK, mediaResponse{
			ID:          rec.ID,
			URL:         signed.URL,
			ExpiresIn:   signed.TTLSeconds,
			ContentType: rec.ContentType,
			Size:        rec.Size,
			Status:      string(rec.Status),
		})
	}
}
