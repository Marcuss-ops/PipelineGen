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
// per type; the registry validates the mapping at registration time.
type SourceRegistry struct {
	resolvers map[scriptpkg.SourceType]SourceResolver
	log       *zap.Logger
}

// NewSourceRegistry creates an empty SourceRegistry.
// log may be nil — warnings are silently dropped.
func NewSourceRegistry(log *zap.Logger) *SourceRegistry {
	return &SourceRegistry{
		resolvers: make(map[scriptpkg.SourceType]SourceResolver),
		log:       log,
	}
}

// Register associates a resolver with a source type. Overwrites
// any previously registered resolver for that type. Logs a warning
// and returns false when resolver is nil (composition mistake
// caught at wiring time).
func (r *SourceRegistry) Register(t scriptpkg.SourceType, resolver SourceResolver) bool {
	if r == nil {
		return false
	}
	if resolver == nil {
		if r.log != nil {
			r.log.Warn("source registry: nil resolver registered (no-op)",
				zap.String("source_type", string(t)))
		}
		return false
	}
	r.resolvers[t] = resolver
	return true
}

// Resolve dispatches the SourceSpec to the registered resolver for
// its Type. Returns ErrNoSource if no resolver is registered for
// that type.
func (r *SourceRegistry) Resolve(ctx context.Context, src scriptpkg.SourceSpec, itemID string) (*scriptpkg.ResolvedSource, error) {
	if r == nil {
		return nil, fmt.Errorf("source registry: not initialized")
	}
	resolver, ok := r.resolvers[src.Type]
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
