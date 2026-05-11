package sources

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/models"
)

// CreateClip creates a new clip.
func (h *Handler) CreateClip(c *gin.Context) {
	source := c.Param("source")
	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	var clip models.Clip
	if err := c.ShouldBindJSON(&clip); err != nil {
		apiutil.BadRequest(c, "invalid clip data: "+err.Error())
		return
	}

	// Ensure ID is generated if missing
	if clip.ID == "" {
		clip.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	if err := repo.UpsertClip(c.Request.Context(), &clip); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// Also update Asset Tree if service is available
	if h.assetTreeSvc != nil {
		node := clipToAssetNode(&clip)
		if err := h.assetTreeSvc.UpsertNode(c.Request.Context(), node); err != nil {
			h.log.Warn("failed to upsert to asset tree", zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"clip_id": clip.ID,
		"clip":    clip,
	})
}

// UpdateClip updates an existing clip.
func (h *Handler) UpdateClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		apiutil.BadRequest(c, "invalid payload")
		return
	}

	ctx := c.Request.Context()
	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Manual update of fields from payload
	if val, ok := payload["name"].(string); ok {
		clip.Name = val
	}
	if val, ok := payload["category"].(string); ok {
		clip.Category = val
	}
	if val, ok := payload["tags"].([]interface{}); ok {
		tags := make([]string, len(val))
		for i, v := range val {
			if s, ok := v.(string); ok {
				tags[i] = s
			}
		}
		clip.Tags = tags
	}
	if val, ok := payload["search_terms"].([]interface{}); ok {
		terms := make([]string, len(val))
		for i, v := range val {
			if s, ok := v.(string); ok {
				terms[i] = s
			}
		}
		clip.SearchTerms = terms
	}
	if val, ok := payload["status"].(string); ok {
		clip.Status = val
	}
	if val, ok := payload["error"].(string); ok {
		clip.Error = val
	}
	if val, ok := payload["folder_id"].(string); ok {
		clip.FolderID = val
	}
	if val, ok := payload["folder_path"].(string); ok {
		clip.FolderPath = val
	}
	if val, ok := payload["drive_link"].(string); ok {
		clip.DriveLink = val
	}
	if val, ok := payload["download_link"].(string); ok {
		clip.DownloadLink = val
	}
	if val, ok := payload["thumb_url"].(string); ok {
		clip.ThumbURL = val
	}

	if err := repo.UpsertClip(ctx, clip); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// Also update Asset Tree if service is available
	if h.assetTreeSvc != nil {
		node := clipToAssetNode(clip)
		if err := h.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			h.log.Warn("failed to upsert to asset tree", zap.String("clip_id", clipID), zap.Error(err))
		}
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"clip_id": clipID,
		"clip":    clip,
	})
}

// TrashClip moves a clip to Drive trash and removes SQLite record.
func (h *Handler) TrashClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	if err := h.deletionSvc.DeleteClip(c.Request.Context(), source, clipID, false); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "trashed",
		"source":  source,
		"clip_id": clipID,
	})
}

// DeleteClip permanently deletes a clip from Drive and SQLite.
func (h *Handler) DeleteClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	if err := h.deletionSvc.DeleteClip(c.Request.Context(), source, clipID, true); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "deleted",
		"source":  source,
		"clip_id": clipID,
	})
}

// BulkAddTags adds tags to multiple clips in one request.
func (h *Handler) BulkAddTags(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		IDs  []string `json:"ids"`
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	if len(req.IDs) == 0 || len(req.Tags) == 0 {
		apiutil.OK(c, gin.H{"ok": true, "message": "no items or tags provided"})
		return
	}

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	if err := repo.BulkAddTags(c.Request.Context(), req.IDs, req.Tags); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// Update Asset Tree if available
	if h.assetTreeSvc != nil {
		for _, id := range req.IDs {
			clip, err := repo.GetClip(c.Request.Context(), id)
			if err == nil {
				node := clipToAssetNode(clip)
				h.assetTreeSvc.UpsertNode(c.Request.Context(), node)
			}
		}
	}

	apiutil.OK(c, gin.H{
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
		apiutil.BadRequest(c, err.Error())
		return
	}

	if len(req.IDs) == 0 || len(req.Tags) == 0 {
		apiutil.OK(c, gin.H{"ok": true, "message": "no items or tags provided"})
		return
	}

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	if err := repo.BulkRemoveTags(c.Request.Context(), req.IDs, req.Tags); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// Update Asset Tree if available
	if h.assetTreeSvc != nil {
		for _, id := range req.IDs {
			clip, err := repo.GetClip(c.Request.Context(), id)
			if err == nil {
				node := clipToAssetNode(clip)
				h.assetTreeSvc.UpsertNode(c.Request.Context(), node)
			}
		}
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"count":   len(req.IDs),
		"message": fmt.Sprintf("removed tags from %d items", len(req.IDs)),
	})
}
