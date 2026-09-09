// Package youtube provides a downloader.Provider guard for YouTube URLs.
//
// YouTube is intentionally not resolved through the generic downloader flow:
// it has a dedicated set of endpoints (/v1/youtube/*) backed by
// internal/youtube. This provider only claims YouTube URLs during platform
// auto-detection so they no longer fall through to the generic 9xbuddy
// fallback, and returns ErrYouTubeSeparateEndpoint so callers are directed to
// the dedicated endpoints.
package youtube

import (
	"context"
	"net/url"
	"strings"

	"rest-api/internal/downloader"
)

type Provider struct{}

var _ downloader.Provider = (*Provider)(nil)

var _ downloader.URLMatcher = (*Provider)(nil)

func New() *Provider { return &Provider{} }

func (p *Provider) Name() string { return "youtube" }

func (p *Provider) Platform() downloader.Platform { return downloader.PlatformYouTube }

func (p *Provider) Type() downloader.ProviderType { return downloader.ProviderExternalAPI }

func (p *Provider) MatchesURL(u string) bool {
	parsed, err := url.Parse(strings.TrimSpace(u))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	for _, suffix := range []string{"youtube.com", "youtube-nocookie.com", "youtu.be"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func (p *Provider) Resolve(context.Context, downloader.DownloadRequest) (*downloader.DownloadResult, error) {
	return nil, downloader.ErrYouTubeSeparateEndpoint
}
