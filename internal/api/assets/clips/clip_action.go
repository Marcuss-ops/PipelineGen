// Package clips — action transport for download, reupload and duplicate lookup.
package clips

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/duplicates"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ActionDeps contains only the collaborators consumed by the three action
// endpoints. Use cases are constructed in internal/app, never in transport.
type ActionDeps struct {
	AssetRepo       asset.Repository
	DriveAdmin      appclips.ClipDriveUploaderPort
	DuplicateFinder *duplicates.Finder
	DownloadUC      *appclips.DownloadUseCase
	ReuploadUC      *appclips.ReuploadUseCase
	Log             *zap.Logger
}

// ActionHandler owns the publication and duplicate-query HTTP endpoints.
type ActionHandler struct {
	assetRepo       asset.Repository
	driveAdmin      appclips.ClipDriveUploaderPort
	duplicateFinder *duplicates.Finder
	downloadUC      *appclips.DownloadUseCase
	reuploadUC      *appclips.ReuploadUseCase
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
			apiutil.NotFound(c, err.Error())
		} else {
			apiutil.InternalError(c, err)
		}
		return
	}

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
		if !strings.HasPrefix(meta.MimeType, "video/") && !strings.HasPrefix(meta.MimeType, "audio/") && meta.MimeType != "application/octet-stream" {
			h.log.Warn("refusing to proxy non-media file from drive", zap.String("mime", meta.MimeType))
			apiutil.BadRequest(c, "drive file is not media: "+meta.MimeType)
			return
		}

		body, contentType, dlErr := h.driveAdmin.DownloadFile(c.Request.Context(), result.DriveID)
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
		if _, copyErr := io.Copy(c.Writer, body); copyErr != nil {
			h.log.Debug("drive stream interrupted", zap.Error(copyErr))
		}
		return
	}

	apiutil.NotFound(c, "clip video not available (no local file and no drive ID)")
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
		"ok":              result.OK,
		"source":          result.Source,
		"clip_id":         result.ClipID,
		"drive_link":      result.DriveLink,
		"legacy_file_md5": result.LegacyFileMD5,
		"uploaded_at":     result.UploadedAt,
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
	if clip.LegacyFileMD5() == "" {
		apiutil.OK(c, gin.H{
			"ok": true, "source": source, "clip_id": clipID,
			"legacy_file_md5": "", "duplicates": []gin.H{},
		})
		return
	}
	if h.duplicateFinder == nil {
		h.log.Error("FindDuplicates: DuplicateFinder not wired")
		apiutil.Error(c, 503, "duplicate finder not available")
		return
	}

	matches, findErr := h.duplicateFinder.Find(c.Request.Context(), clip.LegacyFileMD5())
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
		"legacy_file_md5": clip.LegacyFileMD5(), "duplicates": duplicates,
	})
}
