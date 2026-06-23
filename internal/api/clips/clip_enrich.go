package clips

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// enrichAndIndexClip runs the full enrichment pipeline in background with a 3-minute timeout:
//  1. LLM semantic tagger → search_text, tags, subjects
//  2. Clip indexer → embedding computation
//  3. Vector store (Qdrant) upsert
func (h *Handler) EnrichAndIndexClip(ctx context.Context, clip *asset.Asset, source string) {
	// Apply a 3-minute timeout to prevent runaway goroutines
	enrichCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	h.log.Info("starting enrichment for clip",
		zap.String("clip_id", clip.ID),
		zap.String("source", source))

	// Step 1: Semantic enrichment via MetadataWriter (LLM-generated tags/description)
	if h.metaWriter != nil && clip.Name != "" {
		prompt := clip.Name
		if clip.Category != "" {
			prompt = clip.Category + ": " + prompt
		}

		payload, _, err := h.metaWriter.GeneratePayload(enrichCtx, semantic.WriteRequest{
			AssetID:   clip.ID,
			AssetType: "clip",
			MediaType: string(clip.MediaType),
			Source:    source,
			Generator: "api_create",
			Style:     clip.Category,
			Prompt:    prompt,
		})
		if err != nil {
			h.log.Warn("semantic enrichment failed for clip",
				zap.String("clip_id", clip.ID), zap.Error(err))
		} else if payload != nil {
			// Update clip with enriched metadata
			if payload.SearchText != "" {
				clip.SearchText = payload.SearchText
			}
			if len(payload.Tags) > 0 {
				clip.Tags = append(clip.Tags, payload.Tags...)
			}
			if payload.SemanticDescription != "" {
				// Preserve existing metadata and add enriched fields
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

			// Persist enriched metadata to DB (so clipIndexer can read it when computing embeddings)
			if h.assetRepo != nil {
				if err := h.assetRepo.Upsert(enrichCtx, clip); err != nil {
					h.log.Warn("failed to persist enriched clip metadata",
						zap.String("clip_id", clip.ID), zap.Error(err))
				}
			}
		}
	}

	// Step 2: Clip indexer — generates search_text + embedding + auto-upserts to vector store
	if h.clipIndexer != nil && h.clipIndexer.IsEnabled() {
		if err := h.clipIndexer.IndexClip(enrichCtx, clip.ID); err != nil {
			h.log.Warn("clip indexer failed for clip",
				zap.String("clip_id", clip.ID), zap.Error(err))
		}
	} else if h.vectorStore != nil && clip.SearchText != "" {
		// Step 3: Direct vector store upsert (fallback if clipIndexer not available)
		asset := qdrant.VectorAsset{
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
		if err := h.vectorStore.UpsertAsset(enrichCtx, asset); err != nil {
			h.log.Warn("vector store upsert failed for clip",
				zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	h.log.Info("enrichment complete for clip", zap.String("clip_id", clip.ID))
}

// EnrichMedia triggers semantic enrichment + embedding for any media asset.
// Works with ALL media types (image, clip, stock, artlist, youtube, audio, voiceover).
// If search_text is missing, calls the semantic tagger to generate it.
// Then generates embedding via the Python embedding server, persists to DB.
// Finally upserts to Qdrant vector store.
//
// Usage:
//
//	POST /api/artlist/enrich    — uses path param :source from route
//	POST /api/enrich            — uses source from JSON body
func (h *Handler) EnrichMedia(c *gin.Context) {
	var req struct {
		AssetID      string `json:"asset_id"`
		Source       string `json:"source"`
		SkipQdrant   bool   `json:"skip_qdrant"`
		SkipEmbedGen bool   `json:"skip_embed_gen"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if req.AssetID == "" {
		apiutil.BadRequest(c, "asset_id is required")
		return
	}

	// Fallback: use path param :source if body doesn't have source
	if req.Source == "" {
		req.Source = c.Param("source")
	}

	ctx := c.Request.Context()
	h.log.Info("enriching media asset",
		zap.String("asset_id", req.AssetID),
		zap.String("source", req.Source),
		zap.Bool("skip_qdrant", req.SkipQdrant),
	)

	// Try to find and enrich via clip indexer first (works for clips/stock/artlist/youtube)
	if req.Source != "" && (h.clipIndexer != nil || h.vectorStore != nil) {
		repo := h.repoForSource(req.Source)
		if repo != nil {
			clip, err := repo.GetClip(ctx, req.AssetID)
			if err == nil && clip != nil {
				// Clip found — use existing enrichment pipeline (async, survives handler return)
				concurrent.SafeGo("media-enrich", func() {
					h.EnrichAndIndexClip(context.WithoutCancel(ctx), clip, req.Source)
				})
				apiutil.OK(c, gin.H{
					"ok":       true,
					"action":   "enqueued",
					"asset_id": req.AssetID,
					"source":   req.Source,
					"method":   "clip_enrichment_pipeline",
					"message":  "enrichment started in background",
				})
				return
			}
		}
	}

	// Fallback: try to index via clip indexer (generates embedding + upserts to Qdrant)
	if h.clipIndexer != nil && h.clipIndexer.IsEnabled() && !req.SkipEmbedGen {
		concurrent.SafeGo("clip-indexer-fallback", func() {
			indexCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			if err := h.clipIndexer.IndexClip(indexCtx, req.AssetID); err != nil {
				h.log.Warn("clip indexer fallback failed",
					zap.String("asset_id", req.AssetID), zap.Error(err))
			}
		})
		apiutil.OK(c, gin.H{
			"ok":       true,
			"action":   "enqueued",
			"asset_id": req.AssetID,
			"method":   "clip_indexer_fallback",
			"message":  "embedding generation + vector store upsert started in background",
		})
		return
	}

	apiutil.OK(c, gin.H{
		"ok":       true,
		"action":   "accepted",
		"asset_id": req.AssetID,
		"message":  "enrichment pipeline not fully available — use the Python script for full processing",
		"hint":     "POST /api/enrich with asset_id to trigger the Go enrichment pipeline",
	})
}

// ReindexClip triggers re-indexing of an existing clip (semantic enrichment + vector store).
// Useful after manually creating/updating a clip to make it searchable.
func (h *Handler) ReindexClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()

	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Run semantic enrichment first if search_text is empty but we have a name
	enrichNeeded := clip.SearchText == "" && clip.Name != "" && h.metaWriter != nil
	if enrichNeeded {
		concurrent.SafeGo("reindex-enrich", func() {
			h.EnrichAndIndexClip(context.WithoutCancel(ctx), clip, source)
		})
		apiutil.OK(c, gin.H{
			"ok":      true,
			"action":  "enqueued",
			"clip_id": clipID,
			"method":  "async_enrich+index",
			"message": "enrichment + indexing started in background",
		})
		return
	}

	if h.clipIndexer != nil && h.clipIndexer.IsEnabled() {
		// Full pipeline: indexer generates embedding + upserts to vector store
		if err := h.clipIndexer.IndexClip(ctx, clipID); err != nil {
			apiutil.InternalError(c, fmt.Errorf("index failed: %w", err))
			return
		}
		apiutil.OK(c, gin.H{
			"ok":      true,
			"action":  "reindexed",
			"clip_id": clipID,
			"method":  "clip_indexer",
		})
		return
	}

	// Fallback: direct vector store upsert if we have search_text
	if h.vectorStore != nil && clip.SearchText != "" {
		asset := qdrant.VectorAsset{
			AssetID:    clip.ID,
			Source:     string(clip.Source),
			Name:       clip.Name,
			LocalPath:  clip.LocalPath(),
			DriveLink:  clip.DriveLink(),
			Category:   clip.Category,
			MediaType:  string(clip.MediaType),
			SearchText: clip.SearchText,
			Tags:       clip.Tags,
		}
		if err := h.vectorStore.UpsertAsset(ctx, asset); err != nil {
			apiutil.InternalError(c, fmt.Errorf("vector upsert failed: %w", err))
			return
		}
		apiutil.OK(c, gin.H{
			"ok":      true,
			"action":  "reindexed",
			"clip_id": clipID,
			"method":  "direct_vector_upsert",
		})
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "skipped",
		"clip_id": clipID,
		"reason":  "no indexer or vector store configured, and no search_text available",
	})
}

// BatchReindex finds all assets missing embeddings and re-indexes them via the job system.
// Returns immediately with a job_id that can be polled via GET /api/jobs/:id/full.
//
// POST /api/media/enrich/batch
// Body: {"source": "artlist", "media_type": "video", "limit": 100}
func (h *Handler) BatchReindex(c *gin.Context) {
	var req struct {
		Source    string `json:"source"`
		MediaType string `json:"media_type"`
		Limit     int    `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if h.clipIndexer == nil || !h.clipIndexer.IsEnabled() {
		apiutil.InternalError(c, fmt.Errorf("clip indexer not available"))
		return
	}

	// Enqueue as a job so callers can poll progress via GET /api/jobs/:id/full
	if h.jobsSvc != nil {
		job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type: "media.reindex",
			Payload: map[string]any{
				"source":     req.Source,
				"media_type": req.MediaType,
				"limit":      req.Limit,
			},
			ActiveKey: fmt.Sprintf("batch_reindex_%s_%s", req.Source, req.MediaType),
		})
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		apiutil.OK(c, gin.H{
			"ok":         true,
			"action":     "batch_reindex_enqueued",
			"job_id":     job.ID,
			"status_url": "/api/jobs/" + job.ID + "/full",
			"message":    "Batch reindex job enqueued",
		})
		return
	}

	// Fallback: fire-and-forget goroutine if jobs service not available
	ctx := c.Request.Context()
	result, err := h.clipIndexer.BatchReindex(ctx, req.Source, req.MediaType, req.Limit)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "batch_reindex_started",
		"total":   result.Total,
		"message": fmt.Sprintf("%d assets queued for re-indexing (background)", result.Total),
	})
}
