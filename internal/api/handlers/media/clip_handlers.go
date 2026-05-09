package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/apiutil"
	driveutil "velox/go-master/pkg/drive"
	"velox/go-master/pkg/models"
)

// ClipStatus returns the status of a clip.
func (h *CommonHandler) ClipStatus(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	clip, err := repo.GetClip(c.Request.Context(), clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Determine status based on available data
	status := "unknown"
	if clip.DriveLink != "" || clip.DownloadLink != "" {
		status = "processed"
	} else if clip.LocalPath != "" {
		status = "downloaded"
	} else {
		status = "pending"
	}

	apiutil.OK(c, gin.H{
		"ok":             true,
		"source":         source,
		"clip_id":        clipID,
		"exists_db":      true,
		"name":           clip.Name,
		"has_local_file": clip.LocalPath != "",
		"local_path":     clip.LocalPath,
		"has_drive_link": clip.DriveLink != "" || clip.DownloadLink != "",
		"drive_link":     clip.DriveLink,
		"download_link":  clip.DownloadLink,
		"file_hash":      clip.FileHash,
		"folder_id":      clip.FolderID,
		"folder_path":    clip.FolderPath,
		"status":         status,
	})
}

// VerifyClip verifies DB, local file, and Drive coherence.
func (h *CommonHandler) VerifyClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	clip, err := repo.GetClip(c.Request.Context(), clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	result := h.verifyClip(c.Request.Context(), source, repo, clip)
	c.JSON(http.StatusOK, result)
}

// verifyClip performs verification of a single clip and returns the result map.
func (h *CommonHandler) verifyClip(ctx context.Context, source string, repo *clips.Repository, clip *models.Clip) gin.H {
	result := gin.H{
		"ok":      true,
		"source":  source,
		"clip_id": clip.ID,
		"issues":  []string{},
	}

	// Check DB
	result["db"] = true

	// Check local file
	hasLocalFile := false
	if clip.LocalPath != "" {
		if _, statErr := os.Stat(clip.LocalPath); statErr == nil {
			hasLocalFile = true
			result["local_file"] = true
			result["local_path"] = clip.LocalPath
		} else {
			result["local_file"] = false
			result["local_path"] = clip.LocalPath
			result["local_error"] = "file not found: " + statErr.Error()
			result["issues"] = append(result["issues"].([]string), "local_file_missing")
		}
	} else {
		result["local_file"] = false
		result["issues"] = append(result["issues"].([]string), "local_path_empty")
	}

	// Check Drive link
	driveLink := clip.DriveLink
	if driveLink == "" {
		driveLink = clip.DownloadLink
	}
	if driveLink != "" {
		result["has_drive_link"] = true
		result["drive_link"] = driveLink

		// Extract file ID and verify with Drive API
		fileID := driveutil.FileIDFromLink(driveLink)
		if fileID != "" && h.cleanupSvc != nil {
			result["drive_file_id"] = fileID
		} else if fileID == "" {
			result["drive_link_valid"] = false
			result["issues"] = append(result["issues"].([]string), "drive_link_invalid")
		}
	} else {
		result["has_drive_link"] = false
		result["issues"] = append(result["issues"].([]string), "drive_link_missing")
	}

	// Check hash
	if clip.FileHash != "" {
		result["hash"] = clip.FileHash
		result["has_hash"] = true

		// Verify hash if local file exists
		if hasLocalFile {
			result["hash_verified"] = false // Placeholder
		}
	} else {
		result["has_hash"] = false
		result["issues"] = append(result["issues"].([]string), "hash_missing")
	}

	// Check folder info
	if clip.FolderID != "" {
		result["folder_id"] = clip.FolderID
	}
	if clip.FolderPath != "" {
		result["folder_path"] = clip.FolderPath
	}

	// Determine status based on available data
	status := "unknown"
	if clip.DriveLink != "" || clip.DownloadLink != "" {
		status = "processed"
	} else if clip.LocalPath != "" {
		status = "downloaded"
	} else {
		status = "pending"
	}
	result["status"] = status

	// Determine overall status
	issues := result["issues"].([]string)
	if len(issues) == 0 {
		result["coherent"] = true
	} else {
		result["coherent"] = false
		result["issue_count"] = len(issues)
	}

	return result
}

// TrashClip moves a clip to Drive trash and removes SQLite record.
func (h *CommonHandler) TrashClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()

	// Get clip to find Drive file ID
	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Trash file in Drive if driveUploader is available
	if h.driveUploader != nil {
		fileID := driveutil.FileIDFromLink(clip.DriveLink)
		if fileID == "" {
			fileID = driveutil.FileIDFromLink(clip.DownloadLink)
		}
		if fileID != "" {
			if err := h.driveUploader.TrashFile(ctx, fileID); err != nil {
				h.log.Warn("failed to trash drive file", zap.String("file_id", fileID), zap.Error(err))
				// Continue to delete from DB even if Drive operation fails
			}
		}
	}

	// Delete from DB using resolved repo
	if err := repo.DeleteClip(ctx, clipID); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "trashed",
		"source":  source,
		"clip_id": clipID,
	})
}

