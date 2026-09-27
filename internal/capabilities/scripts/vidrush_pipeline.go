// Package scriptgeneration — vidrush_pipeline.go owns the composition-time
// seam for the incremental VidRush pipeline and the VidRush observability
// contract. The Runner builds a fresh, run-scoped VidRushIncrementalCoordinator
// from these immutable dependencies for each run, so generation and VidRush
// enrichment overlap without sharing run-scoped coordinator state across runs.
// The metrics port is the bounded per-scene surface consumed by the
// coordinator, plus the per-run timing contract (generation vs VidRush overlap)
// used to prove that enrichment overlaps generation instead of running after it.
package scriptgeneration

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
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
// Fase 1-5 semantic cutover (big-bang): SceneIRSegmentEnricher and
// SemanticProviderResolver are wired through the new ports. The barrier is
// wrapped by MediaCertBarrier so a CERTIFIED=false run fails the job.
type VidRushPipeline struct {
	// ProviderResolver fans out the enriched segment's visual provider
	// searches (Artlist, internet images) after entity extraction. A nil
	// resolver leaves enrichment at the entities+queries stage. When
	// StockResolverPort + SamplerPort are set, SemanticProviderResolver is
	// composed with this fan-out resolver.
	ProviderResolver SegmentProviderResolver
	// Materializer acquires/verifies/finalizes candidates after provider
	// search. A nil materializer leaves enrichment at the search stage.
	Materializer SegmentMaterializer
	// Metrics records bounded per-scene pipeline events and per-run overlap.
	Metrics VidRushMetrics
	// PlanResolver resolves the per-run plan. Required when NERPort is wired.
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
	SamplerPort scriptports.MediaSamplerPort
	// CertifierPort is the MediaCert certifier (Fase 2). When set (with
	// CertSpec), the coordinator's barrier is wrapped by MediaCertBarrier
	// so a CERTIFIED=false run fails the job.
	CertifierPort MediaCertifierPort
	// CertSpecResolver derives the per-run contract after PlanResolver has
	// produced the canonical segment list. It takes precedence over CertSpec.
	CertSpecResolver MediaCertSpecResolver
	// CertSpec is the certification spec the MediaCertBarrier certifies
	// against. In production this is the golden Mediterranean fixture spec.
	CertSpec mediacert.Spec
}

// VidRushMetrics records bounded per-scene pipeline events for the incremental
// VidRush coordinator. Dynamic identifiers (run id, scene id, segment id,
// asset id) belong in structured logs, never in metric labels, so this port
// carries no labels.
type VidRushMetrics interface {
	// SceneCommitted records one stable scene committed by the runner.
	SceneCommitted()
	// EnrichmentStarted records one scene enrichment beginning.
	EnrichmentStarted()
	// EnrichmentCompleted records one scene enrichment finishing, with the
	// wall-clock duration of that scene's enrichment.
	EnrichmentCompleted(duration time.Duration)
	// BarrierWait records the wall-clock time the final barrier spent waiting
	// for still-running enrichments.
	BarrierWait(seconds float64)
	// GenerationOverlap records the wall-clock overlap between scene
	// generation and VidRush enrichment for the run. overlap > 0 is the
	// success signal for the incremental pipeline.
	GenerationOverlap(seconds float64)
	// StaleResult records one enrichment result discarded by stale-result
	// fencing (superseded text hash or revision).
	StaleResult()
}

// VidRushRunTimings is the per-run wall-clock timing contract. A positive
// OverlapMS proves enrichment began before generation finished — the success
// signal for the incremental VidRush pipeline.
type VidRushRunTimings struct {
	// GenerationTotalMS is the total wall-clock time spent generating scene
	// text (from generation start to the last scene commit).
	GenerationTotalMS int64 `json:"generation_total_ms"`
	// VidRushTotalMS is the total wall-clock time from the first enrichment
	// start to the final barrier completion.
	VidRushTotalMS int64 `json:"vidrush_total_ms"`
	// BarrierWaitMS is the wall-clock time the final barrier waited for
	// still-running enrichments.
	BarrierWaitMS int64 `json:"vidrush_barrier_wait_ms"`
	// OverlapMS is the wall-clock overlap between generation and VidRush
	// enrichment. It is zero when enrichment only starts after generation has
	// fully completed (the sequential-block anti-pattern).
	OverlapMS int64 `json:"overlap_ms"`
}

// OverlapAchieved reports whether the run achieved incremental overlap, i.e.
// VidRush enrichment began before scene generation finished.
func (t VidRushRunTimings) OverlapAchieved() bool {
	return t.OverlapMS > 0
}

// VidRushTimingRecorder is the seam through which the runner reports the
// scene-generation wall-clock window so the coordinator can compute the
// generation↔VidRush overlap (the success signal: overlap_ms > 0).
type VidRushTimingRecorder interface {
	// MarkGenerationStart records the moment scene-text generation began.
	MarkGenerationStart(t time.Time)
	// MarkGenerationComplete records the moment the last scene was committed
	// (generation finished emitting stable scenes).
	MarkGenerationComplete(t time.Time)
	// RunTimings returns the per-run wall-clock timing surface. OverlapMS is
	// populated once both the generation window and the first enrichment start
	// are known.
	RunTimings() VidRushRunTimings
}
