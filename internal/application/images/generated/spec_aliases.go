// Package generated — spec_aliases.go declares the user-spec surface
// (FASE 5, July 2026) on top of Step 8's canonical implementation.
package generated

import "errors"

type Provider = GenerationProvider

type Registry interface {
	Resolve(providerID string) (Provider, error)
}

type GenerationRegistryImpl = GenerationProviderRegistry

type ResolvedGenerationRequest = GenerateRequest

var ErrProviderNotFound = errors.New("generated: provider id not found in registry")

var (
	_ Provider = (*GoogleSlidesProvider)(nil)
	_ Provider = (*FluxProvider)(nil)
	_ Provider = (*NvidiaProvider)(nil)

	_ Registry = (*GenerationProviderRegistry)(nil)
	_ Registry = (*GenerationRegistryImpl)(nil)
)
