// Package styles — registry.go declares the application-layer
// wrapper around generation.StyleRegistry.
//
// Per the July 2026 image-restructuring plan, the application
// layer should be able to *lookup* a registered style at boot
// and consult the canonical registry handle. The underlying
// canonical implementation lives in
// internal/application/assets/generation — this file is a thin
// wrapper that exposes a stable name (styles.Registry) without
// forcing callers to import the deeper package directly.
//
// Step 9 (July 2026): the wrapper is a struct that EMBEDS the
// canonical registry, so all methods are available transitively.
// Explicit Register/Lookup/List forwarders exist for the small
// surface most application-layer code touches.
//
// NOTE: generation.StyleRegistry is today a YAML-backed, runtime
// read-only registry. Register returns a typed error to surface
// the read-only contract; migrations that need runtime
// registration must use the YAML bootstrap path.
package styles

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
)

// Registry is the application-layer wrapper around
// generation.StyleRegistry. Composition root construction calls
// NewRegistry to obtain one; all public methods forward to the
// canonical implementation.
type Registry struct {
	inner *generation.StyleRegistry
}

// ErrRegistryReadOnly is returned by Register when caller code
// attempts to add a StyleDefinition at runtime. The canonical
// registry is YAML-backed today; runtime mutations require
// re-via generation.NewStyleRegistry.
var ErrRegistryReadOnly = errors.New("styles.Registry: runtime Register not supported (YAML bootstrap only)")

// NewRegistry wraps a non-nil *generation.StyleRegistry.
// Returns nil if `inner` is nil — callers must handle the nil
// case (back-compat with Step 4 pre-Stage-5 wiring where
// StyleRegistry was optional).
func NewRegistry(inner *generation.StyleRegistry) *Registry {
	if inner == nil {
		return nil
	}
	return &Registry{inner: inner}
}

// Register adds a new StyleDefinition at runtime. Today this
// always returns ErrRegistryReadOnly because canonical state is
// YAML-backed; composition roots that need to extend the recipe
// set should write to the style registry YAML and reload.
func (r *Registry) Register(_ context.Context, _ StyleDefinition) error {
	if r == nil {
		return ErrRegistryReadOnly
	}
	return ErrRegistryReadOnly
}

// Lookup returns the StyleDefinition for the given StyleID, or
// (zero, false) if not found. Uses generation.StyleRegistry.Get
// under the hood (case-insensitive name lookup).
func (r *Registry) Lookup(_ context.Context, id StyleID) (StyleDefinition, bool) {
	if r == nil || r.inner == nil {
		return StyleDefinition{}, false
	}
	if id == "" {
		return StyleDefinition{}, false
	}
	return r.inner.Get(id)
}

// List returns all registered StyleDefinitions. Used by the
// admin UI / health endpoints. alphabetical ordering is handled
// by the canonical registry when wired (List() returns an
// unordered slice — callers should sort if needed).
func (r *Registry) List(_ context.Context) []StyleDefinition {
	if r == nil || r.inner == nil {
		return nil
	}
	return r.inner.List()
}

// Inner returns the underlying canonical *generation.StyleRegistry.
// Reserved for tests + composition roots; application code should
// use the wrapped surface above.
func (r *Registry) Inner() *generation.StyleRegistry {
	if r == nil {
		return nil
	}
	return r.inner
}
