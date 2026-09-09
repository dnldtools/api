package downloader

import (
	"context"
	"errors"
	"time"
)

const (
	defaultResolveTimeout = 45 * time.Second
	defaultCacheTTL       = 10 * time.Minute
)

type Downloader interface {
	Resolve(ctx context.Context, req DownloadRequest) (*DownloadResult, error)

	SupportedPlatforms() []Platform
}

type Service struct {
	resolver PlatformResolver
	registry *Registry

	// cache is an optional result cache. When set, successful resolves are
	// stored (and served) keyed by platform + URL. A nil cache is a no-op.
	cache    ResultCache
	cacheTTL time.Duration

	// resolveTimeout bounds the whole Resolve call (platform detection + one
	// provider). It must stay below the HTTP server WriteTimeout so requests do
	// not outlive the socket deadline. Zero falls back to defaultResolveTimeout.
	resolveTimeout time.Duration
}

func NewService(registry *Registry) *Service {
	return NewServiceWithResolver(NewResolver(registry), registry)
}

func NewServiceWithResolver(resolver PlatformResolver, registry *Registry) *Service {
	return &Service{
		resolver:       resolver,
		registry:       registry,
		cacheTTL:       defaultCacheTTL,
		resolveTimeout: defaultResolveTimeout,
	}
}

// SetCache enables result caching. ttl <= 0 falls back to defaultCacheTTL.
func (s *Service) SetCache(c ResultCache, ttl time.Duration) {
	s.cache = c
	s.cacheTTL = ttl
}

// SetResolveTimeout overrides the per-request resolve deadline. d <= 0 falls
// back to defaultResolveTimeout.
func (s *Service) SetResolveTimeout(d time.Duration) {
	s.resolveTimeout = d
}

func (s *Service) Registry() *Registry {
	return s.registry
}

func (s *Service) SupportedPlatforms() []Platform {
	return s.registry.Platforms()
}

func (s *Service) Resolve(ctx context.Context, req DownloadRequest) (*DownloadResult, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	timeout := s.resolveTimeout
	if timeout <= 0 {
		timeout = defaultResolveTimeout
	}
	resolveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	platform, err := s.resolver.ResolvePlatform(resolveCtx, req)
	if err != nil {
		return nil, err
	}

	key := CacheKey{Platform: platform, URL: req.URL}
	if s.cache != nil {
		if cached, ok := s.cache.Get(resolveCtx, key); ok {
			return cached, nil
		}
	}

	provider, err := s.registry.Get(platform)
	if err != nil {
		return nil, err
	}

	if lc, ok := provider.(Lifecycle); ok {
		if err := lc.Ready(); err != nil {
			return nil, errors.Join(ErrProviderUnavailable, err)
		}
	}

	req.Platform = platform

	result, err := provider.Resolve(resolveCtx, req)
	if err != nil {
		return nil, err
	}

	if result == nil {
		result = &DownloadResult{}
	}
	if result.Platform == "" {
		result.Platform = platform
	}
	if result.URL == "" {
		result.URL = req.URL
	}

	if s.cache != nil {
		ttl := s.cacheTTL
		if ttl <= 0 {
			ttl = defaultCacheTTL
		}
		// Cache under the request URL and, when the provider canonicalised it
		// (e.g. TikTok short link -> canonical URL), also under the canonical
		// URL so the streaming-proxy path hits the cache without re-scraping.
		_ = s.cache.Set(ctx, key, result, ttl)
		if result.URL != "" && result.URL != req.URL {
			_ = s.cache.Set(ctx, CacheKey{Platform: platform, URL: result.URL}, result, ttl)
		}
	}

	return result, nil
}

// StreamMedia fetches a resolved media URL through the provider registered for
// the given platform. The provider is responsible for attaching any session
// state (cookies/headers) required by the upstream and for validating the URL
// against its own allowlist to prevent SSRF.
func (s *Service) StreamMedia(ctx context.Context, platform Platform, mediaURL string) (*MediaStream, error) {
	provider, err := s.registry.Get(platform)
	if err != nil {
		return nil, err
	}

	if lc, ok := provider.(Lifecycle); ok {
		if err := lc.Ready(); err != nil {
			return nil, errors.Join(ErrProviderUnavailable, err)
		}
	}

	streamer, ok := provider.(MediaStreamer)
	if !ok {
		return nil, ErrStreamUnsupported
	}
	return streamer.StreamMedia(ctx, mediaURL)
}

var _ Downloader = (*Service)(nil)
