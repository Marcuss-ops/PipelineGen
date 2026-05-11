package sources

import (
	"fmt"
	"io"
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

// ReprocessClip reprocesses a clip (download/process/upload).
func (h *Handler) ReprocessClip(c *gin.Context) {
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

// DownloadClip streams the local video file for a clip.
func (h *Handler) DownloadClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	var clip *models.Clip
	var err error

	// Handle Voiceover source
	if strings.ToLower(source) == "voiceover" && h.voiceoverRepo != nil {
		rec, getErr := h.voiceoverRepo.GetByID(c.Request.Context(), clipID)
		if getErr != nil {
			apiutil.NotFound(c, "voiceover not found: "+clipID)
			return
		}
		clip = voiceoverRecordToClip(rec)
	} else {
		repo := h.resolveRepo(source)
		if repo == nil {
			apiutil.BadRequest(c, "invalid source: "+source)
			return
		}

		clip, err = repo.GetClip(c.Request.Context(), clipID)
		if err != nil {
			apiutil.NotFound(c, "clip not found: "+clipID)
			return
		}
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

// ReuploadClip reuploads a clip to Drive.
func (h *Handler) ReuploadClip(c *gin.Context) {
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

// FindDuplicates finds clips with the same file_hash across different sources.
func (h *Handler) FindDuplicates(c *gin.Context) {
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
