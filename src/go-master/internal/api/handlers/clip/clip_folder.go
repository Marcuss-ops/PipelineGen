package clip

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/clip"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// ReadFolderClips reads clips from a folder
func (h *ClipHandler) ReadFolderClips(c *gin.Context) {
	var req clip.ReadFolderClipsRequest
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

	ctx := c.Request.Context()

	// Find folder by name if ID not provided
	folderID := req.FolderID
	folderName := req.FolderName

	if folderID == "" && folderName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "folder_id or folder_name required",
		})
		return
	}

	if folderID == "" && folderName != "" {
		folder, err := client.GetFolderByName(ctx, folderName, h.rootFolderID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{
				"ok":    false,
				"error": fmt.Sprintf("Folder '%s' not found", folderName),
			})
			return
		}
		folderID = folder.ID
		folderName = folder.Name
	}

	// Check cache
	cacheKey := clip.FolderCacheKey(folderID, "", "")
	if cached, found := clip.GetGlobalCache().Get(cacheKey); found {
		if content, ok := cached.(clip.FolderContent); ok {
			c.JSON(http.StatusOK, gin.H{
				"ok":               true,
				"folder_id":        content.FolderID,
				"folder_name":      content.FolderName,
				"clips":            content.Clips,
				"videos":           content.Videos, // Alias
				"subfolders":       content.Subfolders,
				"total_clips":      content.TotalClips,
				"total_videos":     content.TotalVideos,
				"total_subfolders": content.TotalSubfolders,
				"cached":           true,
			})
			return
		}
	}

	// Get folder content
	content, err := client.GetFolderContent(ctx, folderID)
	if err != nil {
		logger.Error("Failed to get folder content", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to get folder content: " + err.Error(),
		})
		return
	}

	var clips []clip.Clip
	var subfolders []clip.Folder

	for _, file := range content.Files {
		if isVideoFile(file.MimeType, file.Name) {
			clips = append(clips, clip.Clip{
				ID: file.ID, Name: cleanClipName(file.Name), Filename: file.Name,
				DriveLink: file.Link, Size: file.Size, MimeType: file.MimeType,
				ModifiedAt: file.ModifiedTime, FolderID: folderID, FolderName: folderName,
				Thumbnail: getThumbnailURL(file.ID),
			})
		}
	}

	for _, sf := range content.Subfolders {
		subfolders = append(subfolders, clip.Folder{ID: sf.ID, Name: sf.Name, Link: sf.Link})
	}

	// Include subfolder clips if requested
	if req.IncludeSubfolders {
		for _, sf := range content.Subfolders {
			subContent, err := client.GetFolderContent(ctx, sf.ID)
			if err != nil {
				continue
			}
			for _, file := range subContent.Files {
				if isVideoFile(file.MimeType, file.Name) {
					clips = append(clips, clip.Clip{
						ID:         file.ID,
						Name:       cleanClipName(file.Name),
						Filename:   file.Name,
						DriveLink:  file.Link,
						Size:       file.Size,
						MimeType:   file.MimeType,
						ModifiedAt: file.ModifiedTime,
						FolderID:   sf.ID,
						FolderName: sf.Name,
						Thumbnail:  getThumbnailURL(file.ID),
					})
				}
			}
		}
	}

	// Cache the result
	folderContent := clip.FolderContent{
		FolderID:        folderID,
		FolderName:      folderName,
		Clips:           clips,
		Videos:          clips, // Alias
		Subfolders:      subfolders,
		TotalClips:      len(clips),
		TotalVideos:     len(clips),
		TotalSubfolders: len(subfolders),
	}
	clip.GetGlobalCache().Set(cacheKey, folderContent)

	logger.Info("Folder clips read",
		zap.String("folder_id", folderID),
		zap.String("folder_name", folderName),
		zap.Int("total_clips", len(clips)),
		zap.Int("total_subfolders", len(subfolders)))

	c.JSON(http.StatusOK, gin.H{
		"ok":               true,
		"folder_id":        folderID,
		"folder_name":      folderName,
		"clips":            clips,
		"videos":           clips, // Alias for compatibility
		"subfolders":       subfolders,
		"total_clips":      len(clips),
		"total_videos":     len(clips),
		"total_subfolders": len(subfolders),
	})
}

// Suggest suggests clips for a title
func (h *ClipHandler) Suggest(c *gin.Context) {
	var req clip.SuggestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "title is required",
		})
		return
	}

	if h.suggester == nil {
		client, err := h.getDriveClient(c)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"ok":    false,
				"error": "Drive service not available: " + err.Error(),
			})
			return
		}
		h.mu.Lock()
		if h.suggester == nil {
			h.suggester = clip.NewSuggester(client, h.rootFolderID)
		}
		// Set indexer if available for fast suggestions
		if h.indexer != nil {
			h.suggester.SetIndexer(h.indexer)
		}
		h.mu.Unlock()
	}

	// Set defaults
	maxResults := req.MaxResults
	if maxResults == 0 {
		maxResults = 10
	}
	minScore := req.MinScore
	if minScore == 0 {
		minScore = 10.0
	}

	startTime := time.Now()

	suggestions, err := h.suggester.SuggestClips(
		c.Request.Context(),
		req.Title,
		req.Script,
		req.Group,
		maxResults,
		minScore,
		req.MediaType,
	)
	if err != nil {
		logger.Error("Failed to generate suggestions", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to generate suggestions: " + err.Error(),
		})
		return
	}

	// Record usage for suggested clips
	var clipIDs []string
	for _, s := range suggestions {
		clipIDs = append(clipIDs, s.Clip.ID)
	}
	clip.RecordMultipleClipUsage(clipIDs)

	processingTime := time.Since(startTime).Milliseconds()

	logger.Info("Clip suggestions generated",
		zap.String("title", req.Title),
		zap.String("group", req.Group),
		zap.Int("suggestions", len(suggestions)),
		zap.Int64("ms", processingTime))

	c.JSON(http.StatusOK, gin.H{
		"ok":              true,
		"title":           req.Title,
		"suggestions":     suggestions,
		"total":           len(suggestions),
		"group":           req.Group,
		"processing_time": processingTime,
	})
}
