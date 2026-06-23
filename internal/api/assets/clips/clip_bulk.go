package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
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
		apiutil.BadRequest(c, err.Error())
		return
	}

	result, err := h.bulkTagsUC.AddTags(c.Request.Context(), clips.BulkTagsRequest{
		Source: source,
		IDs:    req.IDs,
		Tags:   req.Tags,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  result.Source,
		"count":   result.Count,
		"message": result.Message,
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

	result, err := h.bulkTagsUC.RemoveTags(c.Request.Context(), clips.BulkTagsRequest{
		Source: source,
		IDs:    req.IDs,
		Tags:   req.Tags,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  result.Source,
		"count":   result.Count,
		"message": result.Message,
	})
}
