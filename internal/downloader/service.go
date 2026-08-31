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

var _ Downloader = (*Service)(nil)
