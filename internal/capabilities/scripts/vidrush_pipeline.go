// Package scriptgeneration — vidrush_pipeline.go owns the composition-time
// seam for the incremental VidRush pipeline. The Runner builds a fresh,
// run-scoped VidRushIncrementalCoordinator from these immutable dependencies
// for each run, so generation and VidRush enrichment overlap without sharing
// run-scoped coordinator state across runs.
package scriptgeneration

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// VidRushPlanResolver resolves the per-run ResolvedGenerationPlan consumed by
// the incremental VidRush coordinator. The plan carries the caller's language,
// title, segments, and media policy (provider toggles, extraction limits), so
// per-scene enrichment and provider fan-out run with the same contract as the
// batch flow.
type VidRushPlanResolver interface {
	ResolveVidRushPlan(ctx context.Context, req GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error)
}

// VidRushPlanResolverFunc adapts a plain function to VidRushPlanResolver.
type VidRushPlanResolverFunc func(ctx context.Context, req GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error)

// ResolveVidRushPlan implements VidRushPlanResolver.
func (f VidRushPlanResolverFunc) ResolveVidRushPlan(ctx context.Context, req GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
	return f(ctx, req)
}

// VidRushPipeline bundles the composition-time dependencies the Runner needs
// to construct a run-scoped coordinator. It holds only immutable dependencies,
// never the coordinator itself, so the Runner stays reusable across runs.
type VidRushPipeline struct {
	// Enricher converts one stable scene into a VidRushSegmentResult. It is
	// the single-segment owner of extraction/query/cache work.
	Enricher SegmentEnricher
	// ProviderResolver fans out the enriched segment's visual provider
	// searches (Artlist, internet images) after entity extraction. A nil
	// resolver leaves enrichment at the entities+queries stage.
	ProviderResolver SegmentProviderResolver
	// Materializer acquires/verifies/finalizes candidates after provider
	// search. A nil materializer leaves enrichment at the search stage.
	Materializer SegmentMaterializer
	// Metrics records bounded per-scene pipeline events and per-run overlap.
	Metrics VidRushMetrics
	// PlanResolver resolves the per-run plan. Required when Enricher is set.
	PlanResolver VidRushPlanResolver
	// Backpressure bounds each stage independently. Zero values use the
	// canonical defaults (extraction single-slot, search 4, materialize 2).
	Backpressure VidRushBackpressure
}
