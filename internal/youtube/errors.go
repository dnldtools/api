package youtube

import "errors"

// Sentinel errors returned by the YouTube service. They are mapped to HTTP
// AppError values in internal/http/mapper.go, mirroring how downloader
// provider errors are handled.
var (
	// ErrInvalidURL is returned when a convert request has no usable URL.
	ErrInvalidURL = errors.New("url is required")

	// ErrQueryRequired is returned when search is called without a query.
	ErrQueryRequired = errors.New("query is required")

	// ErrFormatUnavailable is returned when the requested output format or
	// quality is not part of the catalog.
	ErrFormatUnavailable = errors.New("format is not available")

	// ErrProviderUnavailable is returned when the upstream (convert1s) has no
	// healthy worker or rejects the request as temporarily unavailable.
	ErrProviderUnavailable = errors.New("youtube provider is unavailable")

	// ErrProviderTimeout is returned when an upstream request or status poll
	// exceeds its deadline.
	ErrProviderTimeout = errors.New("youtube provider timed out")

	// ErrProviderInvalidResponse is returned when the upstream responds with
	// an unexpected body or an HTTP status that is not retryable.
	ErrProviderInvalidResponse = errors.New("youtube provider returned an invalid response")

	// ErrMediaNotFound is returned when the upstream reports the media cannot
	// be found or the job permanently failed.
	ErrMediaNotFound = errors.New("video not found")
)
