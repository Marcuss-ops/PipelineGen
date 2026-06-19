package clips

import (
	"fmt"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/sources/internal"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// ListFolders lists all folders for a source.
func (h *Handler) ListFolders(c *gin.Context) {
	source := c.Param("source")

	repo := h.resolveRepo(source)
	if repo == nil {
		internal.APIUtil.BadRequest(c, "invalid source: "+source)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	folders, err := repo.ListFolders(c.Request.Context(), "")
	if err != nil {
		internal.APIUtil.InternalError(c, err)
		return
	}

	// Apply limit
	if limit > 0 && limit < len(folders) {
		folders = folders[:limit]
	}

	internal.APIUtil.OK(c, gin.H{
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
		internal.APIUtil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()

	// Get folder
	folder, err := repo.GetFolder(ctx, folderID)
	if err != nil {
		// Try by folder_id (Drive ID)
		folders, err2 := repo.ListFolders(ctx, "")
		if err2 != nil {
			internal.APIUtil.InternalError(c, err2)
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
			internal.APIUtil.NotFound(c, "folder not found")
			return
		}
	}

	// Get clips in folder
	clipList, _ := repo.ListByFolderID(ctx, folder.FolderID)
	if len(clipList) == 0 {
		clipList, _ = repo.ListByFolderPath(ctx, folder.FolderPath)
	}

	// Compute stats
	stats := media.ClipFolderStats{}
	for _, clip := range clipList {
		stats.ClipCount++
		if clip.DriveLink() != "" || clip.DownloadLink() != "" {
			stats.ProcessedCount++
		}
	}

	internal.APIUtil.OK(c, gin.H{
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
		internal.APIUtil.BadRequest(c, "invalid source: "+source)
		return
	}

	if h.folderMemSvc == nil {
		internal.APIUtil.InternalError(c, nil)
		return
	}

	h.log.Info("regenerating manifest for folder", zap.String("id", folderID))

	internal.APIUtil.OK(c, gin.H{
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
		internal.APIUtil.BadRequest(c, "invalid source: "+source)
		return
	}

	var driveFolderID string
	var dbFolderID string
	ctx := c.Request.Context()

	folder, err := repo.GetFolder(ctx, folderID)
	if err == nil && folder != nil {
		driveFolderID = folder.FolderID
		dbFolderID = folder.ID
		if folder.FolderPath != "" {
			if err := os.RemoveAll(folder.FolderPath); err != nil {
				h.log.Error("failed to remove local folder path", zap.String("path", folder.FolderPath), zap.Error(err))
			}
		}
	} else {
		driveFolderID = folderID
		folders, err2 := repo.ListFolders(ctx, "")
		if err2 == nil {
			for _, f := range folders {
				if f.FolderID == folderID {
					dbFolderID = f.ID
					if f.FolderPath != "" {
						if err := os.RemoveAll(f.FolderPath); err != nil {
							h.log.Error("failed to remove local folder path", zap.String("path", f.FolderPath), zap.Error(err))
						}
					}
					break
				}
			}
		}
	}

	if driveFolderID != "" {
		if h.driveUploader == nil {
			internal.APIUtil.InternalError(c, fmt.Errorf("drive uploader not configured"))
			return
		}
		if err := h.driveUploader.TrashFolder(ctx, driveFolderID); err != nil {
			h.log.Error("failed to trash folder in Google Drive", zap.String("folder_id", driveFolderID), zap.Error(err))
			internal.APIUtil.InternalError(c, err)
			return
		}
	}

	if dbFolderID != "" {
		if err := repo.DeleteFolder(ctx, dbFolderID); err != nil {
			h.log.Error("failed to delete folder from database", zap.String("id", dbFolderID), zap.Error(err))
		}
	}

	internal.APIUtil.OK(c, gin.H{
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
		internal.APIUtil.BadRequest(c, "invalid source: "+source)
		return
	}

	var driveFolderID string
	var dbFolderID string
	ctx := c.Request.Context()

	folder, err := repo.GetFolder(ctx, folderID)
	if err == nil && folder != nil {
		driveFolderID = folder.FolderID
		dbFolderID = folder.ID
		if folder.FolderPath != "" {
			if err := os.RemoveAll(folder.FolderPath); err != nil {
				h.log.Error("failed to remove local folder path", zap.String("path", folder.FolderPath), zap.Error(err))
			}
		}
	} else {
		driveFolderID = folderID
		folders, err2 := repo.ListFolders(ctx, "")
		if err2 == nil {
			for _, f := range folders {
				if f.FolderID == folderID {
					dbFolderID = f.ID
					if f.FolderPath != "" {
						if err := os.RemoveAll(f.FolderPath); err != nil {
							h.log.Error("failed to remove local folder path", zap.String("path", f.FolderPath), zap.Error(err))
						}
					}
					break
				}
			}
		}
	}

	if driveFolderID != "" {
		if h.driveUploader == nil {
			internal.APIUtil.InternalError(c, fmt.Errorf("drive uploader not configured"))
			return
		}
		if err := h.driveUploader.DeleteFolder(ctx, driveFolderID); err != nil {
			h.log.Error("failed to delete folder in Google Drive", zap.String("folder_id", driveFolderID), zap.Error(err))
			internal.APIUtil.InternalError(c, err)
			return
		}
	}

	if dbFolderID != "" {
		if err := repo.DeleteFolder(ctx, dbFolderID); err != nil {
			h.log.Error("failed to delete folder from database", zap.String("id", dbFolderID), zap.Error(err))
		}
	}

	internal.APIUtil.OK(c, gin.H{
		"ok":     true,
		"action": "deleted",
		"source": source,
		"folder": folderID,
	})
}
