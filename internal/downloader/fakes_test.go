package downloader

import "context"

type stubProvider struct {
	name     string
	platform Platform
	ptype    ProviderType
	resolve  func(ctx context.Context, req DownloadRequest) (*DownloadResult, error)
	matches  func(url string) bool
}

func (p *stubProvider) Name() string       { return p.name }
func (p *stubProvider) Platform() Platform { return p.platform }
func (p *stubProvider) Type() ProviderType {
	if p.ptype == "" {
		return ProviderNative
	}
	return p.ptype
}
func (p *stubProvider) Resolve(ctx context.Context, req DownloadRequest) (*DownloadResult, error) {
	if p.resolve == nil {
		return &DownloadResult{Platform: p.platform, URL: req.URL}, nil
	}
	return p.resolve(ctx, req)
}

func (p *stubProvider) MatchesURL(url string) bool {
	if p.matches == nil {
		return false
	}
	return p.matches(url)
}

type browserCapableProvider struct {
	stubProvider
	runtime BrowserRuntime
}

func (p *browserCapableProvider) SetBrowserRuntime(rt BrowserRuntime) {
	p.runtime = rt
}

func (p *browserCapableProvider) BrowserRuntime() BrowserRuntime {
	return p.runtime
}

type fakeBrowserRuntime struct {
	name  string
	fetch func(ctx context.Context, url string) (string, error)
}

func (f *fakeBrowserRuntime) Name() string { return f.name }

func (f *fakeBrowserRuntime) Fetch(ctx context.Context, url string) (string, error) {
	if f.fetch == nil {
		return "", nil
	}
	return f.fetch(ctx, url)
}

type lifecycleProvider struct {
	stubProvider
	init     func(ctx context.Context) error
	ready    func() error
	shutdown func(ctx context.Context) error
}

func (p *lifecycleProvider) Init(ctx context.Context) error {
	if p.init == nil {
		return nil
	}
	return p.init(ctx)
}

func (p *lifecycleProvider) Ready() error {
	if p.ready == nil {
		return nil
	}
	return p.ready()
}

func (p *lifecycleProvider) Shutdown(ctx context.Context) error {
	if p.shutdown == nil {
		return nil
	}
	return p.shutdown(ctx)
}
