// Package scriptgeneration — vidrush_pipeline.go owns the composition-time
// seam for the incremental VidRush pipeline. The Runner builds a fresh,
// run-scoped VidRushIncrementalCoordinator from these immutable dependencies
// for each run, so generation and VidRush enrichment overlap without sharing
// run-scoped coordinator state across runs.
package scriptgeneration

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
//
// Fase 1-5 semantic cutover (big-bang): the legacy Enricher/ProviderResolver
// are replaced by SceneIRSegmentEnricher + SemanticProviderResolver, wired
// through the new VisualNERPort/MediaSamplerPort/LocalStockResolverPort. The
// barrier is wrapped by MediaCertBarrier so a CERTIFIED=false run fails the
// job. The legacy Enricher/ProviderResolver fields remain for composition
// roots that have not yet wired the new ports; when the new ports are set
// they take precedence (see Runner.beginVidRush).
type VidRushPipeline struct {
	// Enricher converts one stable scene into a VidRushSegmentResult. It is
	// the single-segment owner of extraction/query/cache work. Legacy field;
	// when NERPort is set, SceneIRSegmentEnricher replaces this.
	Enricher SegmentEnricher
	// ProviderResolver fans out the enriched segment's visual provider
	// searches (Artlist, internet images) after entity extraction. A nil
	// resolver leaves enrichment at the entities+queries stage. Legacy
	// field; when StockResolverPort + SamplerPort are set,
	// SemanticProviderResolver replaces this.
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

	// ── Fase 1-5 semantic chain ports (big-bang cutover) ───────────────

	// NERPort is the VisualNER Rust crate adapter (Fase 3). When set, the
	// pipeline builds a SceneIRSegmentEnricher that compiles a SceneIR
	// (Fase 1) and extracts source-grounded entities via this port.
	NERPort VisualNERPort
	// StockResolverPort is the LOCAL FIRST PROVIDER SECOND resolver
	// (Fase 5). When set (with SamplerPort), the pipeline builds a
	// SemanticProviderResolver that consults local Qdrant/SQLite first.
	StockResolverPort LocalStockResolverPort
	// SamplerPort is the MediaSampler Rust crate adapter (Fase 4). When
	// set (with StockResolverPort), the SemanticProviderResolver ranks
	// candidates via this port.
	SamplerPort MediaSamplerPort
	// CertifierPort is the MediaCert certifier (Fase 2). When set (with
	// CertSpec), the coordinator's barrier is wrapped by MediaCertBarrier
	// so a CERTIFIED=false run fails the job.
	CertifierPort MediaCertifierPort
	// CertSpec is the certification spec the MediaCertBarrier certifies
	// against. In production this is the golden Mediterranean fixture spec.
	CertSpec mediacert.Spec
}
