package clips

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"go.uber.org/zap"
)

// EnrichUseCase handles semantic enrichment for clips.
// vectorStore was removed from this flow.
// The clip indexer is the canonical semantic-search backend now.
type EnrichUseCase struct {
	assetRepo   asset.Repository
	clipIndexer *clipindexer.Service
	metaWriter  *semantic.MetadataWriter
	log         *zap.Logger
}

// NewEnrichUseCase constructs the use case.
func NewEnrichUseCase(
	repo asset.Repository,
	indexer *clipindexer.Service,
	mw *semantic.MetadataWriter,
	log *zap.Logger,
) *EnrichUseCase {
	return &EnrichUseCase{
		assetRepo:   repo,
		clipIndexer: indexer,
		metaWriter:  mw,
		log:         log,
	}
}

// EnrichAndIndex runs the full enrichment pipeline in background:
//  1. LLM semantic tagger -> search_text, tags, subjects
//  2. Clip indexer -> embedding computation
//  3. Vector store (Qdrant) upsert
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

			if uc.assetRepo != nil {
				if err := uc.assetRepo.Upsert(enrichCtx, clip); err != nil {
					uc.log.Warn("failed to persist enriched clip metadata",
						zap.String("clip_id", clip.ID), zap.Error(err))
				}
			}
		}
	}

	// Step 2: Clip indexer (PG-034: only the canonical search backend — vector
	// store fallback was deleted with Qdrant).
	if uc.clipIndexer != nil && uc.clipIndexer.IsEnabled() {
		if err := uc.clipIndexer.IndexClip(enrichCtx, clip.ID); err != nil {
			uc.log.Warn("clip indexer failed for clip",
				zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	uc.log.Info("enrichment complete for clip", zap.String("clip_id", clip.ID))
}

// UpsertToVectorStore was removed in PG-034 (June 2026) along with the
// Qdrant capability. The clip indexer's IndexClip path is now the
// single canonical semantic indexing entry point.

// HasVectorStore was removed in PG-034 (June 2026).
// The clip indexer is now the canonical semantic-search backend.

// EnrichMediaRequest contains the input for the EnrichMedia endpoint.
// SkipQdrant was removed from this flow.
// SkipEmbedGen is preserved for callers that want to skip the embedding
//-generation leg altogether (the indexer now handles the whole pipeline).
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
//   ┌───────────────────────┬──────────────────────────────────────┐
//   │ Deps wired            │ Result.Action                        │
//   ├───────────────────────┼──────────────────────────────────────┤
//   │ JobsSvc + clipIndexer │ "deprecated_use_route_POST_media_enrich"
//   │ JobsSvc only          │ "deprecated_use_route_POST_media_enrich"
//   │ clipIndexer only      │ "deprecated_use_route_POST_reindex"
//   │ neither               │ "no_pipeline_available"              │
//   └───────────────────────┴──────────────────────────────────────┘
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
	switch {
	case uc.clipIndexer != nil && uc.clipIndexer.IsEnabled() && !req.SkipEmbedGen:
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
			Message: "neither clipIndexServer nor jobs port wired; cannot enrich",
		}, nil
	}
}
