package downloader

import "errors"

var (
	ErrInvalidURL = errors.New("url is required")

	ErrPlatformUnsupported = errors.New("platform not supported")

	ErrProviderUnavailable = errors.New("provider is unavailable")

	ErrProviderTimeout = errors.New("provider timed out")

	ErrNotImplemented = errors.New("downloader not implemented yet")

	ErrProviderInvalidResponse = errors.New("provider returned an invalid response")

	ErrMediaNotFound = errors.New("media not found")

	ErrStreamUnsupported = errors.New("streaming is not supported for this platform")
)
