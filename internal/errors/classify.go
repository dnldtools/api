package errors

import (
	"context"
	stderrors "errors"
	"net/http"
)

func From(err error) *AppError {
	if err == nil {
		return nil
	}

	var appErr *AppError
	if stderrors.As(err, &appErr) {
		return appErr
	}

	switch {
	case stderrors.Is(err, context.DeadlineExceeded):
		return TimeoutError("request timed out").WithCause(err)
	case stderrors.Is(err, context.Canceled):

		return Internal(CodeInternalError, "request canceled").WithCause(err)
	default:
		return Internal(CodeInternalError, "internal server error").WithCause(err)
	}
}

func HTTPStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if appErr := From(err); appErr != nil {
		return appErr.Status
	}
	return http.StatusInternalServerError
}

func AsAppError(err error) (*AppError, bool) {
	var appErr *AppError
	if stderrors.As(err, &appErr) {
		return appErr, true
	}
	return nil, false
}
