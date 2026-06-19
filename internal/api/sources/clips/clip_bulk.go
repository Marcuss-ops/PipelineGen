package clips

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api/sources/internal"
	"github.com/gin-gonic/gin"
)

// BulkAddTags adds tags to multiple clips in one request.
func (h *Handler) BulkAddTags(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		IDs  []string `json:"ids"`
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		internal.APIUtil.BadRequest(c, err.Error())
		return
	}

	if len(req.IDs) == 0 || len(req.Tags) == 0 {
		internal.APIUtil.OK(c, gin.H{"ok": true, "message": "no items or tags provided"})
		return
	}

	repo := h.resolveRepo(source)
	if repo == nil {
		internal.APIUtil.BadRequest(c, "invalid source: "+source)
		return
	}

	if err := repo.BulkAddTags(c.Request.Context(), req.IDs, req.Tags); err != nil {
		internal.APIUtil.InternalError(c, err)
		return
	}

	// Update Asset Tree if available
	if h.assetTreeSvc != nil {
		for _, id := range req.IDs {
			clip, err := repo.GetClip(c.Request.Context(), id)
			if err == nil {
				node := ClipToAssetNode(clip)
				h.assetTreeSvc.UpsertNode(c.Request.Context(), node)
			}
		}
	}

	internal.APIUtil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"count":   len(req.IDs),
		"message": fmt.Sprintf("added tags to %d items", len(req.IDs)),
	})
}

// BulkRemoveTags removes tags from multiple clips.
func (h *Handler) BulkRemoveTags(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		IDs  []string `json:"ids"`
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		internal.APIUtil.BadRequest(c, err.Error())
		return
	}

	if len(req.IDs) == 0 || len(req.Tags) == 0 {
		internal.APIUtil.OK(c, gin.H{"ok": true, "message": "no items or tags provided"})
		return
	}

	repo := h.resolveRepo(source)
	if repo == nil {
		internal.APIUtil.BadRequest(c, "invalid source: "+source)
		return
	}

	if err := repo.BulkRemoveTags(c.Request.Context(), req.IDs, req.Tags); err != nil {
		internal.APIUtil.InternalError(c, err)
		return
	}

	// Update Asset Tree if available
	if h.assetTreeSvc != nil {
		for _, id := range req.IDs {
			clip, err := repo.GetClip(c.Request.Context(), id)
			if err == nil {
				node := ClipToAssetNode(clip)
				h.assetTreeSvc.UpsertNode(c.Request.Context(), node)
			}
		}
	}

	internal.APIUtil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"count":   len(req.IDs),
		"message": fmt.Sprintf("removed tags from %d items", len(req.IDs)),
	})
}
