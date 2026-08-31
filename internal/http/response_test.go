package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apperrors "rest-api/internal/errors"
)

func TestWriteSuccessProducesConsistentEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req = req.WithContext(withRequestID(req.Context(), "req-123"))

	WriteSuccess(rec, req, http.StatusOK, map[string]string{"status": "ok"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if success, _ := body["success"].(bool); !success {
		t.Errorf("success = %v, want true", body["success"])
	}
	if _, exists := body["data"]; !exists {
		t.Error("data should be present on success")
	}
	if _, exists := body["error"]; exists {
		t.Error("error should be omitted on success")
	}
	if id, _ := body["request_id"].(string); id != "req-123" {
		t.Errorf("request_id = %q, want req-123", id)
	}
	if ts, _ := body["timestamp"].(string); ts == "" {
		t.Error("timestamp should be non-empty on success")
	}
}

func TestWriteErrorProducesConsistentEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	req = req.WithContext(withRequestID(req.Context(), "req-456"))

	err := apperrors.NotFound(apperrors.CodeNotFound, "resource not found").
		WithRequestID("req-456").
		WithTimestamp(nowUTC())
	writeError(rec, req, err)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if success, _ := body["success"].(bool); success {
		t.Errorf("success = %v, want false", body["success"])
	}

	errObj, ok := body["error"].(map[string]interface{})
	if !ok {
		t.Fatal("error is missing or not an object")
	}
	if code, _ := errObj["code"].(string); code != "NOT_FOUND" {
		t.Errorf("error.code = %q, want NOT_FOUND", code)
	}
	if category, _ := errObj["category"].(string); category != "NOT_FOUND" {
		t.Errorf("error.category = %q, want NOT_FOUND", category)
	}
	if id, _ := errObj["request_id"].(string); id != "req-456" {
		t.Errorf("error.request_id = %q, want req-456", id)
	}
	if _, exists := errObj["details"]; !exists {
		t.Error("error.details should be present (null) on error")
	}
}
