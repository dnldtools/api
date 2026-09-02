package downloader

import (
	"context"
	"errors"
)

type Downloader interface {
	Resolve(ctx context.Context, req DownloadRequest) (*DownloadResult, error)

	SupportedPlatforms() []Platform
}

type Service struct {
	resolver PlatformResolver
	registry *Registry
}

func NewService(registry *Registry) *Service {
	return NewServiceWithResolver(NewResolver(registry), registry)
}

func NewServiceWithResolver(resolver PlatformResolver, registry *Registry) *Service {
	return &Service{resolver: resolver, registry: registry}
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

	platform, err := s.resolver.ResolvePlatform(ctx, req)
	if err != nil {
		return nil, err
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

	result, err := provider.Resolve(ctx, req)
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
