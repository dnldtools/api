package http

import (
	"log/slog"
	"net/http"
	"time"

	apperrors "rest-api/internal/errors"
)

type ErrorHandler struct {
	logger *slog.Logger
}

func NewErrorHandler(logger *slog.Logger) *ErrorHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ErrorHandler{logger: logger}
}

func (h *ErrorHandler) Handle(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		err = apperrors.Internal(apperrors.CodeInternalError, "Something went wrong on our end. Please try again.")
	}

	appErr := mapError(err)
	appErr = appErr.
		WithRequestID(requestIDFromContext(r.Context())).
		WithTimestamp(time.Now().UTC())

	h.log(r, appErr)

	writeError(w, r, appErr)
}

func (h *ErrorHandler) log(r *http.Request, appErr *apperrors.AppError) {
	attrs := []any{
		"request_id", appErr.RequestID,
		"code", appErr.Code,
		"category", appErr.Category,
		"http_status", appErr.Status,
		"endpoint", r.URL.Path,
		"method", r.Method,
		"error", appErr.Error(),
	}

	if cause := appErr.Cause(); cause != nil {
		attrs = append(attrs, "cause", cause.Error())
	}

	if start, ok := startTimeFromContext(r.Context()); ok {
		attrs = append(attrs, "duration", time.Since(start).String())
	}

	h.logger.Error("request failed", attrs...)
}
