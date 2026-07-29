// Package nonops — handler_index.go: ReindexClip + BatchReindex
// endpoints extracted from clips/handler_index.go per
// PR-CLIPS-NONOPS-EXTRACT (July 2026). The method bodies are
// byte-equivalent with the pre-extraction versions — only the
// receiver type changed from *Handler to *NonOpsHandler, and
// h.repoForSource now delegates to the nonops callback (wired by
// the parent as `h.repoForSource`).
//
// The enqueueRequest type alias is moved here from clips/handler.go
// (godlike/07 minimum-blast-radius: code directly orphaned by the
// extraction is removed in the same commit).
package nonops

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ── ReindexClip, BatchReindex, and EnrichMedia endpoints use
// kerneljob.EnqueueRequest (the canonical kernel type) directly.
// The `type enqueueRequest` alias was inlined per P1 legacy cleanup.

// ReindexClip triggers re-indexing of an existing clip (semantic
// enrichment + vector store).
func (h *NonOpsHandler) ReindexClip(c *gin.Context) {
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

	enrichNeeded := clip.SearchText == "" && clip.Name != "" && h.enrichUC != nil
	if enrichNeeded {
		if h.jobsSvc == nil {
			apiutil.Error(c, http.StatusServiceUnavailable,
				"reindex requires the jobs service (S1a removed the in-process SafeGo fallback); wire jobsSvc to use reindex")
			return
		}
		job, err := h.jobsSvc.Enqueue(ctx, &kerneljob.EnqueueRequest{
			Type: "media.enrich",
			Payload: map[string]any{
				"asset_id": clipID,
				"source":   source,
			},
			ActiveKey: "enrich_clip_" + clipID,
		})
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to enqueue media.enrich job: %w", err))
			return
		}
		apiutil.OK(c, gin.H{
			"ok":         true,
			"action":     "enqueued",
			"job_id":     job.ID,
			"status_url": "/api/jobs/" + job.ID + "/full",
			"clip_id":    clipID,
			"method":     "async_enrich+index_via_jobs",
			"message":    "enrichment + indexing dispatched to jobs system (worker will run)",
		})
		return
	}

	if h.clipIndexer != nil && h.clipIndexer.IsEnabled() {
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

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "skipped",
		"clip_id": clipID,
		"reason":  "no indexer configured and no search_text available",
	})
}

// BatchReindex finds all assets missing embeddings and re-indexes
// them via the job system (or synchronously when jobsSvc is nil).
func (h *NonOpsHandler) BatchReindex(c *gin.Context) {
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

	if h.jobsSvc != nil {
		job, err := h.jobsSvc.Enqueue(c.Request.Context(), &kerneljob.EnqueueRequest{
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

	// Fallback: synchronous call when jobs service not available.
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
