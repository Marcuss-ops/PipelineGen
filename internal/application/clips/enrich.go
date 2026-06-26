package clips

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// EnrichUseCase handles semantic enrichment for clips.
// vectorStore was removed from this flow.
// The clip indexer is the canonical semantic-search backend now.
type EnrichUseCase struct {
	assetRepo   asset.Repository
	clipIndexer ClipIndexerPort
	metaWriter  ClipMetaWriterPort
	log         *zap.Logger
}

// NewEnrichUseCase constructs the use case.
func NewEnrichUseCase(
	repo asset.Repository,
	indexer ClipIndexerPort,
	mw ClipMetaWriterPort,
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

		payload, _, err := uc.metaWriter.GeneratePayload(enrichCtx, ClipMetaWriteRequest{
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

// EnrichMedia triggers enrichment for a media asset.
// It tries the clip enrichment pipeline first, then falls back to clip indexer.
func (uc *EnrichUseCase) EnrichMedia(ctx context.Context, req EnrichMediaRequest, findClip func(source string) ClipFinder) (*EnrichMediaResult, error) {
	if req.AssetID == "" {
		return nil, fmt.Errorf("asset_id is required")
	}

	// Try to find and enrich via clip indexer first
	if req.Source != "" && uc.clipIndexer != nil {
		finder := findClip(req.Source)
		if finder != nil {
			clip, err := finder.GetClip(ctx, req.AssetID)
			if err == nil && clip != nil {
				concurrent.SafeGo("media-enrich", func() {
					uc.EnrichAndIndex(context.WithoutCancel(ctx), clip, req.Source)
				})
				return &EnrichMediaResult{
					Action:  "enqueued",
					AssetID: req.AssetID,
					Source:  req.Source,
					Method:  "clip_enrichment_pipeline",
					Message: "enrichment started in background",
				}, nil
			}
		}
	}

	// Fallback: try to index via clip indexer
	if uc.clipIndexer != nil && uc.clipIndexer.IsEnabled() && !req.SkipEmbedGen {
		concurrent.SafeGo("clip-indexer-fallback", func() {
			indexCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			if err := uc.clipIndexer.IndexClip(indexCtx, req.AssetID); err != nil {
				uc.log.Warn("clip indexer fallback failed",
					zap.String("asset_id", req.AssetID), zap.Error(err))
			}
		})
		return &EnrichMediaResult{
			Action:  "enqueued",
			AssetID: req.AssetID,
			Method:  "clip_indexer_fallback",
			Message: "embedding generation + vector store upsert started in background",
		}, nil
	}

	return &EnrichMediaResult{
		Action:  "accepted",
		AssetID: req.AssetID,
		Message: "enrichment pipeline not fully available",
	}, nil
}
