package clips

import (
	"fmt"
	"time"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// CreateClip creates a new clip and triggers semantic enrichment + vector indexing
// so the clip becomes immediately searchable via semantic search endpoints.
//
// The enrichment pipeline runs asynchronously via goroutine:
//  1. Save clip to DB + Asset Tree
//  2. Call semantic tagger (LLM) to generate search_text, tags, subjects
//  3. Upsert to clip indexer (embedding computation)
//  4. Upsert to Qdrant vector store
func (h *Handler) CreateClip(c *gin.Context) {
	source := c.Param("source")

	// Validate source param exists
	if source == "" {
		apiutil.BadRequest(c, "source is required")
		return
	}

	if h.assetRepo == nil {
		apiutil.InternalError(c, fmt.Errorf("asset repository not available"))
		return
	}

	var clip asset.Asset
	if err := c.ShouldBindJSON(&clip); err != nil {
		apiutil.BadRequest(c, "invalid clip data: "+err.Error())
		return
	}

	// Ensure ID is generated if missing
	if clip.ID == "" {
		clip.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if clip.Source == "" {
		clip.Source = asset.Source(source)
	}

	ctx := c.Request.Context()

	// 1. Save to DB via canonical asset.Repository (no converter needed).
	if err := h.assetRepo.Upsert(ctx, &clip); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// 2. Update Asset Tree
	if h.assetTreeSvc != nil {
		node := appclips.ClipToAssetNode(&clip)
		if err := h.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			h.log.Warn("failed to upsert to asset tree", zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	// 3. Trigger async enrichment + indexing via canonical jobs system
	// (S1a, June 2026). The previous implementation used
	// `concurrent.SafeGo` + `context.WithoutCancel` to detach from the
	// HTTP handler ctx — but that simulates a background job from a
	// handler, which AGENTS.md §7 + Pattern 8 explicitly forbid.
	// Canonical path: enqueue a `media.enrich` job whose worker runs in
	// the local broker pool (or a remote worker via VELOX_BROKER_URL),
	// with the same 3-minute hard cap. The clip row is already saved
	// before this point so a failed enqueue does NOT roll back the HTTP
	// write — we log a WARN and let the operator re-trigger via
	// `POST /:source/clips/:id/reindex`.
	indexed := false
	if h.enrichUC != nil && h.jobsSvc != nil {
		_, err := h.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type: jobservice.TypeMediaEnrich,
			Payload: map[string]any{
				"asset_id": clip.ID,
				"source":   source,
			},
			ActiveKey: "enrich_clip_" + clip.ID,
		})
		if err != nil {
			h.log.Warn("failed to enqueue media.enrich job (clip is saved; reactive re-index required)",
				zap.String("clip_id", clip.ID), zap.Error(err))
		} else {
			indexed = true
		}
	} else if h.enrichUC != nil {
		// S1a (June 2026): jobs service NOT wired but enrichment deps
		// are. Pre-lift behaviour claimed `indexed: true` while
		// doing nothing — that was misleading. Truthful signal:
		// leave `indexed: false`. Production always wires jobsSvc;
		// a missing jobsSvc in test fixtures is the test author's
		// responsibility (use a mock jobsSvc that no-ops
		// Enqueue, or wire a real one). Logged at WARN so test
		// authors see the drift in repo logs.
		h.log.Warn("CreateClip: enrichment deps wired but jobsSvc nil — clip saved; index will lag until reactive re-index",
			zap.String("clip_id", clip.ID), zap.String("source", source))
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"clip_id": clip.ID,
		"clip":    clip,
		"indexed": indexed,
	})
}
