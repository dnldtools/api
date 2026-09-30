package downloader

import (
	"context"
	"strings"
)

type PlatformResolver interface {
	ResolvePlatform(ctx context.Context, req DownloadRequest) (Platform, error)
}

type Resolver struct {
	registry *Registry
}

func NewResolver(registry *Registry) *Resolver {
	return &Resolver{registry: registry}
}

var platformAliases = map[string]Platform{
	"apple":       PlatformApple,
	"apple music": PlatformApple,
	"apple-music": PlatformApple,
	"applemusic":  PlatformApple,
}

func normalizePlatform(p Platform) Platform {
	key := strings.ToLower(strings.TrimSpace(string(p)))
	if alias, ok := platformAliases[key]; ok {
		return alias
	}
	return p
}

func (r *Resolver) ResolvePlatform(_ context.Context, req DownloadRequest) (Platform, error) {
	if req.Platform != "" {
		platform := normalizePlatform(req.Platform)
		if _, err := r.registry.Get(platform); err != nil {
			return "", err
		}
		return platform, nil
	}

	for _, p := range r.registry.All() {
		if m, ok := p.(URLMatcher); ok && m.MatchesURL(req.URL) {
			return p.Platform(), nil
		}
	}

	return "", ErrPlatformUnsupported
}
