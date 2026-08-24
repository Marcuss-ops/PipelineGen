// Package generated (application/images/generated) — provider_registry.go
// holds the GenerationProviderRegistry — the canonical single-provider
// registry for AI image generation. Per PR-IMG-SPLIT-5 (July 2026), the
// registry is now in its own file, separate from types, errors, interfaces,
// and concrete providers.
//
// Google Slides, driven through Chrome/Playwright and Nano Banana Pro, is
// the only supported generation path. The registry enforces a
// single-provider invariant: non-Google-Slides providers are rejected at
// Generate time.
//
// File layout:
//
//	types.go                 — DTOs + CanonicalGoogleSlidesModel constant
//	errors.go                — ErrProviderUnavailable sentinel
//	provider.go              — GenerationProvider interface + ImageGeneratorPort
//	provider_google_slides.go — GoogleSlidesProvider
//	provider_registry.go      — GenerationProviderRegistry
package generation

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// GenerationProviderRegistry retains the registry seam while enforcing a
// single-provider invariant. This keeps composition/test boundaries stable
// without preserving unnecessary provider selection logic.
type GenerationProviderRegistry struct {
	provider GenerationProvider
	log      *zap.Logger
}

// NewGenerationProviderRegistry composes the registry with a single provider.
func NewGenerationProviderRegistry(log *zap.Logger, provider GenerationProvider) *GenerationProviderRegistry {
	if log == nil {
		log = zap.NewNop()
	}
	return &GenerationProviderRegistry{provider: provider, log: log}
}

// NewDefaultProviderRegistry builds the only supported generation registry.
func NewDefaultProviderRegistry(log *zap.Logger, googleSlidesPort ImageGeneratorPort) *GenerationProviderRegistry {
	return NewGenerationProviderRegistry(log, NewGoogleSlidesProvider(googleSlidesPort, log))
}

// Generate dispatches to the single registered provider. Non-Google-Slides
// providers are rejected at this gate (fail-closed per godlike/07).
func (r *GenerationProviderRegistry) Generate(ctx context.Context, req GenerateRequest, opts GenerateOptions) (*GeneratedImage, error) {
	if r == nil || r.provider == nil {
		return nil, fmt.Errorf("google-slides provider not registered: %w", ErrProviderUnavailable)
	}
	if r.provider.Name() != asset.ProviderGoogleSlides {
		return nil, fmt.Errorf("invalid generation provider %q: only %q is allowed: %w",
			r.provider.Name(), asset.ProviderGoogleSlides, ErrProviderUnavailable)
	}

	out, err := r.provider.Generate(ctx, req, opts)
	if err != nil {
		return nil, err
	}
	if r.log != nil {
		r.log.Info("generation provider dispatched",
			zap.String("provider", string(asset.ProviderGoogleSlides)),
			zap.String("model", CanonicalGoogleSlidesModel),
			zap.Int("bytes", len(out.Data)),
		)
	}
	return out, nil
}

// TriggerPrewarm forwards the warmup signal to the single registered provider.
func (r *GenerationProviderRegistry) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	if r == nil || r.provider == nil {
		return
	}
	r.provider.TriggerPrewarm(ctx, jobID, count)
}

// ProviderByName returns Google Slides for its canonical ID and nil for every
// other provider name.
func (r *GenerationProviderRegistry) ProviderByName(name asset.ImageProvider) GenerationProvider {
	if r == nil || r.provider == nil || name != asset.ProviderGoogleSlides {
		return nil
	}
	return r.provider
}

// Providers returns either the sole Google Slides provider or an empty list.
func (r *GenerationProviderRegistry) Providers() []GenerationProvider {
	if r == nil || r.provider == nil {
		return nil
	}
	return []GenerationProvider{r.provider}
}

// Diagnostics probes only the real Google Slides provider.
func (r *GenerationProviderRegistry) Diagnostics(ctx context.Context) map[asset.ImageProvider]error {
	out := make(map[asset.ImageProvider]error, 1)
	if r == nil || r.provider == nil {
		return out
	}
	out[asset.ProviderGoogleSlides] = r.provider.Healthy(ctx)
	return out
}

// Resolve implements the generation Registry contract. Only google-slides is
// resolvable; all former provider IDs fail closed.
func (r *GenerationProviderRegistry) Resolve(providerID string) (GenerationProvider, error) {
	if r == nil {
		return nil, errors.New("generated: nil registry")
	}
	if providerID != string(asset.ProviderGoogleSlides) || r.provider == nil {
		return nil, fmt.Errorf("%w (id=%q)", ErrProviderNotFound, providerID)
	}
	return r.provider, nil
}