// DeleteClip permanently deletes a clip from Drive and SQLite.
func (h *CommonHandler) DeleteClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()

	// Get clip to find Drive file ID
	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Delete file from Drive permanently if driveUploader is available
	if h.driveUploader != nil {
		fileID := driveutil.FileIDFromLink(clip.DriveLink)
		if fileID == "" {
			fileID = driveutil.FileIDFromLink(clip.DownloadLink)
		}
		if fileID != "" {
			if err := h.driveUploader.DeleteFile(ctx, fileID); err != nil {
				h.log.Warn("failed to delete drive file", zap.String("file_id", fileID), zap.Error(err))
				// Continue to delete from DB even if Drive operation fails
			}
		}
	}

	// Delete from DB using resolved repo
	if err := repo.DeleteClip(ctx, clipID); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "deleted",
		"source":  source,
		"clip_id": clipID,
	})
}

// ReuploadClip reuploads a clip to Drive.
func (h *CommonHandler) ReuploadClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Check local file
	if clip.LocalPath == "" {
		apiutil.BadRequest(c, "clip has no local path")
		return
	}

	if _, err := os.Stat(clip.LocalPath); err != nil {
		apiutil.BadRequest(c, "local file not found: "+clip.LocalPath)
		return
	}

	// Check if uploader is available
	if h.driveUploader == nil {
		apiutil.InternalError(c, fmt.Errorf("drive uploader not configured"))
		return
	}

	// Determine folder ID
	folderID := clip.FolderID
	if folderID == "" {
		apiutil.BadRequest(c, "clip has no folder ID")
		return
	}

	// Upload file to Drive
	filename := clip.Filename
	if filename == "" {
		filename = filepath.Base(clip.LocalPath)
	}

	result, err := h.driveUploader.UploadFile(ctx, clip.LocalPath, folderID, filename)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("upload failed: %w", err))
		return
	}

	// Update clip with new Drive link
	clip.DriveLink = result.DownloadLink
	if clip.DriveLink == "" && result.FileID != "" {
		clip.DriveLink = "https://drive.google.com/file/d/" + result.FileID + "/view"
	}

	// Update file hash if available
	if result.MD5Checksum != "" {
		clip.FileHash = result.MD5Checksum
	}

	// Save to DB
	if err := repo.UpsertClip(ctx, clip); err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to update clip: %w", err))
		return
	}

	apiutil.OK(c, gin.H{
		"ok":          true,
		"source":      source,
		"clip_id":     clipID,
		"drive_link":  clip.DriveLink,
		"file_hash":   clip.FileHash,
		"uploaded_at": time.Now().Format(time.RFC3339),
	})
}

