package http

import (
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
	writeJSON(w, r, status, Response{
		Success:   true,
		Message:   http.StatusText(status),
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
	var (
		data []byte
		err  error
	)

	if prettyFromContext(r) {
		data, err = json.MarshalIndent(payload, "", "  ")
	} else {
		data, err = json.Marshal(payload)
	}

	if err != nil {

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"success":false,"message":"failed to encode response","error":{"code":"INTERNAL_ERROR","category":"INTERNAL","details":null,"retryable":false,"request_id":"","timestamp":""}}` + "\n"))
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n"))
}

func decodeJSON(r *http.Request, dst interface{}) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
