package downloader

import "context"

type PlatformResolver interface {
	ResolvePlatform(ctx context.Context, req DownloadRequest) (Platform, error)
}

type Resolver struct {
	registry *Registry
}

func NewResolver(registry *Registry) *Resolver {
	return &Resolver{registry: registry}
}

func (r *Resolver) ResolvePlatform(_ context.Context, req DownloadRequest) (Platform, error) {
	if req.Platform != "" {
		if _, err := r.registry.Get(req.Platform); err != nil {
			return "", err
		}
		return req.Platform, nil
	}

	for _, p := range r.registry.All() {
		if m, ok := p.(URLMatcher); ok && m.MatchesURL(req.URL) {
			return p.Platform(), nil
		}
	}

	return "", ErrPlatformUnsupported
}
