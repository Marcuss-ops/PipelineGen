// Package generated — spec_aliases.go exposes the stable generation-provider
// contract. Google Slides is the sole implementation.
package generation

import "errors"

type Provider = GenerationProvider

type Registry interface {
	Resolve(providerID string) (Provider, error)
}

type GenerationRegistryImpl = GenerationProviderRegistry

var ErrProviderNotFound = errors.New("generated: provider id not found in registry")

var (
	_ Provider = (*GoogleSlidesProvider)(nil)

	_ Registry = (*GenerationProviderRegistry)(nil)
	_ Registry = (*GenerationRegistryImpl)(nil)
)
