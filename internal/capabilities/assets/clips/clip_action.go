// Package clips — action transport for download, reupload and duplicate lookup.
package clips

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/duplicates"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// httpStatusConflict is 409 Conflict. Spelled out so the intent is greppable and
// the constant does not depend on which net/http version is vendored.
const httpStatusConflict = 409

// AssetJobStatusLookup reports the in-flight job state for an asset when one
// exists. It is optional: when it is not wired, the download handler still
// answers 409 (never a misleading 404) but cannot name the real status.
type AssetJobStatusLookup interface {
	// LatestJobStatus returns the latest job status for assetID, the retry
	// count, and whether any job was found at all.
	LatestJobStatus(ctx context.Context, assetID string) (status string, retryCount int, found bool)
}

// ActionDeps contains only the collaborators consumed by the three action
// endpoints. Use cases are constructed in internal/app, never in transport.
type ActionDeps struct {
	AssetRepo       AssetReader
	DriveAdmin      appclips.ClipDriveUploaderPort
	DuplicateFinder *duplicates.Finder
	DownloadUC      *appclips.DownloadUseCase
	ReuploadUC      *appclips.ReuploadUseCase
	// JobStatus is optional. When wired, a download of an asset whose render
	// job is still running answers 409 with the real status instead of a
	// generic one.
	JobStatus AssetJobStatusLookup
	Log       *zap.Logger
}

// ActionHandler owns the publication and duplicate-query HTTP endpoints.
type ActionHandler struct {
	assetRepo       AssetReader
	driveAdmin      appclips.ClipDriveUploaderPort
	duplicateFinder *duplicates.Finder
	downloadUC      *appclips.DownloadUseCase
	reuploadUC      *appclips.ReuploadUseCase
	jobStatus       AssetJobStatusLookup
	log             *zap.Logger
}

func NewActionHandler(d ActionDeps) *ActionHandler {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	return &ActionHandler{
		assetRepo:       d.AssetRepo,
		driveAdmin:      d.DriveAdmin,
		duplicateFinder: d.DuplicateFinder,
		downloadUC:      d.DownloadUC,
		reuploadUC:      d.ReuploadUC,
		jobStatus:       d.JobStatus,
		log:             d.Log,
	}
}

