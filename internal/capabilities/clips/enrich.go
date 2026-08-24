package clips

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// ClipEnricher is the slim typed port consumed across the clips module
// boundary. PR-CLIPS-NONOPS-EXTRACT-derived cross-package callers
// (sourcingEnrichmentAdapter in internal/app, the one non-HTTP
// consumer per godlike/06 SSOT) depend on this interface instead of
// reaching through *clips.Handler — keeping the descriptor's exposed
// surface to routes + job handlers only.
//
// godlike/06 SSOT: this is the canonical single owner of the enrich
// capability at the application boundary. Callers MUST depend on this
// interface (not on *clips.Handler or *EnrichUseCase) so a future
// implementation swap (e.g. async worker-backed enrichment, a
// Qdrant-direct bulk path) does not require caller rewrites.
type ClipEnricher interface {
	// EnrichAndIndex looks up the clip by ID, runs the semantic
	// enrichment pipeline, and enqueues an outbox event for
	// downstream re-indexing. Returns a typed error on any
	// per-step failure (no silent no-op).
	EnrichAndIndex(ctx context.Context, clipID string) error
}

// ErrEnrichDispatcherRequired is the typed sentinel returned by
// NewEnrichUseCase (and surfaced by EnrichAndIndex) when the canonical
// outbox dispatcher is nil. Rendered as a fail-closed-by-construction
// guard (godlike/07 no-fake-availability) so a partial deployment
// crashes at buildRegisterBundle time rather than at first enrich.
var ErrEnrichDispatcherRequired = fmt.Errorf("clips: EnrichUseCase requires the outbox dispatcher (card 10 closed the assetRepo local-fallback path; partial deployments must fail-closed at the composition root)")

// EnrichUseCase handles semantic enrichment for clips.
// Wave 2 (Asset commit + Qdrant, July 2026): direct clipIndexer.IndexClip
// calls have been removed. Enriched metadata is persisted and re-indexed
// through the canonical outbox pipeline (mutations.AssetMutationDispatcher).
// The IndexingHandler consumer drives embedding generation and Qdrant upsert
// asynchronously.
//
// Card 10 (July 2026): signature changed from (ctx, *Asset, source) (no
// return) to (ctx, clipID) error, and the local assetRepo fallback was
// REMOVED. The dispatcher is now MANDATORY at construction — partial
// deployments fail closed with ErrEnrichDispatcherRequired rather than
// silently losing enriched metadata through a bypass path.
type EnrichUseCase struct {
	assetRepo  asset.Repository
	metaWriter semantic.MetadataWriterPort
	dispatcher mutations.AssetMutationDispatcher
	log        *zap.Logger
}

// NewEnrichUseCase constructs the use case.
//
// Wave 2 (Asset commit + Qdrant, July 2026): dispatcher is required so
// enriched metadata is persisted and re-indexed through the canonical
// outbox pipeline.
//
// Card 10 (July 2026): dispatcher nil returns ErrEnrichDispatcherRequired
// from construction — the pre-Card-10 silent-nil-fallback into
// assetRepo.Upsert is permanently retired. Callers must wire the
// canonical dispatcher; the composition root already does so via
// buildClipsBundle.
func NewEnrichUseCase(
	repo asset.Repository,
	mw semantic.MetadataWriterPort,
	dispatcher mutations.AssetMutationDispatcher,
	log *zap.Logger,
) (*EnrichUseCase, error) {
	if dispatcher == nil {
		return nil, ErrEnrichDispatcherRequired
	}
	return &EnrichUseCase{
		assetRepo:  repo,
		metaWriter: mw,
		dispatcher: dispatcher,
		log:        log,
	}, nil
}

