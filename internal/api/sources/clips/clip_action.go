package clips

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ReprocessClip reprocesses a clip (download/process/upload).
func (h *Handler) ReprocessClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	if h.assetRepo == nil {
		apiutil.InternalError(c, fmt.Errorf("asset repository not available"))
		return
	}

	ctx := c.Request.Context()
	clip, err := h.assetRepo.Get(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}
	if clip == nil {
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
		SourceURL: clip.SourceURL,
		FolderID:  clip.FolderID(),
		Duration:  int(clip.Duration.Milliseconds()),
		Metadata: map[string]any{
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
	clip.SetLocalPath(result.LocalPath)
	clip.SetFileHash(result.FileHash)
	if result.DriveLink != "" {
		clip.SetDriveLink(result.DriveLink)
	}
	if result.DownloadLink != "" {
		clip.SetDownloadLink(result.DownloadLink)
	}
	clip.UpdatedAt = time.Now()

	// Save to DB
	if err := h.assetRepo.Upsert(ctx, clip); err != nil {
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
		"processed_at":  timeutil.FormatRFC3339(time.Now()),
	})
}

// DownloadClip streams the local video file for a clip.
func (h *Handler) DownloadClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	var clip *asset.Asset

	// Handle Voiceover source — use canonical converter directly.
	if strings.ToLower(source) == "voiceover" && h.voiceoverRepo != nil {
		rec, getErr := h.voiceoverRepo.GetByID(c.Request.Context(), clipID)
		if getErr != nil {
			apiutil.NotFound(c, "voiceover not found: "+clipID)
			return
		}
		clip = artifacts.VoiceoverRecordToClip(rec)
	} else {
		if h.assetRepo == nil {
			apiutil.InternalError(c, fmt.Errorf("asset repository not available"))
			return
		}

		var getErr error
		clip, getErr = h.assetRepo.Get(c.Request.Context(), clipID)
		if getErr != nil {
			apiutil.NotFound(c, "clip not found: "+clipID)
			return
		}
		if clip == nil {
			apiutil.NotFound(c, "clip not found: "+clipID)
			return
		}
	}

	// 1. Try local file if it exists
	if clip.LocalPath() != "" {
		if info, err := os.Stat(clip.LocalPath()); err == nil && !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(clip.LocalPath()))
			if ext == ".txt" || ext == ".json" || ext == ".md" {
				apiutil.BadRequest(c, "file is not a video: "+ext)
				return
			}
			c.File(clip.LocalPath())
			return
		}
	}

	// 2. Try to proxy from Google Drive
	driveID := clip.DriveFileID()
	if driveID == "" {
		driveID = driveutil.FileIDFromLink(clip.DriveLink())
	}
	if driveID == "" {
		driveID = driveutil.FileIDFromLink(clip.DownloadLink())
	}

	if driveID != "" && h.driveUploader != nil {
		h.log.Info("local file missing, proxying from drive",
			zap.String("clip_id", clipID),
			zap.String("drive_id", driveID))

		// Check mime type first
		meta, err := h.driveUploader.GetFileMeta(c.Request.Context(), driveID)
		if err != nil {
			h.log.Error("failed to get drive file metadata", zap.Error(err), zap.String("id", driveID))
			apiutil.InternalError(c, fmt.Errorf("failed to reach drive: %w", err))
			return
		}

		// BLOCK non-media MIME types from Drive
		if !strings.HasPrefix(meta.MimeType, "video/") && !strings.HasPrefix(meta.MimeType, "audio/") && meta.MimeType != "application/octet-stream" {
			h.log.Warn("refusing to proxy non-media file from drive", zap.String("mime", meta.MimeType))
			apiutil.BadRequest(c, "drive file is not media: "+meta.MimeType)
			return
		}

		body, contentType, err := h.driveUploader.DownloadFile(c.Request.Context(), driveID)
		if err != nil {
			h.log.Error("failed to download from drive", zap.Error(err), zap.String("id", driveID))
			apiutil.InternalError(c, fmt.Errorf("failed to stream from drive: %w", err))
			return
		}
		defer body.Close()

		if contentType == "" || contentType == "application/octet-stream" {
			contentType = "video/mp4"
		}

		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", "public, max-age=3600")

		_, err = io.Copy(c.Writer, body)
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

	if h.assetRepo == nil {
		apiutil.InternalError(c, fmt.Errorf("asset repository not available"))
		return
	}

	ctx := c.Request.Context()
	clip, err := h.assetRepo.Get(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}
	if clip == nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Check local file
	if clip.LocalPath() == "" {
		apiutil.BadRequest(c, "clip has no local path")
		return
	}

	if _, err := os.Stat(clip.LocalPath()); err != nil {
		apiutil.BadRequest(c, "local file not found: "+clip.LocalPath())
		return
	}

	// Check if uploader is available
	if h.driveUploader == nil {
		apiutil.InternalError(c, fmt.Errorf("drive uploader not configured"))
		return
	}

	// Determine folder ID
	folderID := clip.FolderID()
	if folderID == "" {
		// Attempt to resolve folder ID dynamically based on source and local path
		var rootID string
		var pathMarker string
		switch source {
		case "youtube":
			rootID = h.cfg.Drive.ClipsFolder()
			pathMarker = "/clips/"
		case "artlist":
			rootID = h.cfg.Drive.ArtlistFolder()
			pathMarker = "/artlist/"
		case "stock":
			rootID = h.cfg.Drive.StockFolder()
			pathMarker = "/stock/"
		}

		if rootID != "" && pathMarker != "" && strings.Contains(clip.LocalPath(), pathMarker) {
			idx := strings.Index(clip.LocalPath(), pathMarker)
			relPath := clip.LocalPath()[idx+len(pathMarker):]
			dir := filepath.Dir(relPath)
			if dir != "." && dir != "" {
				segments := strings.Split(dir, string(filepath.Separator))
				currentID := rootID
				for _, seg := range segments {
					if seg == "" {
						continue
					}
					id, err := h.driveUploader.GetOrCreateFolder(ctx, seg, currentID)
					if err != nil {
						apiutil.InternalError(c, fmt.Errorf("failed to create drive folder %s: %w", seg, err))
						return
					}
					currentID = id
				}
				folderID = currentID
				clip.SetFolderID(folderID) // save it for later
			}
		}

		if folderID == "" {
			apiutil.BadRequest(c, "clip has no folder ID and dynamic resolution failed (check local path format)")
			return
		}
	}

	// Upload file to Drive
	filename := clip.Filename
	if filename == "" {
		filename = filepath.Base(clip.LocalPath())
	}

	result, err := h.driveUploader.UploadFile(ctx, clip.LocalPath(), folderID, filename)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("upload failed: %w", err))
		return
	}

	// Update clip with new Drive link
	driveLinkVal := result.DownloadLink
	if driveLinkVal == "" && result.FileID != "" {
		driveLinkVal = driveutil.FileURLFromID(result.FileID)
	}
	clip.SetDriveLink(driveLinkVal)

	// Update file hash if available
	if result.MD5Checksum != "" {
		clip.SetFileHash(result.MD5Checksum)
	}

	// Save to DB
	if err := h.assetRepo.Upsert(ctx, clip); err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to update clip: %w", err))
		return
	}

	apiutil.OK(c, gin.H{
		"ok":          true,
		"source":      source,
		"clip_id":     clipID,
		"drive_link":  clip.DriveLink(),
		"file_hash":   clip.FileHash(),
		"uploaded_at": timeutil.FormatRFC3339(time.Now()),
	})
}

