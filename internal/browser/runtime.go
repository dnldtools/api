package browser

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Runtime struct {
	mgr        Manager
	navTimeout time.Duration
}

func NewRuntime(mgr Manager, navTimeout time.Duration) (*Runtime, error) {
	if mgr == nil {
		return nil, errors.New("browser: manager is nil")
	}
	if navTimeout <= 0 {
		navTimeout = DefaultConfig().NavigationTimeout
	}
	return &Runtime{mgr: mgr, navTimeout: navTimeout}, nil
}

func (r *Runtime) Name() string { return r.mgr.Name() }

func (r *Runtime) Fetch(ctx context.Context, url string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	fetchCtx, cancel := context.WithTimeout(ctx, r.navTimeout)
	defer cancel()

	session, err := r.mgr.Acquire(fetchCtx)
	if err != nil {
		return "", classifyFetchError(err, fetchCtx)
	}
	defer session.Release()

	page, err := session.NewPage(fetchCtx)
	if err != nil {
		return "", classifyFetchError(err, fetchCtx)
	}

	if err := page.Goto(fetchCtx, url); err != nil {
		return "", classifyFetchError(err, fetchCtx)
	}

	content, err := page.Content(fetchCtx)
	if err != nil {
		return "", classifyFetchError(err, fetchCtx)
	}
	return content, nil
}

func classifyFetchError(err error, ctx context.Context) error {
	switch {
	case ctx.Err() == context.Canceled || errors.Is(err, context.Canceled):
		return context.Canceled
	case ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	case errors.Is(err, ErrManagerClosed), errors.Is(err, ErrNotStarted):
		return ErrUnavailable
	default:
		return err
	}
}
