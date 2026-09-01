package youtube

import "errors"

var (
	ErrInvalidURL              = errors.New("url is required")
	ErrQueryRequired           = errors.New("query is required")
	ErrFormatUnavailable       = errors.New("format is not available")
	ErrProviderUnavailable     = errors.New("youtube provider is unavailable")
	ErrProviderTimeout         = errors.New("youtube provider timed out")
	ErrProviderInvalidResponse = errors.New("youtube provider returned an invalid response")
	ErrMediaNotFound           = errors.New("video not found")
)