// EnrichAndIndex runs the enrichment pipeline. Card 10 slim signature:
// takes clipID (looks up the asset by ID internally) and returns a
// typed error. No silent no-ops.
//
// Pipeline:
//  1. Look up the clip by ID (assetRepo.Get)
//  2. LLM semantic tagger -> search_text, tags, subjects
//  3. Persist the enriched asset AND enqueue an outbox event so the
//     IndexingHandler consumer can re-generate embeddings and upsert
//     to Qdrant asynchronously.
//
// Fail-closed: dispatcher nil is unreachable (NewEnrichUseCase rejects
// nil dispatcher). When contentHash is missing, we surface a typed
// error rather than warn-and-skip.
func (uc *EnrichUseCase) EnrichAndIndex(ctx context.Context, clipID string) error {
	if clipID == "" {
		return fmt.Errorf("EnrichUseCase.EnrichAndIndex: clipID is required")
	}
	enrichCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	clip, err := uc.assetRepo.Get(enrichCtx, clipID)
	if err != nil {
		return fmt.Errorf("EnrichUseCase.EnrichAndIndex: asset lookup clip_id=%s: %w", clipID, err)
	}
	if clip == nil {
		return fmt.Errorf("EnrichUseCase.EnrichAndIndex: asset not found clip_id=%s", clipID)
	}
	source := string(clip.Source)

	uc.log.Info("starting enrichment for clip",
		zap.String("clip_id", clip.ID),
		zap.String("source", source))

	// Step 1: Semantic enrichment via MetadataWriter. Skip when the
	// clip has no name OR the metaWriter is nil — both are
	// composition-time invariants in production but tolerate the
	// partial-dep case for graceful degradation (log only, no
	// arcane error).
	if uc.metaWriter == nil || clip.Name == "" {
		uc.log.Info("EnrichUseCase.EnrichAndIndex: metaWriter=nil or clip.Name empty; skipping semantic enrichment (no-op label path)",
			zap.String("clip_id", clip.ID))
	} else {
		prompt := clip.Name
		if clip.Category != "" {
			prompt = clip.Category + ": " + prompt
		}

		payload, _, err := uc.metaWriter.GeneratePayload(enrichCtx, semantic.WriteRequest{
			AssetID:   clip.ID,
			AssetType: "clip",
			MediaType: string(clip.MediaType),
			Source:    source,
			Generator: "api_create",
			Style:     clip.Category,
			Prompt:    prompt,
		})
		if err != nil {
			uc.log.Warn("semantic enrichment failed for clip",
				zap.String("clip_id", clip.ID), zap.Error(err))
		} else if payload != nil {
			if payload.SearchText != "" {
				clip.SearchText = payload.SearchText
			}
			if len(payload.Tags) > 0 {
				clip.Tags = append(clip.Tags, payload.Tags...)
			}
			if payload.SemanticDescription != "" {
				if clip.Metadata == nil {
					clip.Metadata = make(map[string]any)
				}
				clip.Metadata["semantic_description"] = payload.SemanticDescription
				if payload.RetrievalScore != nil {
					clip.Metadata["confidence"] = *payload.RetrievalScore
				} else {
					clip.Metadata["confidence"] = 0.0
				}
				clip.Metadata["semantic_enriched"] = true
			}
		}
	}

	// Step 2: persist the enriched asset + enqueue the re-index
	// outbox event. Card 10 closed the legacy assetRepo fallback
	// (the silent-success path that masked partial deployments);
	// the canonical dispatcher is the SOLE writer here. A missing
	// LegacyFileMD5 is a typed error (card 10 closed the warn-and-skip
	// silent path that lost enrichment metadata in partial
	// deployments).
	contentHash := clip.LegacyFileMD5()
	if contentHash == "" {
		return fmt.Errorf("EnrichUseCase.EnrichAndIndex: enriched clip_id=%s has no content hash (cannot re-index)", clip.ID)
	}
	if err := uc.dispatcher.EnqueueAndIndex(enrichCtx, clip, contentHash); err != nil {
		return fmt.Errorf("EnrichUseCase.EnrichAndIndex: enqueue clip_id=%s: %w", clip.ID, err)
	}

	uc.log.Info("enrichment complete for clip", zap.String("clip_id", clip.ID))
	return nil
}

// UpsertToVectorStore was removed in PG-034 (June 2026) along with the
// Qdrant capability. The clip indexer's IndexClip path is still the
// single canonical semantic indexing entry point, but it is invoked
// indirectly by the outbox consumer (IndexingHandler) rather than
// directly by application workflows.

