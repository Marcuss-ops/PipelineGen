// Package clips — folder command sub-handler (Fase 2 split, June 2026).
//
// Extracted from ops.go: write folder operations (RegenerateManifest, TrashFolder, DeleteFolder).
// Depends on: ClipsRepository, DriveAdmin, FolderMemSvc.
package clips

import (
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// RegenerateManifest regenerates manifest files for a folder.
func (oh *OpsHandler) RegenerateManifest(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	oh.log.Info("regenerating manifest for folder", zap.String("id", folderID))

	apiutil.OK(c, gin.H{
		"ok":     true,
		"source": source,
		"folder": folderID,
	})
}

// TrashFolder moves a folder to Drive trash.
func (oh *OpsHandler) TrashFolder(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
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
				oh.log.Error("failed to remove local folder path", zap.String("path", folder.FolderPath), zap.Error(err))
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
							oh.log.Error("failed to remove local folder path", zap.String("path", f.FolderPath), zap.Error(err))
						}
					}
					break
				}
			}
		}
	}

	if driveFolderID != "" {
		if oh.driveAdmin == nil {
			apiutil.InternalError(c, fmt.Errorf("drive uploader not configured"))
			return
		}
		if err := oh.driveAdmin.TrashFolder(ctx, driveFolderID); err != nil {
			oh.log.Error("failed to trash folder in Google Drive", zap.String("folder_id", driveFolderID), zap.Error(err))
			apiutil.InternalError(c, err)
			return
		}
	}

	if dbFolderID != "" {
		if err := repo.DeleteFolder(ctx, dbFolderID); err != nil {
			oh.log.Error("failed to delete folder from database", zap.String("id", dbFolderID), zap.Error(err))
		}
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"action": "trashed",
		"source": source,
		"folder": folderID,
	})
}

// DeleteFolder permanently deletes a folder.
func (oh *OpsHandler) DeleteFolder(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
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
				oh.log.Error("failed to remove local folder path", zap.String("path", folder.FolderPath), zap.Error(err))
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
							oh.log.Error("failed to remove local folder path", zap.String("path", f.FolderPath), zap.Error(err))
						}
					}
					break
				}
			}
		}
	}

	if driveFolderID != "" {
		if oh.driveAdmin == nil {
			apiutil.InternalError(c, fmt.Errorf("drive uploader not configured"))
			return
		}
		if err := oh.driveAdmin.DeleteFolder(ctx, driveFolderID); err != nil {
			oh.log.Error("failed to delete folder in Google Drive", zap.String("folder_id", driveFolderID), zap.Error(err))
			apiutil.InternalError(c, err)
			return
		}
	}

	if dbFolderID != "" {
		if err := repo.DeleteFolder(ctx, dbFolderID); err != nil {
			oh.log.Error("failed to delete folder from database", zap.String("id", dbFolderID), zap.Error(err))
		}
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"action": "deleted",
		"source": source,
		"folder": folderID,
	})
}
