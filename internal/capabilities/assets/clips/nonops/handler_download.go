// Package nonops — handler_download.go: EnrichMedia + EnrichAndIndexClip
// endpoints extracted from clips/handler_download.go per
// PR-CLIPS-NONOPS-EXTRACT (July 2026). The method bodies are
// byte-equivalent with the pre-extraction versions — only the
// receiver type changed from *Handler to *NonOpsHandler.
package assets

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// EnrichAndIndexClip helper — kept for the nonops.Handler interface
// contract (godlike/06 SSOT compile-time pin) but card 10 (July 2026)
// removed its only production caller (sourcingEnrichmentAdapter now
// depends on the canonical appclips.ClipEnricher typed port and never
// reaches through *clips.Handler). Returns immediately if enrichUC is
// nil; otherwise delegates to the shared enrichUC instance using the
// slim (ctx, clipID) signature introduced in card 10.
//
// The `source` parameter is retained on the outer signature for
// interface conformance; the slim enrichUC.EnrichAndIndex looks up
// the asset by clipID internally so the source context is not needed
// at this call site.
func (h *NonOpsHandler) EnrichAndIndexClip(ctx context.Context, clip *asset.Asset, source string) {
	if h.enrichUC == nil {
		return
	}
	_ = source
	h.enrichUC.EnrichAndIndex(ctx, clip.ID)
}

// EnrichMedia triggers semantic enrichment + embedding for any media
// asset. Step 5 Split 2 (June 2026): stayed on the orchestrator
// *Handler (now delegated to *NonOpsHandler per
// PR-CLIPS-NONOPS-EXTRACT) — JobsSvc route.
//
// Status codes:
//
//	503 — jobs service unavailable (S1a, no SafeGo workaround).
func (h *NonOpsHandler) EnrichMedia(c *gin.Context) {
	var req struct {
		AssetID      string `json:"asset_id"`
		Source       string `json:"source"`
		SkipEmbedGen bool   `json:"skip_embed_gen"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	source := req.Source
	if source == "" {
		source = c.Param("source")
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
		zap.String("source", source),
		zap.Bool("skip_embed_gen", req.SkipEmbedGen),
	)

	payload := map[string]any{
		"asset_id":       req.AssetID,
		"source":         source,
		"skip_embed_gen": req.SkipEmbedGen,
	}
	job, err := h.jobsSvc.Enqueue(c.Request.Context(), &kerneljob.EnqueueRequest{
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
		"source":     source,
		"method":     "media.enrich_worker_via_jobs",
		"message":    "enrichment + indexing dispatched to jobs system (worker will run)",
	})
}
