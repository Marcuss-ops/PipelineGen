package clips

import (
	"context"
	"fmt"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// enrichAndIndexClip runs the full enrichment pipeline in background with a 3-minute timeout:
//  1. LLM semantic tagger → search_text, tags, subjects
//  2. Clip indexer → embedding computation
//  3. Vector store (Qdrant) upsert
func (h *Handler) EnrichAndIndexClip(ctx context.Context, clip *asset.Asset, source string) {
	h.enrichUC.EnrichAndIndex(ctx, clip, source)
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
	// PG-034 (June 2026): SkipQdrant field removed — Qdrant capability
	// deleted. The clip indexer is the canonical semantic-search backend.
	var req struct {
		AssetID      string `json:"asset_id"`
		Source       string `json:"source"`
		SkipEmbedGen bool   `json:"skip_embed_gen"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	// Fallback: use path param :source if body doesn't have source
	if req.Source == "" {
		req.Source = c.Param("source")
	}

	h.log.Info("enriching media asset",
		zap.String("asset_id", req.AssetID),
		zap.String("source", req.Source),
	)

	result, err := h.enrichUC.EnrichMedia(c.Request.Context(), appclips.EnrichMediaRequest{
		AssetID:      req.AssetID,
		Source:       req.Source,
		SkipEmbedGen: req.SkipEmbedGen,
	}, func(source string) appclips.ClipFinder {
		repo := h.repoForSource(source)
		if repo == nil {
			return nil
		}
		return repo
	})
	if err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	apiutil.OK(c, gin.H{
		"ok":       true,
		"action":   result.Action,
		"asset_id": result.AssetID,
		"source":   result.Source,
		"method":   result.Method,
		"message":  result.Message,
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

	// PG-034 (June 2026): direct-vector-store fallback removed — Qdrant
	// capability deleted. The clip indexer is the canonical
	// semantic-search backend.

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "skipped",
		"clip_id": clipID,
		"reason":  "no indexer configured and no search_text available",
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
