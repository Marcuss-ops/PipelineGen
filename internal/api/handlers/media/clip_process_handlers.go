package media

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/core/processor"
	"velox/go-master/pkg/apiutil"
)

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
