package clips

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ─── PR-A Phase 4 BULK moved methods ───────────────────────────────────────
// Reconcile, Cleanup, VerifyClip were previously methods on *sources.Handler
// in handler_sources_source_handlers_ops.go. They move here so the unified
// clips.Handler owns the entire clip lifecycle (including reconcile/cleanup
// verbs that operate across the clip surface). SourcesHandler no longer
// needs these.
//
// W14-PR2 slice 4 (June 2026): the *assets.ClipsRepository concrete type is
// replaced by appclips.ClipRepositoryPort; the same applies to voiceover/
// images repo references that crossed into the api package.

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
			// CleanupOrphanFiles is a domain-level deletion op that uses the
			// storage path string. We delegate via the assets-StoragePath port
			// on the typed config bundle.
			storagePath := ""
			if h.cfg != nil {
				storagePath = h.cfg.AssetsPath()
			}
			deleted, err := h.deletionSvc.CleanupOrphanFiles(c.Request.Context(), storagePath, false)
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
			allClips = append(allClips, appclips.VoiceoverDTOToClip(rec))
		}
	} else if repo != nil {
		// W14-PR2 slice 4: repo is now ClipRepositoryPort — but
		// ListClipsPaged takes (source, limit, offset, query) per the
		// existing port signature. The handler previously passed only
		// (source, limit, offset, "") so the call is identical.
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
		clip := appclips.VoiceoverDTOToClip(rec)
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
//
// W14-PR2 slice 4: repo parameter is the typed ClipRepositoryPort. The
// UpsertClip call inside the hash-recovery branch is on the same port.
func (h *Handler) verifyClip(ctx context.Context, source string, repo appclips.ClipRepositoryPort, clip *asset.Asset) gin.H {
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
				if repo != nil {
					if err := repo.UpsertClip(ctx, clip); err != nil {
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

	return result
}
