package generation

import (
	"context"
	"errors"
	"fmt"

	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// Registry is the SSOT for generation-provider dispatch. Production supports
// exactly one provider: Google Slides.
type Registry struct {
	provider Provider
	log      *zap.Logger
}

func NewRegistry(log *zap.Logger, provider Provider) *Registry {
	if log == nil {
		log = zap.NewNop()
	}
	return &Registry{provider: provider, log: log}
}

func NewDefaultRegistry(log *zap.Logger, generator ImageGenerator) *Registry {
	return NewRegistry(log, NewGoogleSlidesProvider(generator, log))
}

func (r *Registry) Generate(ctx context.Context, req GenerateImageRequest, opts GenerateOptions) (*GeneratedImage, error) {
	if r == nil || r.provider == nil {
		return nil, fmt.Errorf("google-slides provider not registered: %w", ErrProviderUnavailable)
	}
	if r.provider.Name() != detail.ProviderGoogleSlides {
		return nil, fmt.Errorf("invalid generation provider %q: only %q is allowed: %w", r.provider.Name(), detail.ProviderGoogleSlides, ErrProviderUnavailable)
	}
	out, err := r.provider.Generate(ctx, req, opts)
	if err != nil {
		return nil, err
	}
	if r.log != nil && out != nil {
		r.log.Info("generation provider dispatched", zap.String("provider", string(detail.ProviderGoogleSlides)), zap.String("model", CanonicalGoogleSlidesModel), zap.Int("bytes", len(out.Data)))
	}
	return out, nil
}

func (r *Registry) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	if r != nil && r.provider != nil {
		r.provider.TriggerPrewarm(ctx, jobID, count)
	}
}

func (r *Registry) ProviderByName(name detail.ImageProvider) Provider {
	if r == nil || r.provider == nil || name != detail.ProviderGoogleSlides {
		return nil
	}
	return r.provider
}

func (r *Registry) Providers() []Provider {
	if r == nil || r.provider == nil {
		return nil
	}
	return []Provider{r.provider}
}

func (r *Registry) Diagnostics(ctx context.Context) map[detail.ImageProvider]error {
	out := make(map[detail.ImageProvider]error, 1)
	if r == nil || r.provider == nil {
		return out
	}
	out[detail.ProviderGoogleSlides] = r.provider.Healthy(ctx)
	return out
}

func (r *Registry) Resolve(providerID string) (Provider, error) {
	if r == nil {
		return nil, errors.New("generated: nil registry")
	}
	if providerID != string(detail.ProviderGoogleSlides) || r.provider == nil {
		return nil, fmt.Errorf("%w (id=%q)", ErrProviderNotFound, providerID)
	}
	return r.provider, nil
}
