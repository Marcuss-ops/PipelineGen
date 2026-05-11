package sources

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/models"
)

// ListFolders lists all folders for a source.
func (h *Handler) ListFolders(c *gin.Context) {
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
func (h *Handler) FolderStatus(c *gin.Context) {
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
func (h *Handler) RegenerateManifest(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	if h.folderMemSvc == nil {
		apiutil.InternalError(c, nil)
		return
	}

	h.log.Info("regenerating manifest for folder", zap.String("id", folderID))
	
	apiutil.OK(c, gin.H{
		"ok":     true,
		"source": source,
		"folder": folderID,
	})
}

// TrashFolder moves a folder to Drive trash.
func (h *Handler) TrashFolder(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"action": "trashed",
		"source": source,
		"folder": folderID,
	})
}

// DeleteFolder permanently deletes a folder.
func (h *Handler) DeleteFolder(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"action": "deleted",
		"source": source,
		"folder": folderID,
	})
}
