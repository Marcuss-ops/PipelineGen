// Package scriptgeneration — segment_enricher.go owns the reusable
// single-segment VidRush enrichment contract. The incremental coordinator and
// the non-streaming batch processor both consume this seam so the extraction,
// query construction, cache and metrics logic is implemented exactly once.
package scriptgeneration

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SegmentMaterializer acquires, verifies and finalizes the candidate assets
// of one enriched segment through the shared provider registry and common
// finalizer. It runs after provider search and returns the segment with its
// candidates persisted. Implementations must return an immutable result and
// must never mutate shared scene state.
type SegmentMaterializer interface {
	Materialize(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error)
}

// SegmentEnricher enriches one stable scene into a VidRushSegmentResult. It is
// the single reusable owner of per-segment VidRush work: entity extraction,
// important words/phrases, Artlist and image query construction, cache lookup
// and metrics. Implementations must return an immutable result and must never
// mutate shared scene state.
type SegmentEnricher interface {
	// Enrich processes a single committed scene. plan carries the resolved
	// generation context (language, model, media plan); scene is the stable
	// scene text to enrich. The returned VidRushSegmentResult is immutable and
	// keyed by the scene's content hash so stale results can be fenced out by
	// the caller.
	Enrich(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error)
}