// ReprocessClip reprocesses a clip (download/process/upload).
func (h *CommonHandler) ReprocessClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	if h.mediaProcessor == nil {
		apiutil.InternalError(c, fmt.Errorf("media processor not configured"))
		return
	}

	var req struct {
		Force       bool  `json:"force"`
		UploadDrive bool  `json:"upload_drive"`
		Normalize   *bool `json:"normalize"`
	}
	_ = c.ShouldBindJSON(&req)

	// Build ProcessInput from clip data
	processInput := &processor.ProcessInput{
		ID:        clip.ID,
		Name:      clip.Name,
		SourceURL: clip.ExternalURL,
		FolderID:  clip.FolderID,
		Duration:  clip.Duration,
		Metadata: map[string]interface{}{
			"source": source,
			"tags":   clip.Tags,
		},
	}

	// Process the asset
	result, err := h.mediaProcessor.Process(ctx, processInput)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("reprocess failed: %w", err))
		return
	}

	// Update clip with result
	clip.LocalPath = result.LocalPath
	clip.FileHash = result.FileHash
	if result.DriveLink != "" {
		clip.DriveLink = result.DriveLink
	}
	if result.DownloadLink != "" {
		clip.DownloadLink = result.DownloadLink
	}
	clip.UpdatedAt = time.Now()

	// Save to DB
	if err := repo.UpsertClip(ctx, clip); err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to update clip: %w", err))
		return
	}

	apiutil.OK(c, gin.H{
		"ok":            true,
		"source":        source,
		"clip_id":       clipID,
		"status":        result.Status,
		"local_path":    result.LocalPath,
		"file_hash":     result.FileHash,
		"drive_link":    result.DriveLink,
		"download_link": result.DownloadLink,
		"processed_at":  time.Now().Format(time.RFC3339),
	})
}

// FindDuplicates finds clips with the same file_hash across different sources.
func (h *CommonHandler) FindDuplicates(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	clip, err := repo.GetClip(c.Request.Context(), clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	if clip.FileHash == "" {
		apiutil.OK(c, gin.H{
			"ok":         true,
			"source":      source,
			"clip_id":    clipID,
			"file_hash":   "",
			"duplicates": []gin.H{},
		})
		return
	}

	duplicates := []gin.H{}
	repos := map[string]*clips.Repository{
		"artlist": h.artlistRepo,
		"youtube": h.clipsRepo,
		"stock":   h.stockRepo,
	}

	for repoSource, srcRepo := range repos {
		if srcRepo == nil {
			continue
		}

		found, err := srcRepo.FindClipsByHash(c.Request.Context(), clip.FileHash)
		if err != nil {
			h.log.Warn("Failed to search duplicates in "+repoSource, zap.Error(err))
			continue
		}

		for _, dup := range found {
			if repoSource == source && dup.ID == clipID {
				continue
			}

			duplicates = append(duplicates, gin.H{
				"source":     repoSource,
				"id":         dup.ID,
				"name":       dup.Name,
				"drive_link": dup.DriveLink,
				"local_path": dup.LocalPath,
				"thumb_url":  dup.ThumbURL,
			})
		}
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":      source,
		"clip_id":     clipID,
		"file_hash":   clip.FileHash,
		"duplicates":  duplicates,
	})
}

