package handlers

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// DownloadClip downloads a clip from YouTube and uploads to Drive
func (h *ClipHandler) DownloadClip(c *gin.Context) {
	var req clip.DownloadClipRequest
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

	// Determine target folder
	targetFolderID := h.rootFolderID
	if req.DriveFolder != "" {
		folder, err := client.GetFolderByName(ctx, req.DriveFolder, h.rootFolderID)
		if err == nil && folder != nil {
			targetFolderID = folder.ID
		}
	}

	// If group is specified, ensure group folder exists
	if req.Group != "" {
		groupFolderID, err := client.GetOrCreateFolder(ctx, req.Group, targetFolderID)
		if err == nil {
			targetFolderID = groupFolderID
		}
	}

	// Download from YouTube
	tempDir, err := os.MkdirTemp("", "clip_download_")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to create temp directory",
		})
		return
	}
	defer os.RemoveAll(tempDir)

	// Build yt-dlp command
	outputTemplate := filepath.Join(tempDir, "%(title)s.%(ext)s")
	args := []string{
		"-f", "bestvideo[height<=1080]+bestaudio/best[height<=1080]",
		"--no-playlist",
		"-o", outputTemplate,
	}

	// Add time range if specified
	if req.StartTime != "" && req.EndTime != "" {
		args = append(args, "--download-sections", fmt.Sprintf("*%s-%s", req.StartTime, req.EndTime))
	}

	args = append(args, req.YouTubeURL)

	// Execute download
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error("yt-dlp download failed",
			zap.String("output", string(output)),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": fmt.Sprintf("Download failed: %s", err),
		})
		return
	}

	// Find downloaded file
	files, _ := filepath.Glob(filepath.Join(tempDir, "*"))
	var videoPath string
	for _, f := range files {
		if isVideoFile("", f) {
			videoPath = f
			break
		}
	}

	if videoPath == "" {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "No video file found after download",
		})
		return
	}

	// Upload to Drive
	filename := req.Title
	if filename == "" {
		filename = filepath.Base(videoPath)
	} else {
		filename = filename + filepath.Ext(videoPath)
	}

	fileID, err := client.UploadVideo(ctx, videoPath, targetFolderID, filename)
	if err != nil {
		logger.Error("Failed to upload to Drive", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to upload: " + err.Error(),
		})
		return
	}

	// Invalidate cache
	clip.InvalidateSearchCache()

	logger.Info("Clip downloaded and uploaded",
		zap.String("youtube_url", req.YouTubeURL),
		zap.String("filename", filename),
		zap.String("drive_id", fileID))

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"message":     "Clip downloaded and uploaded successfully",
		"youtube_url": req.YouTubeURL,
		"title":       req.Title,
		"file_id":     fileID,
		"drive_link":  drive.GetDriveLink(fileID),
		"folder_id":   targetFolderID,
	})
}

// UploadClip uploads a clip to Drive
func (h *ClipHandler) UploadClip(c *gin.Context) {
	var req clip.UploadClipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Validate file exists
	if _, err := os.Stat(req.ClipPath); os.IsNotExist(err) {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Clip file not found: " + req.ClipPath,
		})
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

	// Determine target folder
	targetFolderID := h.rootFolderID
	if req.DriveFolder != "" {
		folder, err := client.GetFolderByName(ctx, req.DriveFolder, h.rootFolderID)
		if err == nil && folder != nil {
			targetFolderID = folder.ID
		}
	}

	// If group is specified, ensure group folder exists
	if req.Group != "" {
		groupFolderID, err := client.GetOrCreateFolder(ctx, req.Group, targetFolderID)
		if err == nil {
			targetFolderID = groupFolderID
		}
	}

	// Set filename
	filename := req.Title
	if filename == "" {
		filename = filepath.Base(req.ClipPath)
	} else {
		filename = filename + filepath.Ext(req.ClipPath)
	}

	// Upload to Drive
	fileID, err := client.UploadVideo(ctx, req.ClipPath, targetFolderID, filename)
	if err != nil {
		logger.Error("Failed to upload to Drive", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to upload: " + err.Error(),
		})
		return
	}

	// Invalidate cache
	clip.InvalidateSearchCache()

	logger.Info("Clip uploaded",
		zap.String("clip_path", req.ClipPath),
		zap.String("filename", filename),
		zap.String("drive_id", fileID))

	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"message":    "Clip uploaded successfully",
		"clip_path":  req.ClipPath,
		"title":      req.Title,
		"file_id":    fileID,
		"drive_link": drive.GetDriveLink(fileID),
		"folder_id":  targetFolderID,
	})
}