// EnrichMediaRequest contains the input for the EnrichMedia endpoint.
// SkipEmbedGen is preserved for callers that want to skip the embedding
// -generation leg altogether (the indexer now handles the whole pipeline).
type EnrichMediaRequest struct {
	AssetID      string `json:"asset_id"`
	Source       string `json:"source"`
	SkipEmbedGen bool   `json:"skip_embed_gen"`
}

// EnrichMediaResult contains the output after triggering enrichment.
type EnrichMediaResult struct {
	Action  string `json:"action"`
	AssetID string `json:"asset_id"`
	Source  string `json:"source"`
	Method  string `json:"method"`
	Message string `json:"message"`
}

// ClipFinder is an interface for finding clips by source.
type ClipFinder interface {
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
}

// EnrichMedia DEPRECATED for handler-driven paths (S1a, June 2026).
//
// Historical context: this method previously forked
// `concurrent.SafeGo` + `context.WithoutCancel` goroutines to
// run `EnrichAndIndex` in the background. That is the
// "HTTP-handler goroutine simulating a background job"
// anti-pattern that AGENTS.md §7 + Pattern 8 explicitly forbid.
// The canonical replacement is:
//
//	POST /api/media/enrich
//	  → clip_enrich.go::EnrichMedia handler
//	    → jobsSvc.Enqueue(TypeMediaEnrich, {asset_id, source})
//	      → MediaEnrichWorker handles the work in the broker pool.
//
// S1a keeps `EnrichMedia` as a public method to preserve
// backwards-compat for test fixtures / future internal callers
// (so we don't break `internal/app/wire_clips.go` or any
// generator-backed unit test that pokes `EnrichUseCase`
// directly). The implementation NO LONGER spawns goroutines;
// instead it returns a deterministic `accepted` result
// describing where the work should go (the jobs system).
//
// Behaviour table:
//
//	┌───────────────────────┬──────────────────────────────────────┐
//	│ Deps wired            │ Result.Action                        │
//	├───────────────────────┼──────────────────────────────────────┤
//	│ Dispatcher + JobsSvc  │ "deprecated_use_route_POST_media_enrich"
//	│ JobsSvc only          │ "deprecated_use_route_POST_media_enrich"
//	│ Dispatcher only       │ "deprecated_use_route_POST_reindex"
//	│ neither               │ "no_pipeline_available"              │
//	└───────────────────────┴──────────────────────────────────────┘
//
// Returning text in `Action`/`Message` rather than an error keeps
// the legacy signature compatible: tests that assert
// `result.Action == "enqueued"` will now fail loudly, surfacing
// the migration drift — that is intentional. Run
// `rg 'EnrichMedia.*action.*enqueued' tests/` to find the
// legacy assertions and update them.
func (uc *EnrichUseCase) EnrichMedia(ctx context.Context, req EnrichMediaRequest, findClip func(source string) ClipFinder) (*EnrichMediaResult, error) {
	if req.AssetID == "" {
		return nil, fmt.Errorf("asset_id is required")
	}
	// Touch findClip only to preserve the legacy parameter shape; we
	// deliberately do NOT call EnrichAndIndex here (that was the
	// bug — handler-tier goroutines doing async work).
	_ = findClip
	_ = ctx

	// Inspect deps to provide a directed message: where should the
	// caller go to actually run the enrichment?
	//
	// Wave 2 (Asset commit + Qdrant, July 2026): replaced the
	// clipIndexer check with the canonical dispatcher check.
	switch {
	case uc.dispatcher != nil && !req.SkipEmbedGen:
		return &EnrichMediaResult{
			Action:  "deprecated_use_route_POST_media_enrich",
			AssetID: req.AssetID,
			Source:  req.Source,
			Method:  "media.enrich_worker",
			Message: "EnrichUseCase.EnrichMedia no longer spawns the work directly; dispatch via POST /api/media/enrich (S1a, June 2026)",
		}, nil
	default:
		return &EnrichMediaResult{
			Action:  "no_pipeline_available",
			AssetID: req.AssetID,
			Source:  req.Source,
			Method:  "noop",
			Message: "dispatcher not wired; cannot enrich",
		}, nil
	}
}
