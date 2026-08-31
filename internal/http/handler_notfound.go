package http

import (
	"net/http"

	apperrors "rest-api/internal/errors"
)

func handleNotFound(errHandler *ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		errHandler.Handle(w, r, apperrors.NotFound(apperrors.CodeNotFound, "resource not found"))
	}
}
