package sources

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
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

	var clip assets.Asset
	if err := c.ShouldBindJSON(&clip); err != nil {
		apiutil.BadRequest(c, "invalid clip data: "+err.Error())
		return
	}

	// Ensure ID is generated if missing
	if clip.ID == "" {
		clip.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if clip.Source == "" {
		clip.Source = assets.Source(source)
	}

	ctx := c.Request.Context()

	// 1. Save to DB via canonical asset.Repository (no converter needed).
	if err := h.assetRepo.Upsert(ctx, &clip); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// 2. Update Asset Tree
	if h.assetTreeSvc != nil {
		node := clipToAssetNode(&clip)
		if err := h.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			h.log.Warn("failed to upsert to asset tree", zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	// 3. Trigger async enrichment + indexing (non-blocking) with 3-minute timeout.
	// context.WithoutCancel ensures the goroutine survives the HTTP handler's return.
	// Stack-copy clip so the background goroutine owns its mutation independently.
	if h.metaWriter != nil || h.clipIndexer != nil || h.vectorStore != nil {
		clipCopy := clip
		concurrent.SafeGo("clip-create-enrich", func() {
			h.enrichAndIndexClip(context.WithoutCancel(ctx), &clipCopy, source)
		})
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"clip_id": clip.ID,
		"clip":    clip,
		"indexed": h.clipIndexer != nil || h.vectorStore != nil,
	})
}
