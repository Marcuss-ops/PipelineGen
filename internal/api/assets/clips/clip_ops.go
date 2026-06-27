package clips

import (
	"net/http"
	"strings"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// ─── PR 2 (June 2026) BULK: thin handler over ClipOpsService ───────────
//
// Reconcile / Cleanup / VerifyClip used to be methods on *sources.Handler
// in handler_sources_source_handlers_ops.go, then moved here preserving
// the original business logic. PR 2 collapses all three into thin
// transport shells that delegate to the canonical
// internal/application/clips.ClipOpsService:
//
//   - Reconcile  → ClipOpsService.Reconcile  (typed log entry, stub-OK
//     response preserved because CatalogSync lives on SourcesHandler).
//   - Cleanup    → ClipOpsService.Cleanup    (deep-mode → system.cleanup
//     job enqueue path; non-deep → per-source orphan pass).
//   - VerifyClip → ClipOpsService.VerifyClip (DB/local/Drive coherence
//     check, voiceover branch preserved, drive-MD5 hash recovery
//     preserved, repo writeback preserved).
//
// The previous logic lives only in the application service. This file
// is responsible for request binding, CleanupReport / VerifyReport
// marshalling into the legacy gin.H JSON shape, and Gin status codes.

// Reconcile reconciles database with Drive files. Pre-PR 2: the API
// handler emitted a stub-OK response because CatalogSync lived on
// SourcesHandler. PR 2 delegates to ClipOpsService.Reconcile which
// preserves the same stub semantics with a typed log entry.
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

	if h.clipOpsService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip ops service not wired")
		return
	}

	h.clipOpsService.Reconcile(c.Request.Context(), source, req.FolderID)

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"message": "reconciliation started (catalogSync is configured on SourcesHandler, not clips.Handler)",
	})
}

// Cleanup removes orphan database records. Pre-PR 2: complex local
// orchestration with a deep-mode branch that enqueued the
// "system.cleanup" job. PR 2 delegates to ClipOpsService.Cleanup
// which preserves the same shape and the same emission target.
func (h *Handler) Cleanup(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		DryRun     bool `json:"dry_run"`
		CheckDrive bool `json:"check_drive"`
		Deep       bool `json:"deep"`
	}
	_ = c.ShouldBindJSON(&req)

	if h.clipOpsService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip ops service not wired")
		return
	}

	report, err := h.clipOpsService.Cleanup(c.Request.Context(), appclips.CleanupInput{
		Source:     source,
		DryRun:     req.DryRun,
		CheckDrive: req.CheckDrive,
		Deep:       req.Deep || c.Query("deep") == "true",
	})
	if err != nil {
		if strings.Contains(err.Error(), "invalid source") {
			apiutil.BadRequest(c, err.Error())
			return
		}
		apiutil.InternalError(c, err)
		return
	}

	items := make([]gin.H, 0, len(report.Items))
	for _, item := range report.Items {
		items = append(items, gin.H{
			"id":     item.ID,
			"name":   item.Name,
			"reason": item.Reason,
		})
	}

	apiutil.OK(c, gin.H{
		"ok":          report.OK,
		"source":      report.Source,
		"job_id":      report.JobID,
		"dry_run":     report.DryRun,
		"check_drive": report.CheckDrive,
		"checked":     report.Checked,
		"deleted":     report.Deleted,
		"summary":     report.Summary,
		"message":     report.Message,
		"items":       items,
	})
}

// VerifyClip verifies DB, local file, and Drive coherence. Pre-PR 2:
// the local verifyClip helper did the work in-line and wrote back
// recovered hashes via raw repo.Upsert — the application-level
// service (ClipOpsService.Verify / verifyClip) implements the same
// shape using typed ports, so PR 2 is a pure delegate.
func (h *Handler) VerifyClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	if h.clipOpsService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip ops service not wired")
		return
	}

	report := h.clipOpsService.Verify(c.Request.Context(), source, clipID)

	c.JSON(http.StatusOK, gin.H{
		"ok":               report.OK,
		"source":           report.Source,
		"clip_id":          report.ClipID,
		"issues":           report.Issues,
		"db":               report.DB,
		"local_file":       report.LocalFile,
		"local_path":       report.LocalPath,
		"local_error":      report.LocalError,
		"has_drive_link":   report.HasDriveLink,
		"drive_link":       report.DriveLink,
		"drive_file_id":    report.DriveFileID,
		"drive_link_valid": report.DriveLinkValid,
		"hash":             report.Hash,
		"has_hash":         report.HasHash,
		"hash_verified":    report.HashVerified,
		"hash_recovered":   report.HashRecovered,
		"folder_id":        report.FolderID,
		"folder_path":      report.FolderPath,
		"status":           report.Status,
		"coherent":         report.Coherent,
		"issue_count":      report.IssueCount,
	})
}
