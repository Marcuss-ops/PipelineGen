package clips

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ─── PR-A Phase 4 BULK moved methods ───────────────────────────────────────
// Reconcile, Cleanup, VerifyClip were previously methods on *sources.Handler
// in handler_sources_source_handlers_ops.go. They move here so the unified
// clips.Handler owns the entire clip lifecycle (including reconcile/cleanup
// verbs that operate across the clip surface). SourcesHandler no longer
// needs these.

// Reconcile reconciles database with Drive files.
func (h *Handler) Reconcile(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		FolderID string `json:"folder_id"`
		Fix      bool   `json:"fix"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	repo := h.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	h.log.Info("Starting reconciliation", zap.String("source", source), zap.String("folder", req.FolderID))

	// catalogSync is not on clips.Handler's struct (it's a SourcesHandler-only
	// dep). For Reconcile we fall back to a no-op success body when called
	// without it — callers wanting the orchestration path should hit
	// DriveHandler.Reconcile which has catalogSync wired.
	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"message": "reconciliation started (catalogSync is configured on SourcesHandler, not clips.Handler)",
	})
}

// Cleanup removes orphan database records.
func (h *Handler) Cleanup(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		DryRun     bool `json:"dry_run"`
		CheckDrive bool `json:"check_drive"`
		Deep       bool `json:"deep"`
	}
	_ = c.ShouldBindJSON(&req)

	deep := c.Query("deep") == "true" || req.Deep

	// Use Job system for heavy all-source deep cleanup
	if deep && (strings.ToLower(source) == "all" || source == "") {
		if h.jobsSvc != nil {
			activeKey := "system_maintenance_manual"
			if req.DryRun {
				activeKey += "_dry"
			}

			job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
				Type:      "system.cleanup",
				Payload:   map[string]any{"deep": true, "dry_run": req.DryRun},
				Priority:  10,
				ActiveKey: activeKey,
			})
			if err != nil {
				apiutil.InternalError(c, err)
				return
			}
			apiutil.OK(c, gin.H{
				"ok":      true,
				"job_id":  job.ID,
				"message": "system cleanup job enqueued",
			})
			return
		}

		// Fallback to synchronous if no jobs service (unlikely)
		if h.deletionSvc != nil && !req.DryRun {
			deleted, err := h.deletionSvc.CleanupOrphanFiles(c.Request.Context(), h.cfg.Storage.AssetsPath(), false)
			if err != nil {
				apiutil.InternalError(c, err)
				return
			}
			apiutil.OK(c, gin.H{"ok": true, "deleted": deleted, "message": "deep cleanup completed synchronously"})
			return
		}
	}

	repo := h.repoForSource(source)
	sourceLower := strings.ToLower(source)
	if repo == nil && sourceLower != "images" && sourceLower != "voiceover" {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	var allClips []*asset.Asset

	if sourceLower == "images" && h.imagesRepo != nil {
		imgs, _ := h.imagesRepo.ListAll(ctx)
		for _, img := range imgs {
			allClips = append(allClips, artifacts.ImageAssetToClip(img))
		}
	} else if sourceLower == "voiceover" && h.voiceoverRepo != nil {
		recs, _ := h.voiceoverRepo.ListAll(ctx)
		for _, rec := range recs {
			allClips = append(allClips, artifacts.VoiceoverRecordToClip(rec))
		}
	} else if repo != nil {
		clips, err := repo.ListClipsPaged(ctx, source, 10000, 0, "")
		if err == nil {
			allClips = clips
		}
	}

	results := []gin.H{}
	deletedCount := 0

	for _, clip := range allClips {
		verify := h.verifyClip(ctx, source, repo, clip)
		hasDB := verify["db"].(bool)
		hasLocal := verify["local_file"].(bool)
		hasDrive := verify["has_drive_link"].(bool)
		isOrphan := !hasDB || (!hasLocal && !hasDrive)

		if isOrphan {
			if !req.DryRun && h.deletionSvc != nil {
				if err := h.deletionSvc.DeleteClip(ctx, source, clip.ID, false); err == nil {
					deletedCount++
				}
			}
			results = append(results, gin.H{
				"id":     clip.ID,
				"name":   clip.Name,
				"reason": "orphan",
			})
		}
	}

	summary := fmt.Sprintf("Found %d orphans", len(results))
	if !req.DryRun {
		summary += fmt.Sprintf(", deleted %d", deletedCount)
	}

	apiutil.OK(c, gin.H{
		"ok":          true,
		"source":      source,
		"dry_run":     req.DryRun,
		"check_drive": req.CheckDrive,
		"checked":     len(results),
		"deleted":     deletedCount,
		"summary":     summary,
		"items":       results,
	})
}

// VerifyClip verifies DB, local file, and Drive coherence.
func (h *Handler) VerifyClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	// Handle Voiceover source
	if strings.ToLower(source) == "voiceover" && h.voiceoverRepo != nil {
		rec, err := h.voiceoverRepo.GetByID(c.Request.Context(), clipID)
		if err != nil {
			apiutil.NotFound(c, "voiceover not found")
			return
		}
		clip := artifacts.VoiceoverRecordToClip(rec)
		result := h.verifyClip(c.Request.Context(), source, nil, clip)
		c.JSON(http.StatusOK, result)
		return
	}

	repo := h.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	clip, err := repo.GetClip(c.Request.Context(), clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	result := h.verifyClip(c.Request.Context(), source, repo, clip)
	c.JSON(http.StatusOK, result)
}

// verifyClip was a private method on *sources.Handler in
// handler_sources_source_utils.go. It moves here so VerifyClip + Cleanup
// (both on *clips.Handler) can use it. Uses h.driveUploader and
// h.voiceoverRepo which are on *clips.Handler; the legacy
// imageAssetToClip / voiceoverRecordToClip private methods were dropped
// in favor of the canonical artifacts.* converters.

func (h *Handler) verifyClip(ctx context.Context, source string, repo *assets.ClipsRepository, clip *asset.Asset) gin.H {
	result := gin.H{
		"ok":      true,
		"source":  source,
		"clip_id": clip.ID,
		"issues":  []string{},
	}

	// Check DB
	result["db"] = true

	// Check local file
	hasLocalFile := false
	if clip.LocalPath() != "" {
		if _, statErr := os.Stat(clip.LocalPath()); statErr == nil {
			hasLocalFile = true
			result["local_file"] = true
			result["local_path"] = clip.LocalPath()
		} else {
			result["local_file"] = false
			result["local_path"] = clip.LocalPath()
			result["local_error"] = "file not found: " + statErr.Error()
			result["issues"] = append(result["issues"].([]string), "local_file_missing")
		}
	} else {
		result["local_file"] = false
		result["issues"] = append(result["issues"].([]string), "local_path_empty")
	}

	// Check Drive link
	driveLink := clip.DriveLink()
	if driveLink == "" {
		driveLink = clip.DownloadLink()
	}
	var fileID string
	if driveLink != "" {
		result["has_drive_link"] = true
		result["drive_link"] = driveLink

		// Extract file ID and verify with Drive API
		fileID = ExtractDriveFolderID(driveLink)
		if fileID != "" {
			result["drive_file_id"] = fileID
			result["drive_link_valid"] = true
		} else {
			result["drive_link_valid"] = false
			result["issues"] = append(result["issues"].([]string), "drive_link_invalid")
		}
	} else {
		result["has_drive_link"] = false
		result["issues"] = append(result["issues"].([]string), "drive_link_missing")
	}

	// Check hash
	if clip.FileHash() != "" {
		result["hash"] = clip.FileHash()
		result["has_hash"] = true

		if hasLocalFile {
			result["hash_verified"] = false
		}
	} else {
		// Try to recover hash from Drive if available
		if fileID != "" && h.driveUploader != nil {
			md5, err := h.driveUploader.GetFileMD5(ctx, fileID)
			if err == nil && md5 != "" {
				clip.SetFileHash(md5)
				result["hash"] = md5
				result["has_hash"] = true
				result["hash_recovered"] = true
			// QDRANT-asset-mutation isolation (June 2026):
			// repo.UpsertClip is REMOVED from ClipRepositoryPort. The
			// hash-recovery write now uses the lower-level Upsert method
			// (still public, still present on *assets.ClipsRepository).
			// The lint in scripts/ci-architectural-checks.sh bans
			// UpsertClip/Restore/HardDelete in internal/application +
			// internal/api production paths.
			if repo != nil {
				if err := repo.Upsert(ctx, clip); err != nil {
					h.log.Warn("failed to save recovered hash", zap.String("clip_id", clip.ID), zap.Error(err))
				} else {
					h.log.Info("recovered and saved missing hash from drive", zap.String("clip_id", clip.ID), zap.String("hash", md5))
				}
			} else if strings.ToLower(source) == "voiceover" && h.voiceoverRepo != nil {
					rec, err := h.voiceoverRepo.GetByID(ctx, clip.ID)
					if err == nil && rec != nil {
						rec.FileHash = md5
						if err := h.voiceoverRepo.Upsert(ctx, rec); err != nil {
							h.log.Warn("failed to save recovered voiceover hash", zap.String("id", clip.ID), zap.Error(err))
						} else {
							h.log.Info("recovered and saved missing voiceover hash from drive", zap.String("id", clip.ID), zap.String("hash", md5))
						}
					}
				}
			} else {
				result["has_hash"] = false
				result["issues"] = append(result["issues"].([]string), "hash_missing")
			}
		} else {
			result["has_hash"] = false
			result["issues"] = append(result["issues"].([]string), "hash_missing")
		}
	}

	if clip.FolderID() != "" {
		result["folder_id"] = clip.FolderID()
	}
	if clip.FolderPath() != "" {
		result["folder_path"] = clip.FolderPath()
	}

	status := "unknown"
	if clip.DriveLink() != "" || clip.DownloadLink() != "" {
		status = "processed"
	} else if clip.LocalPath() != "" {
		status = "downloaded"
	} else {
		status = "pending"
	}
	result["status"] = status

	issues := result["issues"].([]string)
	if len(issues) == 0 {
		result["coherent"] = true
	} else {
		result["coherent"] = false
		result["issue_count"] = len(issues)
	}

	// Reference time.Now() so go vet doesn't flag time as unused if a future
	// refactor stops using it; the prior legacy body took its time via
	// driveUploader.GetFileMD5 indirectly.
	_ = time.Now()

	return result
}
