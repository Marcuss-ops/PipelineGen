package sources

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/models"
)

// GetFolderChildren returns the children of a specific folder.
func (h *Handler) GetFolderChildren(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	if folderID == "root" {
		folderID = ""
	}

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	var children []*models.AssetNode
	var err error

	if h.assetTreeSvc != nil {
		treeNodes, treeErr := h.assetTreeSvc.ListChildrenPaged(ctx, source, folderID, limit, offset)
		if treeErr == nil {
			for _, tn := range treeNodes {
				children = append(children, treeNodeToAssetNode(tn))
			}
		} else {
			err = treeErr
		}
	} else {
		children = []*models.AssetNode{}
		clipChildren, clipErr := repo.GetFolderChildren(ctx, folderID)
		if clipErr == nil {
			for _, clip := range clipChildren {
				children = append(children, treeNodeToAssetNode(clipToAssetNode(clip)))
			}
		} else {
			err = clipErr
		}
	}

	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":       true,
		"source":   source,
		"count":    len(children),
		"children": children,
	})
}

// GetTree returns the direct children of a given parent folder.
func (h *Handler) GetTree(c *gin.Context) {
	source := c.Param("source")
	parentID := c.Query("parent_id")

	if parentID == "root" {
		parentID = ""
	}

	if h.assetTreeSvc == nil {
		apiutil.InternalError(c, nil)
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
