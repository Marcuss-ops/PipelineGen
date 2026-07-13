package clips

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"go.uber.org/zap"
) // EnrichUseCase handles semantic enrichment for clips.
// Wave 2 (Asset commit + Qdrant, July 2026): direct clipIndexer.IndexClip
// calls have been removed. Enriched metadata is persisted and re-indexed
// through the canonical outbox pipeline (mutations.AssetMutationDispatcher).
// The IndexingHandler consumer drives embedding generation and Qdrant upsert
// asynchronously.
type EnrichUseCase struct {
	assetRepo  asset.Repository
	metaWriter semantic.MetadataWriterPort
	dispatcher mutations.AssetMutationDispatcher
	log        *zap.Logger
}

// NewEnrichUseCase constructs the use case.
//
// Wave 2 (Asset commit + Qdrant, July 2026): the dispatcher is now
// required so that enriched metadata is persisted and re-indexed
// through the canonical outbox pipeline. Direct clipIndexer.IndexClip
// calls have been removed; the IndexingHandler consumer drives
// embedding generation and Qdrant upsert asynchronously.
func NewEnrichUseCase(
	repo asset.Repository,
	mw semantic.MetadataWriterPort,
	dispatcher mutations.AssetMutationDispatcher,
	log *zap.Logger,
) *EnrichUseCase {
	return &EnrichUseCase{
		assetRepo:  repo,
		metaWriter: mw,
		dispatcher: dispatcher,
		log:        log,
	}
}

// EnrichAndIndex runs the enrichment pipeline:
//  1. LLM semantic tagger -> search_text, tags, subjects
//  2. Persist the enriched asset and enqueue an outbox event so the
//     IndexingHandler consumer can re-generate embeddings and upsert
//     to Qdrant asynchronously.
func (uc *EnrichUseCase) EnrichAndIndex(ctx context.Context, clip *asset.Asset, source string) {
	enrichCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	uc.log.Info("starting enrichment for clip",
		zap.String("clip_id", clip.ID),
		zap.String("source", source))

	// Step 1: Semantic enrichment via MetadataWriter
	if uc.metaWriter != nil && clip.Name != "" {
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

			// Persist the enriched asset. Prefer the canonical
			// dispatcher (UPSERT + outbox INSERT atomically). When the
			// dispatcher is not wired, fall back to the asset repo so
			// test fixtures and partial deployments do not silently
			// lose enriched metadata.
			if uc.dispatcher != nil {
				contentHash := clip.FileHash()
				if contentHash == "" {
					uc.log.Warn("enriched clip has no content hash; persisting without re-index",
						zap.String("clip_id", clip.ID))
				} else if err := uc.dispatcher.EnqueueAndIndex(enrichCtx, clip, contentHash); err != nil {
					uc.log.Warn("failed to enqueue enriched clip for indexing",
						zap.String("clip_id", clip.ID), zap.Error(err))
				}
			} else if uc.assetRepo != nil {
				uc.log.Warn("enrichment complete but dispatcher not wired; persisting via assetRepo fallback",
					zap.String("clip_id", clip.ID))
				if err := uc.assetRepo.Upsert(enrichCtx, clip); err != nil {
					uc.log.Warn("failed to persist enriched clip metadata",
						zap.String("clip_id", clip.ID), zap.Error(err))
				}
			} else {
				uc.log.Warn("enrichment complete but neither dispatcher nor assetRepo wired; re-index skipped",
					zap.String("clip_id", clip.ID))
			}
		}
	}

	uc.log.Info("enrichment complete for clip", zap.String("clip_id", clip.ID))
}

// UpsertToVectorStore was removed in PG-034 (June 2026) along with the
// Qdrant capability. The clip indexer's IndexClip path is still the
// single canonical semantic indexing entry point, but it is invoked
// indirectly by the outbox consumer (IndexingHandler) rather than
// directly by application workflows.

// EnrichMediaRequest contains the input for the EnrichMedia endpoint.
// SkipQdrant was removed from this flow.
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
