// Package scripts — source_registry.go defines the SourceResolver
// interface and the SourceRegistry dispatcher. Every SourceType has
// exactly one registered resolver; the registry dispatches resolution
// calls to the correct resolver.
//
// This replaces the 3-way switch (clip-explicit / auto-search /
// text-only) in pipeline_handlers.go and the per-endpoint source
// selection scattered across handler_flow.go, catalog_job.go, and
// curation_job.go.
package scripts

import (
	"context"
	"fmt"
	"sync"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// SourceResolver resolves a SourceSpec into a ResolvedSource.
// Every resolver produces the same output shape so the engine
// never branches on source type.
//
// Implementation rules:
//   - Text resolver: assembles topic + source_text + guidelines.
//   - Clips resolver: fetches explicit clip IDs via ClipSourceBuilder.
//   - Catalog resolver: searches the local catalog, then builds context.
//   - Search resolver: performs semantic search, then builds context.
//
// No resolver calls the engine directly — that's PR4's responsibility.
type SourceResolver interface {
	Resolve(ctx context.Context, src scriptpkg.SourceSpec, itemID string) (*scriptpkg.ResolvedSource, error)
}

// SourceRegistry maps SourceType → SourceResolver and dispatches
// resolution calls. The composition root registers one resolver
// per type, then calls Freeze(). After freezing, Register is
// rejected. Run holds a read lock during resolution so frozen
// registries are safe for concurrent use.
type SourceRegistry struct {
	resolvers map[scriptpkg.SourceType]SourceResolver
	frozen    bool
	mu        sync.RWMutex
	log       *zap.Logger
}

// NewSourceRegistry creates an empty, unfrozen SourceRegistry.
// log may be nil — warnings are silently dropped.
func NewSourceRegistry(log *zap.Logger) *SourceRegistry {
	return &SourceRegistry{
		resolvers: make(map[scriptpkg.SourceType]SourceResolver),
		log:       log,
	}
}

// Register associates a resolver with a source type.
//
// Registration rules (fail-closed):
//   - nil registry → false
//   - frozen registry → false (warns if log is non-nil)
//   - nil resolver → false (warns)
//   - duplicate type → false (warns; unlike the old behaviour that
//     silently overwrote)
//
// Returns true only when the resolver was successfully registered.
func (r *SourceRegistry) Register(t scriptpkg.SourceType, resolver SourceResolver) bool {
	if r == nil {
		return false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		if r.log != nil {
			r.log.Warn("source registry: Register called after Freeze — rejected",
				zap.String("source_type", string(t)))
		}
		return false
	}

	if resolver == nil {
		if r.log != nil {
			r.log.Warn("source registry: nil resolver rejected",
				zap.String("source_type", string(t)))
		}
		return false
	}

	if _, exists := r.resolvers[t]; exists {
		if r.log != nil {
			r.log.Warn("source registry: duplicate registration rejected",
				zap.String("source_type", string(t)))
		}
		return false
	}

	r.resolvers[t] = resolver
	return true
}

// Freeze locks the registry so no further registrations are
// accepted. Idempotent — calling Freeze multiple times is a no-op.
// After freezing the registry is safe for concurrent Resolve calls.
func (r *SourceRegistry) Freeze() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
}

// IsFrozen reports whether the registry has been frozen.
func (r *SourceRegistry) IsFrozen() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

// Len returns the number of registered resolvers.
func (r *SourceRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.resolvers)
}

// Registered reports whether a resolver is registered for the
// given source type. Does not consider frozen state.
func (r *SourceRegistry) Registered(t scriptpkg.SourceType) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.resolvers[t]
	return ok
}

// Resolve dispatches the SourceSpec to the registered resolver for
// its Type. Returns a SourceResolutionError if no resolver is
// registered for that type.
//
// Thread-safe: holds a read lock while dispatching. The resolver's
// Resolve method runs outside any lock.
func (r *SourceRegistry) Resolve(ctx context.Context, src scriptpkg.SourceSpec, itemID string) (*scriptpkg.ResolvedSource, error) {
	if r == nil {
		return nil, fmt.Errorf("source registry: not initialized")
	}

	r.mu.RLock()
	resolver, ok := r.resolvers[src.Type]
	r.mu.RUnlock()

	if !ok || resolver == nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  src.Type,
			Query:       src.Query,
			ResultCount: 0,
			Inner:       fmt.Errorf("no resolver registered for source type %q", src.Type),
		}
	}
	return resolver.Resolve(ctx, src, itemID)
}