// DownloadClip streams the local video file for a clip.
func (h *ActionHandler) DownloadClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")
	if h.downloadUC == nil {
		apiutil.InternalError(c, fmt.Errorf("download use case not wired"))
		return
	}

	result, err := h.downloadUC.Resolve(c.Request.Context(), source, clipID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			// The asset row may be absent only because its job has not
			// committed it yet. Distinguish "does not exist" from "not
			// available yet": a running job answers 409, not 404.
			if h.respondIfNotReady(c, clipID, result) {
				return
			}
			apiutil.NotFound(c, err.Error())
		} else {
			apiutil.InternalError(c, err)
		}
		return
	}

	// Every download is per-client and must not be cached by shared proxies.
	// Set this before c.File as well: ServeFile preserves headers already set.
	c.Header("Cache-Control", "private, no-store")

	if result.Source == appclips.DownloadSourceLocal {
		if info, statErr := os.Stat(result.LocalPath); statErr == nil && !info.IsDir() {
			c.File(result.LocalPath)
			return
		}
	}

	if result.Source == appclips.DownloadSourceDrive && h.driveAdmin != nil {
		h.log.Info("local file missing, proxying from drive",
			zap.String("clip_id", clipID),
			zap.String("drive_id", result.DriveID))

		meta, metaErr := h.driveAdmin.GetFileMeta(c.Request.Context(), result.DriveID)
		if metaErr != nil {
			h.log.Error("failed to get drive file metadata", zap.Error(metaErr), zap.String("id", result.DriveID))
			apiutil.InternalError(c, fmt.Errorf("failed to reach drive: %w", metaErr))
			return
		}
		if meta == nil {
			meta = &appclips.ClipDriveFileMetaDTO{}
		}
		if !strings.HasPrefix(meta.MimeType, "video/") && !strings.HasPrefix(meta.MimeType, "audio/") && meta.MimeType != "application/octet-stream" {
			h.log.Warn("refusing to proxy non-media file from drive", zap.String("mime", meta.MimeType))
			apiutil.BadRequest(c, "drive file is not media: "+meta.MimeType)
			return
		}

		filename := meta.Name
		if strings.TrimSpace(filename) == "" {
			filename = clipID
		}
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

		rangeHeader := strings.TrimSpace(c.GetHeader("Range"))
		if rangeHeader != "" && meta.Size > 0 {
			start, end, ok := parseRangeHeader(rangeHeader, meta.Size)
			if !ok {
				c.Header("Content-Range", fmt.Sprintf("bytes */%d", meta.Size))
				apiutil.Error(c, 416, "invalid range")
				return
			}
			if rangeReader, ok := h.driveAdmin.(interface {
				DownloadFileRange(context.Context, string, int64, int64) (io.ReadCloser, string, error)
			}); ok {
				body, contentType, dlErr := rangeReader.DownloadFileRange(c.Request.Context(), result.DriveID, start, end)
				if dlErr == nil {
					defer func() { _ = body.Close() }()
					if contentType == "" || contentType == "application/octet-stream" {
						contentType = "video/mp4"
					}
					c.Header("Content-Type", contentType)
					c.Header("Accept-Ranges", "bytes")
					c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, meta.Size))
					c.Header("Content-Length", strconv.FormatInt(end-start+1, 10))
					c.Status(206)
					if _, copyErr := io.Copy(c.Writer, body); copyErr != nil {
						h.log.Debug("drive range stream interrupted", zap.Error(copyErr))
					}
					return
				}
				if !errors.Is(dlErr, appclips.ErrDriveRangeUnsupported) {
					h.log.Error("failed to range-download from drive", zap.Error(dlErr), zap.String("id", result.DriveID))
					apiutil.InternalError(c, fmt.Errorf("failed to stream range from drive: %w", dlErr))
					return
				}
			}
		}

		body, contentType, dlErr := h.driveAdmin.DownloadFile(c.Request.Context(), result.DriveID)
		if dlErr != nil {
			h.log.Error("failed to download from drive", zap.Error(dlErr), zap.String("id", result.DriveID))
			apiutil.InternalError(c, fmt.Errorf("failed to stream from drive: %w", dlErr))
			return
		}
		defer func() { _ = body.Close() }()

		if contentType == "" || contentType == "application/octet-stream" {
			contentType = "video/mp4"
		}
		c.Header("Content-Type", contentType)
		if meta.Size > 0 {
			c.Header("Content-Length", strconv.FormatInt(meta.Size, 10))
		}
		if _, ok := h.driveAdmin.(interface {
			DownloadFileRange(context.Context, string, int64, int64) (io.ReadCloser, string, error)
		}); ok && rangeHeader == "" {
			c.Header("Accept-Ranges", "bytes")
		} else {
			c.Header("Accept-Ranges", "none")
		}
		if _, copyErr := io.Copy(c.Writer, body); copyErr != nil {
			h.log.Debug("drive stream interrupted", zap.Error(copyErr))
		}
		return
	}

	// The asset row EXISTS (Resolve succeeded); only its artifact is missing.
	// Answering 404 here made a pending render look like a nonexistent asset.
	if h.respondIfNotReady(c, clipID, result) {
		return
	}
	h.respondNotReady(c, "RUNNING", 1)
}

