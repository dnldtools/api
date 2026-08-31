package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	apperrors "rest-api/internal/errors"
)

type Response struct {
	Success   bool               `json:"success"`
	Message   string             `json:"message,omitempty"`
	Data      interface{}        `json:"data,omitempty"`
	Error     *apperrors.Payload `json:"error,omitempty"`
	RequestID string             `json:"request_id,omitempty"`
	Timestamp string             `json:"timestamp,omitempty"`
}

func WriteSuccess(w http.ResponseWriter, r *http.Request, status int, data interface{}) {
	WriteSuccessWithMessage(w, r, status, http.StatusText(status), data)
}

func WriteSuccessWithMessage(w http.ResponseWriter, r *http.Request, status int, message string, data interface{}) {
	if message == "" {
		message = http.StatusText(status)
	}
	writeJSON(w, r, status, Response{
		Success:   true,
		Message:   message,
		Data:      data,
		RequestID: requestIDFromContext(r.Context()),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func writeError(w http.ResponseWriter, r *http.Request, appErr *apperrors.AppError) {
	writeJSON(w, r, appErr.Status, Response{
		Success: false,
		Message: appErr.Message,
		Error:   appErr.Payload(),
	})
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, payload interface{}) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Disable HTML escaping so URLs with query strings keep literal `&`
	// characters instead of being re-encoded as \u0026.
	enc.SetEscapeHTML(false)
	if prettyFromContext(r) {
		enc.SetIndent("", "  ")
	}

	if err := enc.Encode(payload); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"success":false,"message":"failed to encode response","error":{"code":"INTERNAL_ERROR","category":"INTERNAL","details":null,"retryable":false,"request_id":"","timestamp":""}}` + "\n"))
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func decodeJSON(r *http.Request, dst interface{}) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
