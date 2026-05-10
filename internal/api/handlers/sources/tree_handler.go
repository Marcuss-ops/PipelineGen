package sources

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/pkg/apiutil"
)

// GetTree returns the direct children of a given parent folder.
func (h *Handler) GetTree(c *gin.Context) {
	source := c.Param("source")
	parentID := c.Query("parent_id")

	if parentID == "root" {
		parentID = ""
	}

	if h.assetTreeSvc == nil {
		apiutil.InternalError(c, nil) // "asset tree service not configured"
		return
	}

	children, err := h.assetTreeSvc.ListChildren(c.Request.Context(), source, parentID)
	if err != nil {
		h.log.Error("failed to list children", zap.Error(err), zap.String("source", source), zap.String("parent_id", parentID))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":       true,
		"source":   source,
		"children": children,
	})
}

// GetBreadcrumb returns the path from root down to the specified node ID.
func (h *Handler) GetBreadcrumb(c *gin.Context) {
	source := c.Param("source")
	id := c.Query("id")

	if id == "" {
		apiutil.BadRequest(c, "missing id parameter")
		return
	}

	if h.assetTreeSvc == nil {
		apiutil.InternalError(c, nil)
		return
	}

	breadcrumb, err := h.assetTreeSvc.GetBreadcrumb(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to get breadcrumb", zap.Error(err), zap.String("source", source), zap.String("id", id))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"breadcrumb": breadcrumb,
	})
}
