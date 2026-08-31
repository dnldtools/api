package http

import (
	"context"
	stderrors "errors"
	"net/http"
	"testing"

	"rest-api/internal/browser"
	"rest-api/internal/downloader"
	apperrors "rest-api/internal/errors"
)

func TestMapErrorNil(t *testing.T) {
	if got := mapError(nil); got != nil {
		t.Fatalf("mapError(nil) = %v, want nil", got)
	}
}

func TestMapErrorPassesThroughAppError(t *testing.T) {
	want := apperrors.Validation(apperrors.CodeInvalidURL, "url is required")
	got := mapError(want)

	if got != want {
		t.Fatal("mapError should pass through an already-classified AppError")
	}
}

func TestMapErrorValidationSentinels(t *testing.T) {
	cases := []struct {
		err  error
		code apperrors.Code
	}{
		{downloader.ErrInvalidURL, apperrors.CodeInvalidURL},
	}

	for _, tc := range cases {
		got := mapError(tc.err)
		if got.Code != tc.code {
			t.Errorf("mapError(%v).Code = %q, want %q", tc.err, got.Code, tc.code)
		}
		if got.Category != apperrors.CategoryValidation {
			t.Errorf("mapError(%v).Category = %q, want VALIDATION", tc.err, got.Category)
		}
		if got.Status != http.StatusBadRequest {
			t.Errorf("mapError(%v).Status = %d, want 400", tc.err, got.Status)
		}
		if !stderrors.Is(got, tc.err) {
			t.Errorf("mapError(%v) should preserve root cause", tc.err)
		}
	}
}

func TestMapErrorUnsupportedPlatform(t *testing.T) {
	got := mapError(downloader.ErrPlatformUnsupported)

	if got.Code != apperrors.CodeUnsupportedPlatform {
		t.Errorf("Code = %q, want %q", got.Code, apperrors.CodeUnsupportedPlatform)
	}
	if got.Category != apperrors.CategoryUnsupportedPlatform {
		t.Errorf("Category = %q, want UNSUPPORTED_PLATFORM", got.Category)
	}
	if got.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", got.Status)
	}
}

func TestMapErrorProviderUnavailable(t *testing.T) {
	got := mapError(downloader.ErrProviderUnavailable)

	if got.Code != apperrors.CodeProviderUnavailable {
		t.Errorf("Code = %q, want %q", got.Code, apperrors.CodeProviderUnavailable)
	}
	if got.Category != apperrors.CategoryProvider {
		t.Errorf("Category = %q, want PROVIDER", got.Category)
	}
	if got.Status != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want 503", got.Status)
	}
	if !got.Retryable {
		t.Error("Retryable = false, want true")
	}
}

func TestMapErrorProviderTimeout(t *testing.T) {
	got := mapError(downloader.ErrProviderTimeout)

	if got.Code != apperrors.CodeProviderTimeout {
		t.Errorf("Code = %q, want %q", got.Code, apperrors.CodeProviderTimeout)
	}
	if got.Category != apperrors.CategoryProvider {
		t.Errorf("Category = %q, want PROVIDER", got.Category)
	}
	if got.Status != http.StatusGatewayTimeout {
		t.Errorf("Status = %d, want 504", got.Status)
	}
	if !got.Retryable {
		t.Error("Retryable = false, want true")
	}
}

func TestMapErrorNotImplemented(t *testing.T) {
	got := mapError(downloader.ErrNotImplemented)

	if got.Code != apperrors.CodeNotImplemented {
		t.Errorf("Code = %q, want %q", got.Code, apperrors.CodeNotImplemented)
	}
	if got.Status != http.StatusNotImplemented {
		t.Errorf("Status = %d, want 501", got.Status)
	}
}

