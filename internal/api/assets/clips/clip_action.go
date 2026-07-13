// Package clips — clip_action.go: Action cluster (Split 3, not yet
// landed on a dedicated *ActionHandler receiver).
//
// Split 4 (June 2026, override ADR 0009): ReprocessClip moved into
// ops.go (Ops cluster — uses only reprocessUC). DownloadClip,
// ReuploadClip, FindDuplicates remain on *Handler until Split 3 = ActionReceiver
// lands; per the discovery matrix: DownloadClip uses downloadUC +
// driveAdmin; ReuploadClip uses reuploadUC; FindDuplicates uses
// assetRepo + searchAggregator. None of those three uc instances
// are in OpsDeps, so they do not migrate here.
package clips

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// DownloadClip streams the local video file for a clip.
func (h *Handler) DownloadClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	result, err := h.downloadUC.Resolve(c.Request.Context(), source, clipID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			apiutil.NotFound(c, err.Error())
		} else {
			apiutil.InternalError(c, err)
		}
		return
	}

	// 1. Try local file if it exists
	if result.Source == appclips.DownloadSourceLocal {
		if info, statErr := os.Stat(result.LocalPath); statErr == nil && !info.IsDir() {
			c.File(result.LocalPath)
			return
		}
	}

	// 2. Try to proxy from Google Drive
	if result.Source == appclips.DownloadSourceDrive && h.driveAdmin != nil {
		// GetFileMeta + DownloadFile are drive.Reader methods;
		// type-assert from drive.Admin (both satisfied by *drive.Uploader).
		reader, ok := h.driveAdmin.(drive.Reader)
		if !ok {
			apiutil.InternalError(c, fmt.Errorf("drive admin does not support file downloads (Reader interface not satisfied)"))
			return
		}

		h.log.Info("local file missing, proxying from drive",
			zap.String("clip_id", clipID),
			zap.String("drive_id", result.DriveID))

		// Check mime type first
		meta, metaErr := reader.GetFileMeta(c.Request.Context(), result.DriveID)
		if metaErr != nil {
			h.log.Error("failed to get drive file metadata", zap.Error(metaErr), zap.String("id", result.DriveID))
			apiutil.InternalError(c, fmt.Errorf("failed to reach drive: %w", metaErr))
			return
		}

		// BLOCK non-media MIME types from Drive
		if !strings.HasPrefix(meta.MimeType, "video/") && !strings.HasPrefix(meta.MimeType, "audio/") && meta.MimeType != "application/octet-stream" {
			h.log.Warn("refusing to proxy non-media file from drive", zap.String("mime", meta.MimeType))
			apiutil.BadRequest(c, "drive file is not media: "+meta.MimeType)
			return
		}

		body, contentType, dlErr := reader.DownloadFile(c.Request.Context(), result.DriveID)
		if dlErr != nil {
			h.log.Error("failed to download from drive", zap.Error(dlErr), zap.String("id", result.DriveID))
			apiutil.InternalError(c, fmt.Errorf("failed to stream from drive: %w", dlErr))
			return
		}
		defer body.Close()

		if contentType == "" || contentType == "application/octet-stream" {
			contentType = "video/mp4"
		}

		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", "public, max-age=3600")

		_, copyErr := io.Copy(c.Writer, body)
		if copyErr != nil {
			h.log.Debug("drive stream interrupted", zap.Error(copyErr))
		}
		return
	}

	apiutil.NotFound(c, "clip video not available (no local file and no drive ID)")
}

// ReuploadClip reuploads a clip to Drive.
func (h *Handler) ReuploadClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	// P0.5 (June 2026): ReuploadClip now delegates to the
	// ReuploadUseCase — business logic (folder resolution,
	// Drive upload, hash update, dispatcher persistence) lives
	// in internal/application/clips/reupload_usecase.go.
	// The handler is thin transport only, per AGENTS.md Pattern 8.
	if h.reuploadUC == nil {
		apiutil.InternalError(c, fmt.Errorf("reupload use case not wired"))
		return
	}

	result, err := h.reuploadUC.Execute(c.Request.Context(), appclips.ReuploadRequest{
		Source: source,
		ClipID: clipID,
	})
	if err != nil {
		switch {
		case errors.Is(err, appclips.ErrReuploadNotFound):
			apiutil.NotFound(c, "clip not found")
		case errors.Is(err, appclips.ErrReuploadNoLocalPath):
			apiutil.BadRequest(c, "clip has no local path")
		case errors.Is(err, appclips.ErrReuploadLocalFileMissing):
			apiutil.BadRequest(c, err.Error())
		case errors.Is(err, appclips.ErrReuploadFolderResolutionFailed):
			apiutil.BadRequest(c, "clip has no folder ID and dynamic resolution failed (check local path format)")
		case errors.Is(err, appclips.ErrReuploadDispatcherUnavailable):
			apiutil.Error(c, 503, err.Error())
		default:
			apiutil.InternalError(c, err)
		}
		return
	}

	apiutil.OK(c, gin.H{
		"ok":          result.OK,
		"source":      result.Source,
		"clip_id":     result.ClipID,
		"drive_link":  result.DriveLink,
		"file_hash":   result.FileHash,
		"uploaded_at": result.UploadedAt,
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

	// Wave 4 (July 2026): FindDuplicates uses the canonical
	// duplicates.Finder capability. The finder fans out hash lookups to
	// registered sources and returns operator-facing DuplicateMatch rows
	// that include LocalPath/DriveLink (PR-SEARCH-DRIVELINK).
	if h.duplicateFinder == nil {
		h.log.Error("FindDuplicates: DuplicateFinder not wired")
		apiutil.Error(c, 503, "duplicate finder not available")
		return
	}

	matches, findErr := h.duplicateFinder.Find(c.Request.Context(), clip.FileHash())
	if findErr != nil {
		apiutil.InternalError(c, fmt.Errorf("duplicateFinder.Find: %w", findErr))
		return
	}

	duplicates := []gin.H{}
	for _, m := range matches {
		if m.Source == source && m.AssetID == clipID {
			continue
		}
		duplicates = append(duplicates, gin.H{
			"source":     m.Source,
			"id":         m.AssetID,
			"name":       m.Name,
			"drive_link": m.DriveLink,
			"local_path": m.LocalPath,
			"thumb_url":  m.ThumbnailURL,
		})
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"clip_id":    clipID,
		"file_hash":  clip.FileHash(),
		"duplicates": duplicates,
	})
}
