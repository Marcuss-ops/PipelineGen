package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// CreateSubfolder creates a subfolder
func (h *ClipHandler) CreateSubfolder(c *gin.Context) {
	var req clip.CreateSubfolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	client, err := h.getDriveClient(c)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Drive service not available: " + err.Error(),
		})
		return
	}

	// Determine parent folder
	parentID := req.ParentID
	if parentID == "" {
		parentID = h.rootFolderID
	}

	// If group is specified, create under that group folder
	if req.Group != "" {
		groupFolder, err := client.GetOrCreateFolder(c.Request.Context(), req.Group, parentID)
		if err == nil {
			parentID = groupFolder
		}
	}

	// Clean folder name
	folderName := sanitizeFolderName(req.FolderName)

	// Create the folder
	folderID, err := client.CreateFolder(c.Request.Context(), folderName, parentID)
	if err != nil {
		logger.Error("Failed to create folder", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to create folder: " + err.Error(),
		})
		return
	}

	// Invalidate cache
	clip.InvalidateSearchCache()

	logger.Info("Subfolder created",
		zap.String("folder_name", folderName),
		zap.String("folder_id", folderID),
		zap.String("parent_id", parentID))

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"folder_name": folderName,
		"folder_id":   folderID,
		"parent_id":   parentID,
		"link":        drive.GetFolderLink(folderID),
	})
}

// Subfolders lists subfolders
func (h *ClipHandler) Subfolders(c *gin.Context) {
	var req clip.SubfoldersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	client, err := h.getDriveClient(c)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Drive service not available: " + err.Error(),
		})
		return
	}

	// Set defaults
	parentID := req.ParentID
	if parentID == "" {
		parentID = h.rootFolderID
	}
	maxDepth := req.MaxDepth
	if maxDepth == 0 {
		maxDepth = 3
	}
	maxResults := req.MaxResults
	if maxResults == 0 {
		maxResults = 100
	}

	opts := drive.ListFoldersOptions{
		ParentID: parentID,
		MaxDepth: maxDepth,
		MaxItems: maxResults,
	}

	driveFolders, err := client.ListFolders(c.Request.Context(), opts)
	if err != nil {
		logger.Error("Failed to list subfolders", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to list subfolders: " + err.Error(),
		})
		return
	}

	// Convert to clip.Folder and count clips
	var folders []clip.Folder
	for _, df := range driveFolders {
		folder := clip.Folder{
			ID:       df.ID,
			Name:     df.Name,
			Link:     df.Link,
			ParentID: parentID,
			Depth:    df.Depth,
		}

		// Count clips in folder
		content, err := client.GetFolderContent(c.Request.Context(), df.ID)
		if err == nil {
			clipCount := 0
			for _, file := range content.Files {
				if isVideoFile(file.MimeType, file.Name) {
					clipCount++
				}
			}
			folder.ClipCount = clipCount
		}

		// Convert subfolders
		for _, sub := range df.Subfolders {
			folder.Subfolders = append(folder.Subfolders, clip.Folder{
				ID:   sub.ID,
				Name: sub.Name,
				Link: sub.Link,
			})
		}

		folders = append(folders, folder)
	}

	logger.Info("Subfolders listed",
		zap.String("parent_id", parentID),
		zap.Int("total", len(folders)))

	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"parent_id":  parentID,
		"subfolders": folders,
		"total":      len(folders),
	})
}

// Health returns health status
func (h *ClipHandler) Health(c *gin.Context) {
	health := gin.H{
		"ok":      true,
		"status":  "healthy",
		"service": "clip-service",
	}

	if h.driveClient == nil {
		health["drive_status"] = "not_initialized"
		health["status"] = "degraded"
	} else {
		health["drive_status"] = "connected"
	}

	health["cache_stats"] = clip.GetGlobalCache().Stats()

	c.JSON(http.StatusOK, health)
}

// GetGroups returns available clip groups
func (h *ClipHandler) GetGroups(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"groups": clip.ClipGroups,
		"count":  len(clip.ClipGroups),
	})
}