func TestMapErrorProviderInvalidResponse(t *testing.T) {
	got := mapError(downloader.ErrProviderInvalidResponse)

	if got.Code != apperrors.CodeProviderInvalidResponse {
		t.Errorf("Code = %q, want %q", got.Code, apperrors.CodeProviderInvalidResponse)
	}
	if got.Category != apperrors.CategoryProvider {
		t.Errorf("Category = %q, want PROVIDER", got.Category)
	}
	if got.Status != http.StatusBadGateway {
		t.Errorf("Status = %d, want 502", got.Status)
	}
	if got.Retryable {
		t.Error("Retryable = true, want false")
	}
}

func TestMapErrorMediaNotFound(t *testing.T) {
	got := mapError(downloader.ErrMediaNotFound)

	if got.Code != apperrors.CodeMediaNotFound {
		t.Errorf("Code = %q, want %q", got.Code, apperrors.CodeMediaNotFound)
	}
	if got.Category != apperrors.CategoryDownloader {
		t.Errorf("Category = %q, want DOWNLOADER", got.Category)
	}
	if got.Status != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", got.Status)
	}
	if got.Retryable {
		t.Error("Retryable = true, want false")
	}
}

func TestMapErrorBrowserUnavailable(t *testing.T) {
	cases := []error{
		browser.ErrManagerClosed,
		browser.ErrNotStarted,
		browser.ErrUnavailable,
	}

	for _, err := range cases {
		got := mapError(err)
		if got.Category != apperrors.CategoryBrowser {
			t.Errorf("mapError(%v).Category = %q, want BROWSER", err, got.Category)
		}
		if got.Code != apperrors.CodeBrowserUnavailable {
			t.Errorf("mapError(%v).Code = %q, want BROWSER_UNAVAILABLE", err, got.Code)
		}
		if got.Status != http.StatusServiceUnavailable {
			t.Errorf("mapError(%v).Status = %d, want 503", err, got.Status)
		}
		if !got.Retryable {
			t.Errorf("mapError(%v).Retryable = false, want true", err)
		}
	}
}

func TestMapErrorBrowserTimeout(t *testing.T) {
	got := mapError(browser.ErrTimeout)

	if got.Code != apperrors.CodeBrowserTimeout {
		t.Errorf("Code = %q, want %q", got.Code, apperrors.CodeBrowserTimeout)
	}
	if got.Category != apperrors.CategoryBrowser {
		t.Errorf("Category = %q, want BROWSER", got.Category)
	}
	if got.Status != http.StatusGatewayTimeout {
		t.Errorf("Status = %d, want 504", got.Status)
	}
	if !got.Retryable {
		t.Error("Retryable = false, want true")
	}
}

func TestMapErrorBrowserSessionReleased(t *testing.T) {
	got := mapError(browser.ErrSessionReleased)

	if got.Category != apperrors.CategoryBrowser {
		t.Errorf("Category = %q, want BROWSER", got.Category)
	}
	if got.Code != apperrors.CodeBrowserError {
		t.Errorf("Code = %q, want BROWSER_ERROR", got.Code)
	}
	if got.Status != http.StatusInternalServerError {
		t.Errorf("Status = %d, want 500", got.Status)
	}
}

func TestMapErrorUnknownBecomesInternal(t *testing.T) {
	root := stderrors.New("boom")
	got := mapError(root)

	if got.Code != apperrors.CodeInternalError {
		t.Errorf("Code = %q, want INTERNAL_ERROR", got.Code)
	}
	if got.Category != apperrors.CategoryInternal {
		t.Errorf("Category = %q, want INTERNAL", got.Category)
	}
	if !stderrors.Is(got, root) {
		t.Error("unknown error root cause should be preserved")
	}
}

func TestMapErrorContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	<-ctx.Done()

	got := mapError(ctx.Err())
	if got.Code != apperrors.CodeTimeout {
		t.Errorf("Code = %q, want TIMEOUT", got.Code)
	}
	if got.Status != http.StatusGatewayTimeout {
		t.Errorf("Status = %d, want 504", got.Status)
	}
}
