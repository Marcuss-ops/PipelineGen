package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/apiutil"
)

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
