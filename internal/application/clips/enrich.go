package clips

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// VectorStorePort is the port interface for vector store upserts.
// The composition root injects *qdrant.Service which satisfies this.
type VectorStorePort interface {
	UpsertAsset(ctx context.Context, asset qdrant.VectorAsset) error
}

// EnrichUseCase handles semantic enrichment and vector store indexing for clips.
type EnrichUseCase struct {
	assetRepo    asset.Repository
	clipIndexer  *clipindexer.Service
	vectorStore  VectorStorePort
	metaWriter   *semantic.MetadataWriter
	log          *zap.Logger
}

// NewEnrichUseCase constructs the use case.
func NewEnrichUseCase(
	repo asset.Repository,
	indexer *clipindexer.Service,
	vs VectorStorePort,
	mw *semantic.MetadataWriter,
	log *zap.Logger,
) *EnrichUseCase {
	return &EnrichUseCase{
		assetRepo:   repo,
		clipIndexer: indexer,
		vectorStore: vs,
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

	// Step 2: Clip indexer
	if uc.clipIndexer != nil && uc.clipIndexer.IsEnabled() {
		if err := uc.clipIndexer.IndexClip(enrichCtx, clip.ID); err != nil {
			uc.log.Warn("clip indexer failed for clip",
				zap.String("clip_id", clip.ID), zap.Error(err))
		}
	} else if uc.vectorStore != nil && clip.SearchText != "" {
		// Step 3: Direct vector store upsert (fallback)
		va := qdrant.VectorAsset{
			AssetID:    clip.ID,
			Source:     source,
			Name:       clip.Name,
			LocalPath:  clip.LocalPath(),
			DriveLink:  clip.DriveLink(),
			Category:   clip.Category,
			MediaType:  string(clip.MediaType),
			SearchText: clip.SearchText,
			Tags:       clip.Tags,
		}
		if err := uc.vectorStore.UpsertAsset(enrichCtx, va); err != nil {
			uc.log.Warn("vector store upsert failed for clip",
				zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	uc.log.Info("enrichment complete for clip", zap.String("clip_id", clip.ID))
}

// UpsertToVectorStore constructs a VectorAsset from the clip fields and
// upserts it to Qdrant. This centralises the VectorAsset mapping so
// handlers never import infrastructure/qdrant directly.
func (uc *EnrichUseCase) UpsertToVectorStore(ctx context.Context, clip *asset.Asset, source string) error {
	if uc.vectorStore == nil {
		return fmt.Errorf("vector store not configured")
	}
	va := qdrant.VectorAsset{
		AssetID:    clip.ID,
		Source:     source,
		Name:       clip.Name,
		LocalPath:  clip.LocalPath(),
		DriveLink:  clip.DriveLink(),
		Category:   clip.Category,
		MediaType:  string(clip.MediaType),
		SearchText: clip.SearchText,
		Tags:       clip.Tags,
	}
	return uc.vectorStore.UpsertAsset(ctx, va)
}

// HasVectorStore reports whether the vector store backend is configured.
func (uc *EnrichUseCase) HasVectorStore() bool {
	return uc.vectorStore != nil
}

// EnrichMediaRequest contains the input for the EnrichMedia endpoint.
type EnrichMediaRequest struct {
	AssetID      string `json:"asset_id"`
	Source       string `json:"source"`
	SkipQdrant   bool   `json:"skip_qdrant"`
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
	if req.Source != "" && (uc.clipIndexer != nil || uc.vectorStore != nil) {
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
