package clips

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// UpdateClip updates an existing clip.
//
// QDRANT-002: Routes through dispatcher.EnqueueAndIndex when wired for
// atomic UPSERT + outbox event. Falls back to raw repo.UpsertClip when
// dispatcher is nil (tests, partial deployments).
func (h *Handler) UpdateClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	var payload map[string]any
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

	// QDRANT-002: canonical path through dispatcher when wired.
	if h.dispatcher != nil {
		contentHash := clip.FileHash()
		if contentHash == "" {
			contentHash = clipID
		}
		if err := h.dispatcher.EnqueueAndIndex(ctx, clip, contentHash); err != nil {
			apiutil.InternalError(c, err)
			return
		}
	} else if err := repo.UpsertClip(ctx, clip); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// Also update Asset Tree if service is available
	if h.assetTreeSvc != nil {
		node := ClipToAssetNode(clip)
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
