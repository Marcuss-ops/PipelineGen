package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api/sources/internal"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// UpdateClip updates an existing clip.
func (h *Handler) UpdateClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		internal.APIUtil.BadRequest(c, "invalid source: "+source)
		return
	}

	var payload map[string]any
	if err := c.ShouldBindJSON(&payload); err != nil {
		internal.APIUtil.BadRequest(c, "invalid payload")
		return
	}

	ctx := c.Request.Context()
	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		internal.APIUtil.NotFound(c, "clip not found")
		return
	}

	// Manual update of fields from payload
	if val, ok := payload["name"].(string); ok {
		clip.Name = val
	}
	if val, ok := payload["category"].(string); ok {
		clip.Category = val
	}
	if val, ok := payload["tags"].([]any); ok {
		tags := make([]string, len(val))
		for i, v := range val {
			if s, ok := v.(string); ok {
				tags[i] = s
			}
		}
		clip.Tags = tags
	}
	if val, ok := payload["search_terms"].([]any); ok {
		terms := make([]string, len(val))
		for i, v := range val {
			if s, ok := v.(string); ok {
				terms[i] = s
			}
		}
		clip.SearchTerms = terms
	}
	if val, ok := payload["status"].(string); ok {
		clip.SetMetadataString("status", val)
	}
	if val, ok := payload["error"].(string); ok {
		clip.SetMetadataString("error", val)
	}
	if val, ok := payload["folder_id"].(string); ok {
		clip.SetFolderID(val)
	}
	if val, ok := payload["folder_path"].(string); ok {
		clip.SetFolderPath(val)
	}
	if val, ok := payload["drive_link"].(string); ok {
		clip.SetDriveLink(val)
	}
	if val, ok := payload["download_link"].(string); ok {
		clip.SetDownloadLink(val)
	}
	if val, ok := payload["thumb_url"].(string); ok {
		clip.ThumbnailURL = val
	}

	if err := repo.UpsertClip(ctx, clip); err != nil {
		internal.APIUtil.InternalError(c, err)
		return
	}

	// Also update Asset Tree if service is available
	if h.assetTreeSvc != nil {
		node := clipToAssetNode(clip)
		if err := h.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			h.log.Warn("failed to upsert to asset tree", zap.String("clip_id", clipID), zap.Error(err))
		}
	}

	internal.APIUtil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"clip_id": clipID,
		"clip":    clip,
	})
}
