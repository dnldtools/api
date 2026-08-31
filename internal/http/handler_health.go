package http

import (
	"net/http"

	"rest-api/internal/health"
)

func handleHealth(checker *health.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteSuccess(w, r, http.StatusOK, checker.Check(r.Context()))
	}
}
