package browser

import "errors"

var (
	ErrManagerClosed = errors.New("browser manager is closed")

	ErrSessionReleased = errors.New("browser session is already released")

	ErrNotStarted = errors.New("browser manager is not started")

	ErrUnavailable = errors.New("browser is unavailable")

	ErrTimeout = errors.New("browser operation timed out")
)
