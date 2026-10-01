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
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
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

func (r *SemanticAndFanoutResolver) ResolveProviders(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	if _, stockBound := scriptpkg.StockBindingForSegment(plan, nil, segment); stockBound {
		// A direct stock binding is the scene's authoritative visual source.
		// Neither the local-first semantic probe nor the provider fanout may
		// search for it. The gate only rewrites Cache strings (struct values),
		// so a shallow struct copy is a sufficient isolated result here.
		cloned := segment
		cloned.Cache.Artlist = "BYPASSED"
		cloned.Cache.InternetImages = "BYPASSED"
		cloned.Cache.YouTube = "BYPASSED"
		cloned.Cache.Binding = "STOCK_BOUND"
		return cloned, nil
	}
	updated, err := r.semantic.ResolveProviders(ctx, plan, segment)
	if err != nil {
		return segment, err
	}
	if plan == nil {
		return updated, nil
	}
	fanoutPlan := *plan
	fanoutPlan.MediaPlan = plan.MediaPlan.Clone()
	// Artlist was already handled by the semantic resolver. Prevent a second
	// live search while preserving the caller's image/generation policy.
	fanoutPlan.MediaPlan.ProviderPolicy.Artlist = mediadomain.MediaToggleDisabled
	return r.fanout.ResolveProviders(ctx, &fanoutPlan, updated)
}

// NewSemanticProviderResolver wires the new resolver. Both ports must be non-nil.
func NewSemanticProviderResolver(stockResolver LocalStockResolverPort, samplerPort scriptports.MediaSamplerPort) (*SemanticProviderResolver, error) {
	if stockResolver == nil {
		return nil, fmt.Errorf("scriptgeneration: LocalStockResolverPort is required for SemanticProviderResolver")
	}
	if samplerPort == nil {
		return nil, fmt.Errorf("scriptgeneration: MediaSamplerPort is required for SemanticProviderResolver")
	}
	return &SemanticProviderResolver{stockResolver: stockResolver, samplerPort: samplerPort}, nil
}

// ResolveProviders resolves candidates LOCAL FIRST and ranks them via the
// MediaSampler. It is the Fase 4 + Fase 5 replacement for the legacy chooser.
func (r *SemanticProviderResolver) ResolveProviders(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	// The stock resolver owns video discovery and must never bypass the plan's
	// provider policy. Image-only/generation-only plans still use the later
	// image materialization stages, but must not probe local video stock or
	// fall back to Artlist.
	if plan == nil || !plan.MediaPlan.ProviderPolicy.Artlist.AsBool() {
		segment.Cache.InternetImagesProviderSearches = 0
		return segment, nil
	}
	subject := ""
	terms := []string{}
	if segment.Insights.VisualProfile != nil {
		subject = segment.Insights.VisualProfile.Subject
		terms = segment.Insights.VisualProfile.Terms
	}
	query := subject
	if query == "" && len(terms) > 0 {
		query = terms[0]
	}

	stockReq := stockintelligence.ResolveRequest{
		SegmentID:   segment.SegmentID,
		Subject:     subject,
		VisualTerms: terms,
		Query:       query,
	}
	stockRes, err := r.stockResolver.Resolve(ctx, stockReq)
	if err != nil {
		return segment, fmt.Errorf("stockintelligence resolve: %w", err)
	}

	samplerCands := make([]scriptpkg.SegmentAssetCandidate, 0, len(stockRes.Candidates))
	for _, c := range stockRes.Candidates {
		samplerCands = append(samplerCands, scriptpkg.SegmentAssetCandidate{
			AssetID: c.AssetID, Entity: c.Label, RelevanceScore: float64(c.GenericSimilarity), SegmentID: c.OwnerSegmentID,
		})
	}
	winnerID, err := r.samplerPort.Sample(ctx, segment.SegmentID, subject, terms, samplerCands, false)
	if err != nil {
		return segment, fmt.Errorf("mediasampler sample: %w", err)
	}

	if winnerID != "" {
		primary := scriptpkg.SegmentAssetCandidate{
			SegmentID: segment.SegmentID,
			AssetID:   winnerID,
			Provider:  scriptpkg.VidRushProviderArtlist,
			Score:     0.9,
		}
		for _, c := range stockRes.Candidates {
			if c.AssetID == winnerID {
				primary.Entity = c.Label
				primary.Query = query
				primary.RelevanceScore = float64(c.GenericSimilarity)
				break
			}
		}
		segment.Assets.PrimaryVideo = &primary
	}
	// Record the provider live request count on the segment's cache state so
	// MediaCert can assert the LOCAL FIRST PROVIDER SECOND invariant.
	segment.Cache.InternetImagesProviderSearches = stockRes.ProviderLiveRequests
	return segment, nil
}

// SegmentMaterializer acquires, verifies and finalizes the candidate assets
// of one enriched segment through the shared provider registry and common
// finalizer. It runs after provider search and returns the segment with its
// candidates persisted. Implementations must return an immutable result and
// must never mutate shared scene state.
type SegmentResearcher interface {
	ResearchSegment(context.Context, *scriptpkg.ResolvedGenerationPlan, scriptpkg.VidRushSegmentResult) (*scriptpkg.ResearchReport, error)
}

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

// SegmentProviderResolver fans out a single enriched segment's visual provider
// searches. It receives the segment with entities and retrieval queries already
// resolved and returns the segment with candidate assets merged. Implementations
// must dispatch through the shared provider registry and must not duplicate
// per-provider orchestration (search, rate limiting, retry).
type SegmentProviderResolver interface {
	ResolveProviders(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error)
}
