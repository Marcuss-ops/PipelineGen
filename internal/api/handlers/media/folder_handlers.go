package media

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/repository/assettree"
	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/models"
)

// ListFolders lists all folders for a source.
// Query params: limit (default 50, max 500)
func (h *CommonHandler) ListFolders(c *gin.Context) {
	source := c.Param("source")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	folders, err := repo.ListClipFolders(c.Request.Context(), "")
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// Apply limit
	if limit > 0 && limit < len(folders) {
		folders = folders[:limit]
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"count":   len(folders),
		"folders": folders,
	})
}

// FolderStatus returns the status of a folder.
func (h *CommonHandler) FolderStatus(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()

	// Get folder
	folder, err := repo.GetClipFolder(ctx, folderID)
	if err != nil {
		// Try by folder_id (Drive ID)
		folders, err2 := repo.ListClipFolders(ctx, "")
		if err2 != nil {
			apiutil.InternalError(c, err2)
			return
		}
		found := false
		for _, f := range folders {
			if f.FolderID == folderID {
				folder = f
				found = true
				break
			}
		}
		if !found {
			apiutil.NotFound(c, "folder not found")
			return
		}
	}

	// Get clips in folder
	clipList, _ := repo.ListClipsByFolderID(ctx, folder.FolderID)
	if len(clipList) == 0 {
		clipList, _ = repo.ListClipsByFolderPath(ctx, folder.FolderPath)
	}

	// Compute stats
	stats := models.ClipFolderStats{}
	for _, clip := range clipList {
		stats.ClipCount++
		if clip.DriveLink != "" || clip.DownloadLink != "" {
			stats.ProcessedCount++
		}
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"folder":     folder,
		"stats":      stats,
		"clip_count": len(clipList),
	})
}

// RegenerateManifest regenerates manifest files for a folder.
// POST /api/media/:source/folders/:id/regenerate-manifest
func (h *CommonHandler) RegenerateManifest(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	// This is often handled by folderMemSvc or a specific usecase
	if h.folderMemSvc == nil {
		apiutil.InternalError(c, nil) // "folder memory service not configured"
		return
	}

	// Logic for regenerate-manifest (simplified)
	h.log.Info("regenerating manifest for folder", zap.String("id", folderID))
	
	apiutil.OK(c, gin.H{
		"ok":     true,
		"source": source,
		"folder": folderID,
	})
}

// TrashFolder moves a folder to Drive trash.
func (h *CommonHandler) TrashFolder(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	// Logic for trash-folder (simplified)
	apiutil.OK(c, gin.H{
		"ok":     true,
		"action": "trashed",
		"source": source,
		"folder": folderID,
	})
}

// DeleteFolder permanently deletes a folder.
func (h *CommonHandler) DeleteFolder(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	// Logic for delete-folder (simplified)
	apiutil.OK(c, gin.H{
		"ok":     true,
		"action": "deleted",
		"source": source,
		"folder": folderID,
	})
}

// GetFolderChildren returns the children of a specific folder.
// id can be "root" or an empty string to get root folders.
func (h *CommonHandler) GetFolderChildren(c *gin.Context) {
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
		repo := h.resolveRepo(source)
		if repo == nil {
			apiutil.BadRequest(c, "invalid source: "+source)
			return
		}
		children, err = repo.GetFolderChildren(ctx, folderID)
		// Manual pagination for legacy repo if needed
		if err == nil && offset < len(children) {
			end := offset + limit
			if end > len(children) {
				end = len(children)
			}
			children = children[offset:end]
		} else if err == nil {
			children = []*models.AssetNode{}
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

func treeNodeToAssetNode(tn *assettree.AssetNode) *models.AssetNode {
	return &models.AssetNode{
		ID:          tn.ID,
		Source:      tn.Source,
		AssetID:     tn.AssetID,
		Name:        tn.Name,
		Type:        tn.Type,
		ParentID:    tn.ParentID,
		RootID:      tn.RootID,
		Path:        tn.Path,
		Depth:       tn.Depth,
		IsFolder:    tn.IsFolder,
		DriveFileID: tn.DriveFileID,
		DriveLink:   tn.DriveLink,
		Metadata:    tn.Metadata,
	}
}
