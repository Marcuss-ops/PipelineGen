// Package generated (application/images/generated) — provider_registry.go
// holds the GenerationProviderRegistry, the canonical registry seam
// for AI image generation. Per PR-IMG-SPLIT-5 (July 2026), the registry
// is in its own file, separate from types, errors, interfaces, and
// concrete providers.
//
// The generated package no longer ships a default provider. The registry
// is fail-closed by construction: with no provider registered, every
// caller-facing method surfaces ErrProviderUnavailable (or
// ErrProviderNotFound for name-based lookup). Composition roots that
// want a non-empty registry must pass a GenerationProvider explicitly.
//
// File layout:
//
//	types.go                 — DTOs
//	errors.go                — ErrProviderUnavailable sentinel
//	provider.go              — GenerationProvider interface + ImageGeneratorPort
//	provider_registry.go     — GenerationProviderRegistry
package generated

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

// GenerationProviderRegistry is the canonical registry seam for AI image
// generation. The registry holds at most one GenerationProvider; with no
// provider registered it fails closed at every public method.
type GenerationProviderRegistry struct {
	provider GenerationProvider
	log      *zap.Logger
}

// NewGenerationProviderRegistry composes the registry with at most one
// provider. Passing nil for `provider` yields a fail-closed registry
// that returns ErrProviderUnavailable on Generate.
func NewGenerationProviderRegistry(log *zap.Logger, provider GenerationProvider) *GenerationProviderRegistry {
	if log == nil {
		log = zap.NewNop()
	}
	return &GenerationProviderRegistry{provider: provider, log: log}
}

// Generate dispatches to the registered provider. With no provider wired,
// the canonical endpoint surfaces ErrProviderUnavailable (godlike/07
// fail-closed doctrine: never represent an unavailable backend as a
// successful no-op).
func (r *GenerationProviderRegistry) Generate(ctx context.Context, req GenerateRequest, opts GenerateOptions) (*GeneratedImage, error) {
	if r == nil || r.provider == nil {
		return nil, fmt.Errorf("no generation provider registered: %w", ErrProviderUnavailable)
	}

	out, err := r.provider.Generate(ctx, req, opts)
	if err != nil {
		return nil, err
	}
	if r.log != nil {
		r.log.Info("generation provider dispatched",
			zap.Int("bytes", len(out.Data)),
		)
	}
	return out, nil
}

// TriggerPrewarm forwards the warmup signal to the registered provider.
// With no provider wired, the call is a no-op (fail-closed for dispatch
// is enforced separately at Generate time).
func (r *GenerationProviderRegistry) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	if r == nil || r.provider == nil {
		return
	}
	r.provider.TriggerPrewarm(ctx, jobID, count)
}

// ProviderByName is the name-based accessor. With no provider registered,
// it returns nil for every provider ID; callers must check the second
// return value (Resolve) or the canonical Generate gate.
func (r *GenerationProviderRegistry) ProviderByName(name asset.ImageProvider) GenerationProvider {
	if r == nil || r.provider == nil || name == "" {
		return nil
	}
	if r.provider.Name() != name {
		return nil
	}
	return r.provider
}

// Providers returns a list containing the registered provider, or nil
// if no provider is wired.
func (r *GenerationProviderRegistry) Providers() []GenerationProvider {
	if r == nil || r.provider == nil {
		return nil
	}
	return []GenerationProvider{r.provider}
}

// Diagnostics returns a per-provider health map. With no provider
// registered the map is empty (no keys surface, no false positives).
func (r *GenerationProviderRegistry) Diagnostics(ctx context.Context) map[asset.ImageProvider]error {
	out := make(map[asset.ImageProvider]error)
	if r == nil || r.provider == nil {
		return out
	}
	out[r.provider.Name()] = r.provider.Healthy(ctx)
	return out
}

// Resolve implements the generation Registry contract. With no provider
// registered, every provider ID fails closed with ErrProviderNotFound.
func (r *GenerationProviderRegistry) Resolve(providerID string) (GenerationProvider, error) {
	if r == nil {
		return nil, errors.New("generated: nil registry")
	}
	if r.provider == nil || r.provider.Name() != asset.ImageProvider(providerID) {
		return nil, fmt.Errorf("%w (id=%q)", ErrProviderNotFound, providerID)
	}
	return r.provider, nil
}
