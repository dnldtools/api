package errors

import (
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"
)

func TestPayloadJSONSerialization(t *testing.T) {
	err := Validation(CodeInvalidURL, "invalid url").
		WithDetails(map[string]string{"field": "url"}).
		WithRequestID("req-123").
		WithCause(stderrors.New("secret internal detail"))

	p := err.Payload()
	data, marshalErr := json.Marshal(p)
	if marshalErr != nil {
		t.Fatalf("Marshal() error = %v", marshalErr)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if m["code"] != "INVALID_URL" {
		t.Errorf("code = %v, want INVALID_URL", m["code"])
	}
	if m["category"] != "VALIDATION" {
		t.Errorf("category = %v, want VALIDATION", m["category"])
	}
	if m["retryable"] != false {
		t.Errorf("retryable = %v, want false", m["retryable"])
	}
	if m["request_id"] != "req-123" {
		t.Errorf("request_id = %v, want req-123", m["request_id"])
	}
	if m["timestamp"] == "" || m["timestamp"] == nil {
		t.Error("timestamp should be present and non-empty")
	}

	details, ok := m["details"].(map[string]interface{})
	if !ok {
		t.Fatalf("details = %v, want an object", m["details"])
	}
	if details["field"] != "url" {
		t.Errorf("details.field = %v, want url", details["field"])
	}
}

func TestPayloadDoesNotLeakInternalCause(t *testing.T) {
	secret := "api-key=super-secret-value"
	err := Internal(CodeInternalError, "internal server error").WithCause(stderrors.New(secret))

	data, marshalErr := json.Marshal(err.Payload())
	if marshalErr != nil {
		t.Fatalf("Marshal() error = %v", marshalErr)
	}

	if strings.Contains(string(data), secret) {
		t.Fatalf("serialized payload leaked internal cause: %s", data)
	}
	if strings.Contains(string(data), "Cause") {
		t.Fatalf("serialized payload leaked internal field names: %s", data)
	}
}

func TestPayloadNilDetailsMarshalsToNull(t *testing.T) {
	err := Validation(CodeInvalidURL, "invalid url")

	data, marshalErr := json.Marshal(err.Payload())
	if marshalErr != nil {
		t.Fatalf("Marshal() error = %v", marshalErr)
	}

	if !strings.Contains(string(data), `"details":null`) {
		t.Fatalf("expected details to serialize as null, got: %s", data)
	}
}
