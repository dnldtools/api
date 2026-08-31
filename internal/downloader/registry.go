package downloader

import (
	"context"
	stderrors "errors"
	"fmt"
)

type Registry struct {
	providers map[Platform]Provider
	order     []Platform
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[Platform]Provider)}
}

func (r *Registry) Register(p Provider) error {
	if p == nil {
		return fmt.Errorf("provider is nil")
	}

	platform := p.Platform()
	if platform == "" {
		return fmt.Errorf("provider %q has an empty platform", p.Name())
	}

	if _, exists := r.providers[platform]; exists {
		return fmt.Errorf("provider for platform %q already registered", platform)
	}

	r.providers[platform] = p
	r.order = append(r.order, platform)
	return nil
}

func (r *Registry) Get(platform Platform) (Provider, error) {
	p, ok := r.providers[platform]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrPlatformUnsupported, platform)
	}
	return p, nil
}

func (r *Registry) All() []Provider {
	out := make([]Provider, 0, len(r.order))
	for _, platform := range r.order {
		out = append(out, r.providers[platform])
	}
	return out
}

func (r *Registry) Platforms() []Platform {
	out := make([]Platform, len(r.order))
	copy(out, r.order)
	return out
}

func (r *Registry) Len() int {
	return len(r.order)
}

func (r *Registry) Init(ctx context.Context) error {
	var errs []error
	for _, p := range r.All() {
		lc, ok := p.(Lifecycle)
		if !ok {
			continue
		}
		if err := lc.Init(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
		}
	}
	return stderrors.Join(errs...)
}

func (r *Registry) Shutdown(ctx context.Context) error {
	var errs []error
	for _, p := range r.All() {
		lc, ok := p.(Lifecycle)
		if !ok {
			continue
		}
		if err := lc.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
		}
	}
	return stderrors.Join(errs...)
}
