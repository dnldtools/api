package http

import (
	stderrors "errors"

	"rest-api/internal/browser"
	"rest-api/internal/downloader"
	apperrors "rest-api/internal/errors"
	"rest-api/internal/youtube"
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
		return apperrors.Validation(apperrors.CodeInvalidURL, "Please provide a valid link.").WithCause(err)
	case stderrors.Is(err, downloader.ErrPlatformUnsupported):
		return apperrors.UnsupportedPlatform(apperrors.CodeUnsupportedPlatform, "That platform isn't supported yet.").WithCause(err)
	case stderrors.Is(err, downloader.ErrProviderUnavailable):
		return apperrors.ProviderUnavailable("We couldn't reach the download service. Please try again in a moment.").WithCause(err)
	case stderrors.Is(err, downloader.ErrProviderTimeout):
		return apperrors.ProviderTimeout("That took too long. Please try again.").WithCause(err)
	case stderrors.Is(err, downloader.ErrNotImplemented):
		return apperrors.NotImplemented(apperrors.CodeNotImplemented, "This downloader isn't available yet.").WithCause(err)
	case stderrors.Is(err, downloader.ErrProviderInvalidResponse):
		return apperrors.ProviderInvalidResponse("We couldn't get that media right now. Please try again.").WithCause(err)
	case stderrors.Is(err, downloader.ErrMediaNotFound):
		return apperrors.MediaNotFound("We couldn't find that media. It may be private or removed.").WithCause(err)
	case stderrors.Is(err, downloader.ErrStreamUnsupported):
		return apperrors.NotImplemented(apperrors.CodeNotImplemented, "Streaming isn't supported for this platform.").WithCause(err)

	case stderrors.Is(err, youtube.ErrInvalidURL):
		return apperrors.Validation(apperrors.CodeInvalidURL, "Please provide a valid link.").WithCause(err)
	case stderrors.Is(err, youtube.ErrQueryRequired):
		return apperrors.Validation(apperrors.CodeMissingParameter, "A search query is required.").WithCause(err)
	case stderrors.Is(err, youtube.ErrFormatUnavailable):
		return apperrors.Validation(apperrors.CodeFormatNotAvailable, "That quality isn't available for this video.").WithCause(err)
	case stderrors.Is(err, youtube.ErrProviderUnavailable):
		return apperrors.ProviderUnavailable("We couldn't reach the download service. Please try again in a moment.").WithCause(err)
	case stderrors.Is(err, youtube.ErrProviderTimeout):
		return apperrors.ProviderTimeout("That took too long. Please try again.").WithCause(err)
	case stderrors.Is(err, youtube.ErrProviderInvalidResponse):
		return apperrors.ProviderInvalidResponse("We couldn't get that video right now. Please try again.").WithCause(err)
	case stderrors.Is(err, youtube.ErrMediaNotFound):
		return apperrors.MediaNotFound("We couldn't find that video.").WithCause(err)

	case stderrors.Is(err, browser.ErrManagerClosed),
		stderrors.Is(err, browser.ErrNotStarted),
		stderrors.Is(err, browser.ErrUnavailable):
		return apperrors.BrowserUnavailable("The download service is temporarily unavailable. Please try again later.").WithCause(err)
	case stderrors.Is(err, browser.ErrTimeout):
		return apperrors.BrowserTimeout("That took too long. Please try again.").WithCause(err)
	case stderrors.Is(err, browser.ErrSessionReleased):
		return apperrors.Browser(apperrors.CodeBrowserError, "Something went wrong on our end. Please try again.").WithCause(err)
	}

	return apperrors.From(err)
}