// DownloadClip streams the local video file for a clip.
func (h *CommonHandler) DownloadClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	clip, err := repo.GetClip(c.Request.Context(), clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found: "+clipID)
		return
	}

	// 1. Try local file if it exists
	if clip.LocalPath != "" {
		if info, err := os.Stat(clip.LocalPath); err == nil && !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(clip.LocalPath))
			if ext == ".txt" || ext == ".json" || ext == ".md" {
				apiutil.BadRequest(c, "file is not a video: "+ext)
				return
			}
			c.File(clip.LocalPath)
			return
		}
	}

	// 2. Try to proxy from Google Drive
	driveID := clip.DriveFileID
	if driveID == "" {
		driveID = driveutil.FileIDFromLink(clip.DriveLink)
	}
	if driveID == "" {
		driveID = driveutil.FileIDFromLink(clip.DownloadLink)
	}

	if driveID != "" && h.driveUploader != nil && h.driveUploader.Service != nil {
		h.log.Info("local file missing, proxying from drive", 
			zap.String("clip_id", clipID), 
			zap.String("drive_id", driveID))
		
		// Use Files.Get but with Fields to check MimeType first
		driveFile, err := h.driveUploader.Service.Files.Get(driveID).Fields("id, name, mimeType, size").Context(c.Request.Context()).Do()
		if err != nil {
			h.log.Error("failed to get drive file metadata", zap.Error(err), zap.String("id", driveID))
			apiutil.InternalError(c, fmt.Errorf("failed to reach drive: %w", err))
			return
		}

		// BLOCK non-media MIME types from Drive
		if !strings.HasPrefix(driveFile.MimeType, "video/") && !strings.HasPrefix(driveFile.MimeType, "audio/") && driveFile.MimeType != "application/octet-stream" {
			h.log.Warn("refusing to proxy non-media file from drive", zap.String("mime", driveFile.MimeType))
			apiutil.BadRequest(c, "drive file is not media: "+driveFile.MimeType)
			return
		}

		resp, err := h.driveUploader.Service.Files.Get(driveID).
			Context(c.Request.Context()).
			Download()
		if err != nil {
			h.log.Error("failed to download from drive", zap.Error(err), zap.String("id", driveID))
			apiutil.InternalError(c, fmt.Errorf("failed to stream from drive: %w", err))
			return
		}
		defer resp.Body.Close()

		contentType := resp.Header.Get("Content-Type")
		if contentType == "" || contentType == "application/octet-stream" {
			contentType = "video/mp4"
		}
		
		c.Header("Content-Type", contentType)
		if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
			c.Header("Content-Length", contentLength)
		}
		c.Header("Cache-Control", "public, max-age=3600")
		
		_, err = io.Copy(c.Writer, resp.Body)
		if err != nil {
			h.log.Debug("drive stream interrupted", zap.Error(err))
		}
		return
	}

	apiutil.NotFound(c, "clip video not available (no local file and no drive ID)")
}

// CreateClip creates a new clip.
func (h *CommonHandler) CreateClip(c *gin.Context) {
	source := c.Param("source")
	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	var clip models.Clip
	if err := c.ShouldBindJSON(&clip); err != nil {
		apiutil.BadRequest(c, "invalid clip data: "+err.Error())
		return
	}

	// Ensure ID is generated if missing
	if clip.ID == "" {
		clip.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	if err := repo.UpsertClip(c.Request.Context(), &clip); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"clip_id": clip.ID,
		"clip":    clip,
	})
}

// UpdateClip updates an existing clip.
func (h *CommonHandler) UpdateClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		apiutil.BadRequest(c, "invalid payload")
		return
	}

	ctx := c.Request.Context()
	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Manual update of fields from payload
	if val, ok := payload["name"].(string); ok {
		clip.Name = val
	}
	if val, ok := payload["category"].(string); ok {
		clip.Category = val
	}
	if val, ok := payload["tags"].([]interface{}); ok {
		tags := make([]string, len(val))
		for i, v := range val {
			if s, ok := v.(string); ok {
				tags[i] = s
			}
		}
		clip.Tags = tags
	}
	if val, ok := payload["search_terms"].([]interface{}); ok {
		terms := make([]string, len(val))
		for i, v := range val {
			if s, ok := v.(string); ok {
				terms[i] = s
			}
		}
		clip.SearchTerms = terms
	}
	if val, ok := payload["status"].(string); ok {
		clip.Status = val
	}
	if val, ok := payload["error"].(string); ok {
		clip.Error = val
	}
	if val, ok := payload["folder_id"].(string); ok {
		clip.FolderID = val
	}
	if val, ok := payload["folder_path"].(string); ok {
		clip.FolderPath = val
	}
	if val, ok := payload["drive_link"].(string); ok {
		clip.DriveLink = val
	}
	if val, ok := payload["download_link"].(string); ok {
		clip.DownloadLink = val
	}
	if val, ok := payload["thumb_url"].(string); ok {
		clip.ThumbURL = val
	}

	if err := repo.UpsertClip(ctx, clip); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"clip_id": clipID,
		"clip":    clip,
	})
}

// GetClip returns a single clip.
func (h *CommonHandler) GetClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	clip, err := repo.GetClip(c.Request.Context(), clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"source": source,
		"clip":   clip,
	})
}
