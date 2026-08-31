package http

import (
	"encoding/json"
	stderrors "errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "rest-api/internal/errors"
)

func newTestErrorHandler() *ErrorHandler {
	return NewErrorHandler(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestErrorHandlerWritesRequestIDFromContext(t *testing.T) {
	h := newTestErrorHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/downloads", nil)
	req = req.WithContext(withRequestID(req.Context(), "ctx-request-123"))

	h.Handle(rec, req, stderrors.New("boom"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	errObj := body["error"].(map[string]interface{})
	if id, _ := errObj["request_id"].(string); id != "ctx-request-123" {
		t.Errorf("error.request_id = %q, want ctx-request-123", id)
	}
}

func TestErrorHandlerDoesNotLeakInternalCause(t *testing.T) {
	h := newTestErrorHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/downloads", nil)

	secret := "proxy-password=super-secret"
	err := apperrors.Provider(apperrors.CodeProviderError, "provider failed").
		WithCause(stderrors.New(secret))

	h.Handle(rec, req, err)

	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("response leaked internal cause: %s", rec.Body.String())
	}
}

func TestErrorHandlerUnknownErrorBecomesInternal(t *testing.T) {
	h := newTestErrorHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/downloads", nil)

	h.Handle(rec, req, stderrors.New("mystery"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	errObj := body["error"].(map[string]interface{})
	if code, _ := errObj["code"].(string); code != "INTERNAL_ERROR" {
		t.Errorf("error.code = %q, want INTERNAL_ERROR", code)
	}
	if msg, _ := body["message"].(string); msg != "internal server error" {
		t.Errorf("message = %q, want generic internal message", msg)
	}
}

func TestErrorHandlerValidationErrorStatus(t *testing.T) {
	h := newTestErrorHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/downloads", nil)

	h.Handle(rec, req, apperrors.Validation(apperrors.CodeInvalidURL, "url is required"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	errObj := body["error"].(map[string]interface{})
	if retryable, _ := errObj["retryable"].(bool); retryable {
		t.Error("validation error should not be retryable")
	}
}

func TestErrorHandlerProviderUnavailableRetryable(t *testing.T) {
	h := newTestErrorHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/downloads", nil)

	h.Handle(rec, req, apperrors.ProviderUnavailable("service temporarily unavailable"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	errObj := body["error"].(map[string]interface{})
	if retryable, _ := errObj["retryable"].(bool); !retryable {
		t.Error("provider unavailable should be retryable")
	}
	if code, _ := errObj["code"].(string); code != "PROVIDER_UNAVAILABLE" {
		t.Errorf("error.code = %q, want PROVIDER_UNAVAILABLE", code)
	}
}

func TestErrorHandlerTimeoutError(t *testing.T) {
	h := newTestErrorHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/downloads", nil)

	h.Handle(rec, req, apperrors.TimeoutError("request timed out"))

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rec.Code)
	}
}
