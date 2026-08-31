package playwright

import (
	"context"
	"fmt"
	"sync"
	"time"

	pw "github.com/playwright-community/playwright-go"

	"rest-api/internal/browser"
)

type Launcher struct {
	mu      sync.Mutex
	pw      *pw.Playwright
	stopped bool
}

func NewLauncher() *Launcher {
	return &Launcher{}
}

func (l *Launcher) Name() string {
	return "playwright"
}

func (l *Launcher) Launch(ctx context.Context, opts browser.LaunchOptions) (browser.Browser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.stopped {
		return nil, browser.ErrManagerClosed
	}

	if l.pw == nil {
		p, err := pw.Run()
		if err != nil {
			return nil, fmt.Errorf("playwright: start driver: %w", err)
		}
		l.pw = p
	}

	launchOpts := pw.BrowserTypeLaunchOptions{
		Headless: pw.Bool(opts.Headless),
	}
	if opts.ExecutablePath != "" {
		launchOpts.ExecutablePath = pw.String(opts.ExecutablePath)
	}
	if opts.Proxy.Server != "" {
		launchOpts.Proxy = &pw.Proxy{
			Server:   opts.Proxy.Server,
			Username: strPtr(opts.Proxy.Username),
			Password: strPtr(opts.Proxy.Password),
		}
	}
	if opts.Timeout > 0 {
		launchOpts.Timeout = pw.Float(float64(opts.Timeout.Milliseconds()))
	}

	b, err := l.pw.Chromium.Launch(launchOpts)
	if err != nil {
		return nil, fmt.Errorf("playwright: launch chromium: %w", err)
	}

	return &browserAdapter{pw: l.pw, browser: b}, nil
}

func (l *Launcher) Stop(_ context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.stopped {
		return nil
	}
	l.stopped = true

	if l.pw != nil {
		if err := l.pw.Stop(); err != nil {
			return fmt.Errorf("playwright: stop driver: %w", err)
		}
	}
	return nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return pw.String(s)
}

type browserAdapter struct {
	pw      *pw.Playwright
	browser pw.Browser
}

func (b *browserAdapter) Name() string { return "chromium" }

func (b *browserAdapter) Version() string {
	if b.browser == nil {
		return ""
	}
	return b.browser.Version()
}

func (b *browserAdapter) NewContext(ctx context.Context, opts browser.ContextOptions) (browser.BrowserContext, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	bctxOpts := pw.BrowserNewContextOptions{}
	if opts.UserAgent != "" {
		bctxOpts.UserAgent = pw.String(opts.UserAgent)
	}
	if opts.Proxy.Server != "" {
		bctxOpts.Proxy = &pw.Proxy{
			Server:   opts.Proxy.Server,
			Username: strPtr(opts.Proxy.Username),
			Password: strPtr(opts.Proxy.Password),
		}
	}

	bctx, err := b.browser.NewContext(bctxOpts)
	if err != nil {
		return nil, fmt.Errorf("playwright: new context: %w", err)
	}

	if opts.Timeout > 0 {
		bctx.SetDefaultTimeout(float64(opts.Timeout.Milliseconds()))
	}
	if opts.NavigationTimeout > 0 {
		bctx.SetDefaultNavigationTimeout(float64(opts.NavigationTimeout.Milliseconds()))
	}

	return &contextAdapter{bctx: bctx}, nil
}

func (b *browserAdapter) Close(_ context.Context) error {
	if b.browser == nil {
		return nil
	}
	if err := b.browser.Close(); err != nil {
		return fmt.Errorf("playwright: close browser: %w", err)
	}
	return nil
}

type contextAdapter struct {
	bctx pw.BrowserContext
}

func (c *contextAdapter) NewPage(ctx context.Context) (browser.Page, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	page, err := c.bctx.NewPage()
	if err != nil {
		return nil, fmt.Errorf("playwright: new page: %w", err)
	}
	return &pageAdapter{page: page}, nil
}

func (c *contextAdapter) Close(_ context.Context) error {
	if c.bctx == nil {
		return nil
	}
	if err := c.bctx.Close(); err != nil {
		return fmt.Errorf("playwright: close context: %w", err)
	}
	return nil
}

type pageAdapter struct {
	page pw.Page
}

func (p *pageAdapter) Goto(ctx context.Context, url string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	opts := pw.PageGotoOptions{}
	if deadline, ok := ctx.Deadline(); ok {
		if timeout := time.Until(deadline).Milliseconds(); timeout > 0 {
			opts.Timeout = pw.Float(float64(timeout))
		}
	}

	if _, err := p.page.Goto(url, opts); err != nil {
		return fmt.Errorf("playwright: goto %q: %w", url, err)
	}
	return nil
}

func (p *pageAdapter) Content(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	content, err := p.page.Content()
	if err != nil {
		return "", fmt.Errorf("playwright: content: %w", err)
	}
	return content, nil
}

func (p *pageAdapter) Title(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	title, err := p.page.Title()
	if err != nil {
		return "", fmt.Errorf("playwright: title: %w", err)
	}
	return title, nil
}

func (p *pageAdapter) URL() string {
	if p.page == nil {
		return ""
	}
	return p.page.URL()
}

func (p *pageAdapter) Close(_ context.Context) error {
	if p.page == nil {
		return nil
	}
	if err := p.page.Close(); err != nil {
		return fmt.Errorf("playwright: close page: %w", err)
	}
	return nil
}
