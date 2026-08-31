package errors

import (
	"context"
	stderrors "errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func contextDeadlineExceeded() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	return ctx.Err()
}

func TestNewSetsAllFields(t *testing.T) {
	err := New(http.StatusBadRequest, CodeInvalidURL, CategoryValidation, "invalid url")

	if err.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", err.Status, http.StatusBadRequest)
	}
	if err.Code != CodeInvalidURL {
		t.Errorf("Code = %q, want %q", err.Code, CodeInvalidURL)
	}
	if err.Category != CategoryValidation {
		t.Errorf("Category = %q, want %q", err.Category, CategoryValidation)
	}
	if err.Message != "invalid url" {
		t.Errorf("Message = %q, want %q", err.Message, "invalid url")
	}
	if err.Retryable {
		t.Error("Retryable should default to false")
	}
	if err.Cause() != nil {
		t.Errorf("Cause = %v, want nil", err.Cause())
	}
}

func TestWrapPreservesRootCause(t *testing.T) {
	root := stderrors.New("db connection refused")

	err := Wrap(root, http.StatusInternalServerError, CodeInternalError, CategoryInternal, "internal server error")

	if !stderrors.Is(err, root) {
		t.Fatal("expected errors.Is(err, root) to be true via Unwrap")
	}
	if err.Cause() != root {
		t.Errorf("Cause = %v, want %v", err.Cause(), root)
	}
	if !strings.Contains(err.Error(), root.Error()) {
		t.Errorf("Error() = %q, should contain root cause %q", err.Error(), root.Error())
	}
}

func TestWrapWithSentinelsSupportsErrorsIs(t *testing.T) {
	sentinel := stderrors.New("sentinel")

	err := Validation(CodeInvalidURL, "url is required").WithCause(sentinel)

	if !stderrors.Is(err, sentinel) {
		t.Fatal("expected errors.Is to detect the wrapped sentinel")
	}
}

func TestCategoryConstructorsSetStatus(t *testing.T) {
	cases := []struct {
		name string
		err  *AppError
		want int
	}{
		{"validation", Validation(CodeValidationError, "x"), http.StatusBadRequest},
		{"bad request", BadRequest(CodeInvalidRequest, "x"), http.StatusBadRequest},
		{"authentication", Authentication(CodeAuthenticationRequired, "x"), http.StatusUnauthorized},
		{"authorization", Authorization(CodeForbidden, "x"), http.StatusForbidden},
		{"not found", NotFound(CodeNotFound, "x"), http.StatusNotFound},
		{"conflict", Conflict(CodeConflict, "x"), http.StatusConflict},
		{"rate limit", RateLimit(CodeRateLimited, "x"), http.StatusTooManyRequests},
		{"timeout", Timeout(CodeTimeout, "x"), http.StatusGatewayTimeout},
		{"network", Network(CodeNetworkError, "x"), http.StatusBadGateway},
		{"browser", Browser(CodeBrowserError, "x"), http.StatusInternalServerError},
		{"provider", Provider(CodeProviderError, "x"), http.StatusBadGateway},
		{"downloader", Downloader(CodeDownloaderError, "x"), http.StatusInternalServerError},
		{"internal", Internal(CodeInternalError, "x"), http.StatusInternalServerError},
		{"unsupported", UnsupportedPlatform(CodeUnsupportedPlatform, "x"), http.StatusBadRequest},
		{"unavailable", UnavailableService(CodeProviderUnavailable, "x"), http.StatusServiceUnavailable},
		{"not implemented", NotImplemented(CodeNotImplemented, "x"), http.StatusNotImplemented},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Status != tc.want {
				t.Errorf("Status = %d, want %d", tc.err.Status, tc.want)
			}
			if tc.err.Code == "" {
				t.Error("Code should not be empty")
			}
			if tc.err.Category == "" {
				t.Error("Category should not be empty")
			}
		})
	}
}

func TestRetryableDefaults(t *testing.T) {
	if TimeoutError("x").Retryable != true {
		t.Error("timeout should be retryable by default")
	}
	if NetworkError("x").Retryable != true {
		t.Error("network error should be retryable by default")
	}
	if RateLimited("x").Retryable != true {
		t.Error("rate limit should be retryable by default")
	}
	if ProviderUnavailable("x").Retryable != true {
		t.Error("provider unavailable should be retryable")
	}
	if Validation(CodeInvalidURL, "x").Retryable {
		t.Error("validation error should not be retryable by default")
	}
	if Internal(CodeInternalError, "x").Retryable {
		t.Error("internal error should not be retryable")
	}
}

func TestFromReturnsAppErrorUnchanged(t *testing.T) {
	want := Validation(CodeInvalidURL, "url is required")
	got := From(want)

	if got != want {
		t.Fatal("From should return the same *AppError instance")
	}
}

func TestFromWrappedAppError(t *testing.T) {
	inner := Validation(CodeInvalidURL, "url is required")
	wrapped := stderrors.Join(inner, stderrors.New("extra context"))

	got := From(wrapped)
	if got == nil || got.Code != CodeInvalidURL {
		t.Fatalf("From() = %v, want to recover the wrapped AppError", got)
	}
}

func TestFromUnknownErrorBecomesInternal(t *testing.T) {
	root := stderrors.New("db password leak: hunter2")

	got := From(root)

	if got.Code != CodeInternalError {
		t.Errorf("Code = %q, want %q", got.Code, CodeInternalError)
	}
	if got.Category != CategoryInternal {
		t.Errorf("Category = %q, want %q", got.Category, CategoryInternal)
	}
	if got.Status != http.StatusInternalServerError {
		t.Errorf("Status = %d, want %d", got.Status, http.StatusInternalServerError)
	}
	if !stderrors.Is(got, root) {
		t.Error("root cause should be preserved via Unwrap")
	}
}

func TestFromDeadlineExceededIsTimeout(t *testing.T) {
	got := From(contextDeadlineExceeded())
	if got.Code != CodeTimeout {
		t.Errorf("Code = %q, want %q", got.Code, CodeTimeout)
	}
	if got.Status != http.StatusGatewayTimeout {
		t.Errorf("Status = %d, want %d", got.Status, http.StatusGatewayTimeout)
	}
	if !got.Retryable {
		t.Error("timeout should be retryable")
	}
}

func TestHTTPStatus(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, http.StatusOK},
		{Validation(CodeInvalidURL, "x"), http.StatusBadRequest},
		{Authentication(CodeAuthenticationRequired, "x"), http.StatusUnauthorized},
		{NotFound(CodeNotFound, "x"), http.StatusNotFound},
		{stderrors.New("unknown"), http.StatusInternalServerError},
	}

	for _, tc := range cases {
		if got := HTTPStatus(tc.err); got != tc.want {
			t.Errorf("HTTPStatus(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestAsAppError(t *testing.T) {
	appErr := Validation(CodeInvalidURL, "x")

	got, ok := AsAppError(appErr)
	if !ok || got != appErr {
		t.Fatal("AsAppError should detect a direct AppError")
	}

	_, ok = AsAppError(stderrors.New("plain"))
	if ok {
		t.Fatal("AsAppError should not detect a plain error")
	}
}

func TestErrorStringIncludesCodeAndCause(t *testing.T) {
	err := Validation(CodeInvalidURL, "url is required").WithCause(stderrors.New("boom"))

	s := err.Error()
	for _, want := range []string{string(CodeInvalidURL), "url is required", "boom"} {
		if !strings.Contains(s, want) {
			t.Errorf("Error() = %q, should contain %q", s, want)
		}
	}
}
