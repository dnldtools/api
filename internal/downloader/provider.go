package downloader

import "context"

type ProviderType string

const (
	ProviderNative ProviderType = "native"

	ProviderBrowser ProviderType = "browser"

	ProviderExternalAPI ProviderType = "external_api"
)

type Provider interface {
	Name() string

	Platform() Platform

	Type() ProviderType

	Resolve(ctx context.Context, req DownloadRequest) (*DownloadResult, error)
}

type URLMatcher interface {
	MatchesURL(url string) bool
}

type BrowserRuntime interface {
	Name() string

	Fetch(ctx context.Context, url string) (string, error)
}

type BrowserCapable interface {
	SetBrowserRuntime(rt BrowserRuntime)
}
