// Package clips — clip integrity sub-handler (Fase 2 split, June 2026).
//
// Extracted from ops.go: clip operations (VerifyClip, HandleFixHash, Cleanup, Reconcile).
// Depends on: ClipOpsService.
package assets

import (
	"errors"
	"net/http"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// VerifyClip POST + /api/clips/:source/clips/:id/verify — thin
// transport over ClipOpsService.Verify.
func (oh *OpsHandler) VerifyClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	if oh.clipOpsService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip ops service not wired")
		return
	}
	report := oh.clipOpsService.Verify(c.Request.Context(), source, clipID)
	apiutil.OK(c, buildVerifyResponse(report))
}

// HandleFixHash POST + /api/clips/:source/clips/:id/fix-hash —
// thin transport over ClipOpsService.FixHash.
func (oh *OpsHandler) HandleFixHash(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")
	if oh.clipOpsService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip ops service not wired")
		return
	}
	report, err := oh.clipOpsService.FixHash(c.Request.Context(), source, clipID)
	if err != nil {
		switch err {
		case appclips.ErrFixHashVoiceoverUnsupported:
			apiutil.BadRequest(c, err.Error())
		case appclips.ErrFixHashMissingDriveLink:
			apiutil.Error(c, http.StatusConflict, err.Error())
		case appclips.ErrFixHashDispatcherUnavailable:
			apiutil.Error(c, http.StatusServiceUnavailable, err.Error())
		default:
			apiutil.InternalError(c, err)
		}
		return
	}
	apiutil.OK(c, gin.H{"ok": true, "report": report})
}

// Cleanup POST + /api/clips/:source/cleanup — thin transport over
// ClipOpsService.Cleanup.
func (oh *OpsHandler) Cleanup(c *gin.Context) {
	source := c.Param("source")
	var req cleanupRequest
	if err := c.ShouldBindJSON(&req); err != nil && !isEmptyJSONErr(err) {
		apiutil.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	input := req.toCommand(source, c.Query("deep") == "true")

	if oh.clipOpsService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip ops service not wired")
		return
	}
	report, err := oh.clipOpsService.Cleanup(c.Request.Context(), input)
	if err != nil {
		mapClipOpsError(c, err)
		return
	}
	apiutil.OK(c, buildCleanupResponse(report))
}

// Reconcile POST + /api/clips/:source/reconcile — delegates to
// ClipOpsService.Reconcile.
func (oh *OpsHandler) Reconcile(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		FolderID string `json:"folder_id"`
		Fix      bool   `json:"fix"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}
	if oh.clipOpsService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip ops service not wired")
		return
	}
	oh.clipOpsService.Reconcile(c.Request.Context(), source, req.FolderID)
	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"message": "reconciliation started (catalogSync is configured on SourcesHandler, not clips.Handler)",
	})
}

// buildCleanupResponse converts a *CleanupReport into a gin.H response.
func buildCleanupResponse(report *appclips.CleanupReport) gin.H {
	if report == nil {
		return gin.H{"ok": false, "items": []gin.H{}}
	}
	items := make([]gin.H, 0, len(report.Items))
	for _, item := range report.Items {
		items = append(items, gin.H{
			"id":     item.ID,
			"name":   item.Name,
			"reason": item.Reason,
		})
	}
	return gin.H{
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
	}
}

// buildVerifyResponse converts a *VerifyReport into a gin.H response.
func buildVerifyResponse(report *appclips.VerifyReport) gin.H {
	if report == nil {
		return gin.H{"ok": false, "issues": []string{"nil_verify_report"}}
	}
	issues := report.Issues
	if issues == nil {
		issues = []string{}
	}
	return gin.H{
		"ok":               report.OK,
		"source":           report.Source,
		"clip_id":          report.ClipID,
		"issues":           issues,
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
		"hash_info": gin.H{
			"recoverable":    report.HashInfo.Recoverable,
			"candidate_hash": report.HashInfo.CandidateHash,
		},
		"folder_id":   report.FolderID,
		"folder_path": report.FolderPath,
		"status":      report.Status,
		"coherent":    report.Coherent,
		"issue_count": report.IssueCount,
	}
}

// mapClipOpsError translates a ClipOps domain error into HTTP response.
func mapClipOpsError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, appclips.ErrJobsUnavailable) {
		apiutil.Error(c, http.StatusServiceUnavailable, appclips.ErrJobsUnavailable.Error())
		return
	}
	if errors.Is(err, appclips.ErrInvalidSource) {
		apiutil.BadRequest(c, err.Error())
		return
	}
	apiutil.InternalError(c, err)
}