// parseRangeHeader parses the single-range form used by resumable clients:
// "bytes=start-", "bytes=start-end", or "bytes=-suffix". Multi-range
// requests are rejected because the download is a single media stream.
func parseRangeHeader(raw string, size int64) (start, end int64, ok bool) {
	if size <= 0 || !strings.HasPrefix(raw, "bytes=") {
		return 0, 0, false
	}
	spec := strings.TrimSpace(strings.TrimPrefix(raw, "bytes="))
	if spec == "" || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	startPart := strings.TrimSpace(parts[0])
	endPart := strings.TrimSpace(parts[1])
	if startPart == "" {
		suffix, err := strconv.ParseInt(endPart, 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, false
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true
	}
	start, err := strconv.ParseInt(startPart, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end = size - 1
	if endPart != "" {
		parsed, err := strconv.ParseInt(endPart, 10, 64)
		if err != nil || parsed < start {
			return 0, 0, false
		}
		if parsed < end {
			end = parsed
		}
	}
	return start, end, true
}

// respondIfNotReady answers 409 Conflict when the asset's video artifact is not
// available yet, and reports whether it wrote the response.
//
// 409 is the honest status: the resource is known but cannot be served yet, and
// the body tells the caller it is a timing problem (`status`, `retry_count`),
// not a missing asset. A 404 here was misleading and sent callers looking for a
// bug that did not exist.
func (h *ActionHandler) respondIfNotReady(c *gin.Context, clipID string, result *appclips.DownloadResult) bool {
	if h.jobStatus != nil {
		if status, retryCount, found := h.jobStatus.LatestJobStatus(c.Request.Context(), clipID); found {
			h.respondNotReady(c, status, retryCount)
			return true
		}
	}
	if result == nil || result.Clip == nil {
		// No asset row and no job record: this is a genuine miss.
		return false
	}
	// The asset row exists but its artifact is not materialized yet.
	h.respondNotReady(c, "RUNNING", 1)
	return true
}

// respondNotReady writes the canonical 409 body. retryCount is the job's retry
// count when known, otherwise 1 ("a retry is expected").
func (h *ActionHandler) respondNotReady(c *gin.Context, status string, retryCount int) {
	if retryCount <= 0 {
		retryCount = 1
	}
	if strings.TrimSpace(status) == "" {
		status = "RUNNING"
	}
	c.Header("Retry-After", "2")
	c.JSON(httpStatusConflict, gin.H{
		"ok":          false,
		"error":       "clip video not available yet",
		"status":      status,
		"retry_count": retryCount,
	})
}

// ReuploadClip reuploads a clip to Drive through the application use case.
func (h *ActionHandler) ReuploadClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")
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
		"ok":             result.OK,
		"source":         result.Source,
		"clip_id":        result.ClipID,
		"drive_link":     result.DriveLink,
		"content_sha256": result.ContentSHA256,
		"uploaded_at":    result.UploadedAt,
	})
}

// FindDuplicates finds clips with the same file hash across registered sources.
func (h *ActionHandler) FindDuplicates(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	if h.assetRepo == nil {
		apiutil.InternalError(c, fmt.Errorf("asset repository not available"))
		return
	}
	clip, err := h.assetRepo.Get(c.Request.Context(), clipID)
	if err != nil || clip == nil {
		apiutil.NotFound(c, "clip not found")
		return
	}
	if clip.ContentAddress() == "" {
		apiutil.OK(c, gin.H{
			"ok": true, "source": source, "clip_id": clipID,
			"content_sha256": "", "duplicates": []gin.H{},
		})
		return
	}
	if h.duplicateFinder == nil {
		h.log.Error("FindDuplicates: DuplicateFinder not wired")
		apiutil.Error(c, 503, "duplicate finder not available")
		return
	}

	matches, findErr := h.duplicateFinder.Find(c.Request.Context(), clip.ContentAddress())
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
			"source": m.Source, "id": m.AssetID, "name": m.Name,
			"drive_link": m.DriveLink, "local_path": m.LocalPath,
			"thumb_url": m.ThumbnailURL,
		})
	}
	apiutil.OK(c, gin.H{
		"ok": true, "source": source, "clip_id": clipID,
		"content_sha256": clip.ContentHash(), "duplicates": duplicates,
	})
}
