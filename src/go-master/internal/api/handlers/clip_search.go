package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// SearchFolders searches for clip folders
func (h *ClipHandler) SearchFolders(c *gin.Context) {
	var req clip.SearchFoldersRequest
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

	startTime := time.Now()

	// Check cache first
	cacheKey := clip.SearchCacheKey(req.Query, req.Group, req.ParentID)
	if cached, found := clip.GetGlobalCache().Get(cacheKey); found {
		if result, ok := cached.(clip.SearchResult); ok {
			result.Cached = true
			c.JSON(http.StatusOK, gin.H{
				"ok":      true,
				"folders": result.Folders,
				"total":   result.Total,
				"query":   req.Query,
				"group":   req.Group,
				"cached":  true,
			})
			return
		}
	}

	// Determine parent folder
	parentID := req.ParentID
	if parentID == "" {
		parentID = h.rootFolderID
	}

	// If group is specified, try to find that folder first
	if req.Group != "" {
		groupFolder, err := client.GetFolderByName(c.Request.Context(), req.Group, parentID)
		if err == nil && groupFolder != nil {
			parentID = groupFolder.ID
		}
	}

	// Set defaults
	maxDepth := req.MaxDepth
	if maxDepth == 0 {
		maxDepth = 2
	}
	maxItems := req.MaxResults
	if maxItems == 0 {
		maxItems = 50
	}

	opts := drive.ListFoldersOptions{
		ParentID: parentID,
		MaxDepth: maxDepth,
		MaxItems: maxItems,
	}

	driveFolders, err := client.ListFolders(c.Request.Context(), opts)
	if err != nil {
		logger.Error("Failed to list folders", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to list folders: " + err.Error(),
		})
		return
	}

	// Filter by query if provided
	var folders []clip.Folder
	queryLower := strings.ToLower(req.Query)
	for _, df := range driveFolders {
		folder := clip.Folder{
			ID:       df.ID,
			Name:     df.Name,
			Link:     df.Link,
			ParentID: parentID,
			Depth:    df.Depth,
		}

		// Convert subfolders
		for _, sub := range df.Subfolders {
			folder.Subfolders = append(folder.Subfolders, clip.Folder{
				ID:   sub.ID,
				Name: sub.Name,
				Link: sub.Link,
			})
		}

		// Filter by query
		if req.Query == "" || strings.Contains(strings.ToLower(df.Name), queryLower) {
			folders = append(folders, folder)
		}
	}

	searchTime := time.Since(startTime).Milliseconds()

	// Cache the result
	result := clip.SearchResult{
		Folders:    folders,
		Total:      len(folders),
		Query:      req.Query,
		Group:      req.Group,
		Cached:     false,
		SearchTime: searchTime,
	}
	clip.GetGlobalCache().Set(cacheKey, result)

	logger.Info("Clip folders search completed",
		zap.String("query", req.Query),
		zap.String("group", req.Group),
		zap.Int("total", len(folders)),
		zap.Int64("ms", searchTime))

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"folders":      folders,
		"total":        len(folders),
		"query":        req.Query,
		"group":        req.Group,
		"search_time":  searchTime,
	})
}