// FindDuplicates finds clips with the same file_hash across different sources.
func (h *Handler) FindDuplicates(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	if h.assetRepo == nil {
		apiutil.InternalError(c, fmt.Errorf("asset repository not available"))
		return
	}

	clip, err := h.assetRepo.Get(c.Request.Context(), clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}
	if clip == nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	if clip.FileHash() == "" {
		apiutil.OK(c, gin.H{
			"ok":         true,
			"source":     source,
			"clip_id":    clipID,
			"file_hash":  "",
			"duplicates": []gin.H{},
		})
		return
	}

	duplicates := []gin.H{}
	repos := map[string]*sqlite.ClipsRepository{
		"artlist": h.artlistRepo,
		"youtube": h.clipsRepo,
		"stock":   h.stockRepo,
	}

	for repoSource, srcRepo := range repos {
		if srcRepo == nil {
			continue
		}

		found, err := srcRepo.FindClipsByHash(c.Request.Context(), clip.FileHash())
		if err != nil {
			h.log.Warn("Failed to search duplicates in "+repoSource, zap.Error(err))
			continue
		}

		for _, dup := range found {
			if repoSource == source && dup.ID == clipID {
				continue
			}

			canonDup := dup
			duplicates = append(duplicates, gin.H{
				"source":     repoSource,
				"id":         canonDup.ID,
				"name":       canonDup.Name,
				"drive_link": canonDup.DriveLink(),
				"local_path": canonDup.LocalPath(),
				"thumb_url":  canonDup.ThumbnailURL,
			})
		}
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"clip_id":    clipID,
		"file_hash":  clip.FileHash(),
		"duplicates": duplicates,
	})
}
