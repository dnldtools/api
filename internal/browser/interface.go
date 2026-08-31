package browser

import "context"

type Browser interface {
	Name() string

	Version() string

	NewContext(ctx context.Context, opts ContextOptions) (BrowserContext, error)

	Close(ctx context.Context) error
}

type BrowserContext interface {
	NewPage(ctx context.Context) (Page, error)

	Close(ctx context.Context) error
}

type Page interface {
	Goto(ctx context.Context, url string) error

	Content(ctx context.Context) (string, error)

	Title(ctx context.Context) (string, error)

	URL() string

	Close(ctx context.Context) error
}

type Launcher interface {
	Name() string

	Launch(ctx context.Context, opts LaunchOptions) (Browser, error)

	Stop(ctx context.Context) error
}

type Manager interface {
	Start(ctx context.Context) error

	Acquire(ctx context.Context) (*Session, error)

	Shutdown(ctx context.Context) error

	Name() string
}
