package downloader

import "context"

type Lifecycle interface {
	Init(ctx context.Context) error

	Ready() error

	Shutdown(ctx context.Context) error
}
