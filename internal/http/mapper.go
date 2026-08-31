package http

import (
	stderrors "errors"

	"rest-api/internal/browser"
	"rest-api/internal/downloader"
	apperrors "rest-api/internal/errors"
)

func mapError(err error) *apperrors.AppError {
	if err == nil {
		return nil
	}

	if appErr, ok := apperrors.AsAppError(err); ok {
		return appErr
	}

	switch {
	case stderrors.Is(err, downloader.ErrInvalidURL):
		return apperrors.Validation(apperrors.CodeInvalidURL, "url is required").WithCause(err)
	case stderrors.Is(err, downloader.ErrPlatformUnsupported):
		return apperrors.UnsupportedPlatform(apperrors.CodeUnsupportedPlatform, "platform not supported").WithCause(err)
	case stderrors.Is(err, downloader.ErrProviderUnavailable):
		return apperrors.ProviderUnavailable("provider is unavailable").WithCause(err)
	case stderrors.Is(err, downloader.ErrProviderTimeout):
		return apperrors.ProviderTimeout("provider timed out").WithCause(err)
	case stderrors.Is(err, downloader.ErrNotImplemented):
		return apperrors.NotImplemented(apperrors.CodeNotImplemented, "downloader not implemented yet").WithCause(err)
	case stderrors.Is(err, downloader.ErrProviderInvalidResponse):
		return apperrors.ProviderInvalidResponse("provider returned an invalid response").WithCause(err)
	case stderrors.Is(err, downloader.ErrMediaNotFound):
		return apperrors.MediaNotFound("media not found").WithCause(err)

	case stderrors.Is(err, browser.ErrManagerClosed),
		stderrors.Is(err, browser.ErrNotStarted),
		stderrors.Is(err, browser.ErrUnavailable):
		return apperrors.BrowserUnavailable("browser is unavailable").WithCause(err)
	case stderrors.Is(err, browser.ErrTimeout):
		return apperrors.BrowserTimeout("browser timed out").WithCause(err)
	case stderrors.Is(err, browser.ErrSessionReleased):
		return apperrors.Browser(apperrors.CodeBrowserError, "browser session is unavailable").WithCause(err)
	}

	return apperrors.From(err)
}
