package media

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/pkg/apiutil"
	driveutil "velox/go-master/pkg/drive"
	"velox/go-master/pkg/models"
)

// DownloadClip streams the local video file for a clip.
func (h *CommonHandler) DownloadClip(c *gin.Context) {
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
