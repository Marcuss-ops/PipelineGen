package sources

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	urlutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
)

// QdrantHealth is a public liveness probe for the Qdrant vector store.
// Always returns 200 OK on invocation; the *body* reports whether
// Qdrant is actually healthy. The periodic health-monitor background
// loop (see startQdrantHealthMonitor) keeps the Prometheus gauge in
// sync, this endpoint is the human/curl-friendly face of the same.
func (h *Handler) QdrantHealth(c *gin.Context) {
	if h.vectorStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":      false,
			"healthy": false,
			"enabled": false,
			"error":   "vector store not configured",
		})
		return
	}
	if !h.vectorStore.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":      false,
			"healthy": false,
			"enabled": false,
			"error":   "vector store disabled",
		})
		return
	}
	probeCtx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	err := h.vectorStore.Health(probeCtx)
	resp := gin.H{
		"ok":      err == nil,
		"healthy": err == nil,
		"enabled": true,
	}
	status := http.StatusOK
	if err != nil {
		resp["error"] = err.Error()
		// 503 when Qdrant is unhealthy so naive HTTP monitors
		// (which only check status code) see red instead of green.
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, resp)
}

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

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	h.log.Info("Starting reconciliation", zap.String("source", source), zap.String("folder", req.FolderID))

	if h.catalogSync != nil {
		summary, err := h.catalogSync.SyncSource(c.Request.Context(), source)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		apiutil.OK(c, summary)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"message": "reconciliation started (no service)",
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

	repo := h.resolveRepo(source)
	sourceLower := strings.ToLower(source)
	if repo == nil && sourceLower != "images" && sourceLower != "voiceover" {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	var allClips []*assets.Asset
	sourceLower = strings.ToLower(source)

	if sourceLower == "images" && h.imagesRepo != nil {
		imgs, _ := h.imagesRepo.ListAll(ctx)
		for _, img := range imgs {
			allClips = append(allClips, imageAssetToClip(img))
		}
	} else if sourceLower == "voiceover" && h.voiceoverRepo != nil {
		recs, _ := h.voiceoverRepo.ListAll(ctx)
		for _, rec := range recs {
			allClips = append(allClips, voiceoverRecordToClip(rec))
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
		isOrphan := !verify["db"].(bool) || (!verify["local_file"].(bool) && !verify["has_drive_link"].(bool))

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
		clip := voiceoverRecordToClip(rec)
		result := h.verifyClip(c.Request.Context(), source, nil, clip)
		c.JSON(http.StatusOK, result)
		return
	}

	repo := h.resolveRepo(source)
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

// QdrantCleanup triggers an immediate Qdrant stale point cleanup.
// Scrolls all points, validates Drive links, and removes points whose
// Drive files have been trashed or deleted.
// Transient Drive API errors keep the point (no accidental deletion).
// POST /api/media/qdrant/cleanup
func (h *Handler) QdrantCleanup(c *gin.Context) {
	if h.vectorStore == nil || !h.vectorStore.Enabled() {
		apiutil.Error(c, http.StatusServiceUnavailable, "Qdrant vector store not configured or disabled")
		return
	}
	if h.driveUploader == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "Drive uploader not configured")
		return
	}

	ctx := c.Request.Context()
	validator := func(assetID, driveFileID, driveLink string) (bool, error) {
		// Prefer drive_file_id over URL-based link for validation
		fileID := driveFileID
		if fileID == "" {
			var err error
			fileID, err = urlutil.FileIDFromDriveLink(driveLink)
			if err != nil || fileID == "" {
				return true, fmt.Errorf("cannot extract file ID from link %q — keeping in Qdrant", driveLink)
			}
		}

		valid, checkErr := h.driveUploader.FileIsNotTrashed(ctx, fileID)
		if checkErr != nil {
			h.log.Warn("Drive API error during Qdrant cleanup, keeping point",
				zap.String("asset_id", assetID),
				zap.String("file_id", fileID),
				zap.Error(checkErr))
			return true, nil
		}

		return valid, nil
	}

	deleted, err := h.vectorStore.CleanupStalePoints(ctx, validator)
	if err != nil {
		h.log.Error("Qdrant cleanup failed", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("Qdrant cleanup failed: %w", err))
		return
	}

	h.log.Info("Qdrant cleanup completed (manual trigger)", zap.Int("deleted", deleted))
	apiutil.OK(c, gin.H{
		"ok":      true,
		"deleted": deleted,
		"message": fmt.Sprintf("Qdrant cleanup completed: %d stale points removed", deleted),
	})
}

// GetDiagnostics returns system health and version information.
func (h *Handler) GetDiagnostics(c *gin.Context) {
	results := gin.H{
		"ok": true,
		"services": gin.H{
			"artlist":   h.artlistSvc != nil,
			"youtube":   h.youtubeSvc != nil,
			"voiceover": h.voiceoverSvc != nil,
			"jobs":      h.jobsSvc != nil,
		},
		"environment": gin.H{
			"go_version": "1.25.9",
		},
	}

	// Add repository status
	repos := gin.H{}
	if h.artlistRepo != nil {
		repos["artlist"] = "connected"
	}
	if h.clipsRepo != nil {
		repos["clips"] = "connected"
	}
	if h.stockRepo != nil {
		repos["stock"] = "connected"
	}
	results["repositories"] = repos

	// Check Drive connectivity
	if h.driveUploader != nil {
		results["drive"] = gin.H{
			"status": "connected",
		}
	} else {
		results["drive"] = gin.H{
			"status": "disconnected",
		}
	}

	apiutil.OK(c, results)
}
