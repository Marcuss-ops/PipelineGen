package clips

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ReprocessClip reprocesses a clip (download/process/upload).
func (h *Handler) ReprocessClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	var req struct {
		Force       bool  `json:"force"`
		UploadDrive bool  `json:"upload_drive"`
		Normalize   *bool `json:"normalize"`
	}
	_ = c.ShouldBindJSON(&req)

	result, err := h.reprocessUC.Execute(c.Request.Context(), appclips.ReprocessRequest{
		ClipID:      clipID,
		Source:      source,
		Force:       req.Force,
		UploadDrive: req.UploadDrive,
		Normalize:   req.Normalize,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			apiutil.NotFound(c, err.Error())
		} else {
			apiutil.InternalError(c, err)
		}
		return
	}

	apiutil.OK(c, gin.H{
		"ok":            true,
		"source":        result.Source,
		"clip_id":       result.ClipID,
		"status":        result.Status,
		"local_path":    result.LocalPath,
		"file_hash":     result.FileHash,
		"drive_link":    result.DriveLink,
		"download_link": result.DownloadLink,
		"processed_at":  result.ProcessedAt,
	})
}

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
	if result.Source == appclips.DownloadSourceDrive && h.driveUploader != nil {
		h.log.Info("local file missing, proxying from drive",
			zap.String("clip_id", clipID),
			zap.String("drive_id", result.DriveID))

		// Check mime type first
		meta, metaErr := h.driveUploader.GetFileMeta(c.Request.Context(), result.DriveID)
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

		body, contentType, dlErr := h.driveUploader.DownloadFile(c.Request.Context(), result.DriveID)
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
		// Attempt to resolve folder ID dynamically based on source and local path.
		rootID, pathMarker := h.driveRootForSource(source)

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
	// S3d (June 2026): FindDuplicates routes through the
	// SearchAggregator when wired. The aggregator's HashQuery
	// path fans out a deterministic MD5 hash-match lookup
	// against the registered ClipHashSource adapters. Partial-
	// results semantics: per-source errors are recorded in
	// AggregateResult.ProviderErrors; the legacy direct-repo
	// fallback fires when the aggregator isn't wired
	// (composition root hasn't populated Deps.SearchAggregator
	// OR the registry doesn't carry ClipHashSource adapters).
	if h.searchAggregator != nil {
		res, aggErr := h.searchAggregator.Aggregate(
			c.Request.Context(),
			&providers.SearchQuery{},
			providers.AggregateOptions{
				HashQuery: clip.FileHash(),
				Sources:   []string{"artlist", "youtube", "stock"},
			},
		)
		if aggErr != nil {
			apiutil.InternalError(c, fmt.Errorf("aggregator.Aggregate: %w", aggErr))
			return
		}
		if res != nil {
			for name, e := range res.ProviderErrors {
				h.log.Warn("Failed to search duplicates in "+name, zap.Error(e))
			}
			for _, hit := range res.Hits {
				if hit.SourceSource == source && hit.SourceID == clipID {
					continue
				}
				duplicates = append(duplicates, gin.H{
					"source":     hit.SourceSource,
					"id":         hit.SourceID,
					"name":       hit.Name,
					"drive_link": hit.DriveLink,
					"local_path": hit.LocalPath,
					"thumb_url":  hit.ThumbnailURL,
				})
			}
		}
	} else {
		// Legacy direct-repo fallback when no aggregator is wired.
		// Removal of this branch is gated on the composition root
		// shipping a real SearchAggregator with the three
		// ClipHashSource adapters wired.
		// ARCH-ALLOWLIST: factory-only — S3e (June 2026): the legacy
		// FindDuplicates fallback constructs the bag of repos
		// inline because the aggregator path requires wired
		// CompositionRoot + ClipHashSource adapters. Removal of
		// this branch is the S3d-followup PR-CLIP-DEDUP-MIGRATION
		// task; until then, the marker is the documented allowlist
		// entry per scripts/ci-architectural-checks.sh::Check 8
		// (factory-only). Per AGENTS.md §7 zero-baseline rule, this
		// entry carries explicit owner (clips.Handler) and deadline
		// (post-S3d migration series).
		repos := map[string]*assets.ClipsRepository{
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
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"clip_id":    clipID,
		"file_hash":  clip.FileHash(),
		"duplicates": duplicates,
	})
}
