// Package clips — handler_download.go: EnrichMedia + EnrichAndIndexClip endpoints
// extracted from handler.go per PG-028 capability split (July 2026).
package clips

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// EnrichAndIndexClip helper — used by external batch/mixin callers.
// Inline on *Handler post-Split 2 since Ops no longer carries it.
// Returns immediately if enrichUC is nil; otherwise delegates to the
// shared enrichUC instance (single source of construction).
func (h *Handler) EnrichAndIndexClip(ctx context.Context, clip *asset.Asset, source string) {
	if h.enrichUC == nil {
		return
	}
	h.enrichUC.EnrichAndIndex(ctx, clip, source)
}

// EnrichMedia triggers semantic enrichment + embedding for any media
// asset. Step 5 Split 2: stayed on *Handler (inline) — JobsSvc route.
//
// Status codes:
//
//	503 — jobs service unavailable (S1a, no SafeGo workaround).
func (h *Handler) EnrichMedia(c *gin.Context) {
	var req struct {
		AssetID      string `json:"asset_id"`
		Source       string `json:"source"`
		SkipEmbedGen bool   `json:"skip_embed_gen"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if req.Source == "" {
		req.Source = c.Param("source")
	}

	if req.AssetID == "" {
		apiutil.BadRequest(c, "asset_id is required")
		return
	}

	if h.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable,
			"EnrichMedia requires the jobs service (S1a removed the in-process SafeGo fallback); wire jobsSvc to use /api/media/enrich")
		return
	}

	h.log.Info("dispatching media.enrich via jobs system",
		zap.String("asset_id", req.AssetID),
		zap.String("source", req.Source),
		zap.Bool("skip_embed_gen", req.SkipEmbedGen),
	)

	payload := map[string]any{
		"asset_id":       req.AssetID,
		"source":         req.Source,
		"skip_embed_gen": req.SkipEmbedGen,
	}
	job, err := h.jobsSvc.Enqueue(c.Request.Context(), &enqueueRequest{
		Type:      "media.enrich",
		Payload:   payload,
		ActiveKey: "enrich_clip_" + req.AssetID,
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
		"asset_id":   req.AssetID,
		"source":     req.Source,
		"method":     "media.enrich_worker_via_jobs",
		"message":    "enrichment + indexing dispatched to jobs system (worker will run)",
	})
}
