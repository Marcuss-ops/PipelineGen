// Package clips — clip_ops.go: thin transport for the
// Reconcile / Cleanup / VerifyClip routes, owned by
// application/clips.ClipOpsService (Wave 14 PR2 cutover).
//
// All three handlers are pure delegate shells — no business
// logic lives in this file. Per PR 3 (June 2026 — codex/clips-ops-cutover):
//
//   - the request body is parsed into a typed request struct
//     (cleanupRequest) with a toCommand(source) method that
//     translates the JSON shape into the application-side input,
//   - application responses are projected into the legacy gin.H
//     JSON shape via buildCleanupResponse / buildVerifyResponse
//     helpers so the contract stays byte-compatible with the
//     pre-PR2 clients,
//   - error → HTTP mapping is centralized in mapClipOpsError.
//
// Reconcile keeps its current thin-delegate body; PR 4
// (codex/clips-reconcile-real) replaces the stub-by-log path
// with a durable catalog.sync job enqueue + 503 /
// RECONCILE_QUEUE_UNAVAILABLE mapping.
package clips

import (
	"errors"
	"net/http"
	"strings"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// ── Typed request shapes ─────────────────────────────────────────────────────

// cleanupRequest is the typed request body for POST
// /api/clips/:source/cleanup. Mirrors the JSON keys production
// callers send (dry_run, check_drive, deep). Empty bodies are
// accepted with zero-value defaults (preserved pre-PR2 lenient
// behaviour via the isEmptyJSONErr sentinel).
type cleanupRequest struct {
	DryRun     bool `json:"dry_run"`
	CheckDrive bool `json:"check_drive"`
	Deep       bool `json:"deep"`
}

// toCommand translates the request body into the canonical
// application-side CleanupInput. The "deep=true" query parameter
// (?deep=true) is OR'd with the body's Deep field so callers have
// both interfaces.
func (r cleanupRequest) toCommand(source string, deepFromQuery bool) appclips.CleanupInput {
	return appclips.CleanupInput{
		Source:     source,
		DryRun:     r.DryRun,
		CheckDrive: r.CheckDrive,
		Deep:       r.Deep || deepFromQuery,
	}
}

// ── HTTP handlers (thin transport) ──────────────────────────────────────────

// Cleanup POST + /api/clips/:source/cleanup — thin transport over
// ClipOpsService.Cleanup.
//
// Request body: {"dry_run": bool, "check_drive": bool, "deep": bool} +
// optional ?deep=true query param.
//
// Response shape (200): {"ok": true, "source": ..., "job_id": ...,
// "dry_run": ..., "check_drive": ..., "checked": ..., "deleted": ...,
// "summary": ..., "message": ..., "items": [...]}. Empty "items"
// slice for deep-batch enqueu (job_id polls the broker for results).
//
// Status codes:
//
//	200 — cleanup finished (sync or job enqueued).
//	400 — invalid JSON body OR invalid source value.
//	500 — service error.
//	503 — clip ops service not wired (composition bug).
func (h *Handler) Cleanup(c *gin.Context) {
	source := c.Param("source")
	var req cleanupRequest
	if err := c.ShouldBindJSON(&req); err != nil && !isEmptyJSONErr(err) {
		apiutil.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	input := req.toCommand(source, c.Query("deep") == "true")

	if h.clipOpsService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip ops service not wired")
		return
	}
	report, err := h.clipOpsService.Cleanup(c.Request.Context(), input)
	if err != nil {
		mapClipOpsError(c, err)
		return
	}
	apiutil.OK(c, buildCleanupResponse(report))
}

// VerifyClip POST + /api/clips/:source/clips/:id/verify — thin
// transport over ClipOpsService.Verify. The application's
// report.OK bool is independent of HTTP status — verify always
// returns 200; the caller inspects report fields (issues,
// coherent, has_drive_link) for the verdict.
//
// Status codes:
//
//	200 — verify ran (see report fields).
//	503 — clip ops service not wired.
func (h *Handler) VerifyClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	if h.clipOpsService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip ops service not wired")
		return
	}
	report := h.clipOpsService.Verify(c.Request.Context(), source, clipID)
	apiutil.OK(c, buildVerifyResponse(report))
}

// Reconcile POST + /api/clips/:source/reconcile — placeholder for
// PR 4 (codex/clips-reconcile-real). The current implementation
// delegates to ClipOpsService.Reconcile which logs a typed entry,
// then returns stub-OK. PR 4 will replace this with a durable
// catalog.sync job enqueue + queue-absent 503 mapping.
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

// ── Response helpers ────────────────────────────────────────────────────────

// buildCleanupResponse converts a *CleanupReport into a gin.H
// response. Field set matches the pre-PR2 keys verbatim (the
// historical client contract).
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

// buildVerifyResponse converts a *VerifyReport into a gin.H
// response. Field set matches the pre-PR2 keys verbatim.
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
		// S1d: project the typed HashInfo channel; canonical
		// SCOPE BOUNDARY rationale lives on the application-side
		// HashInfo godoc: see internal/application/clips/clip_ops.go::HashInfo.
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

// ── Error mapping ────────────────────────────────────────────────────────────

// mapClipOpsError translates a ClipOps domain error into the
// corresponding HTTP response. The "invalid source" sentinel is
// promoted to 400 Bad Request; everything else is 500. PR 4 will
// add the 503 + RECONCILE_QUEUE_UNAVAILABLE mapping when
// ClipOpsService.Reconcile returns ErrQueueUnavailable.
func mapClipOpsError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	// Wave 22 PR-5 polish: discriminate the typed
	// appclips.ErrJobsUnavailable sentinel into a 503 with the
	// body sourced from .Error() itself, so a future tweak of the
	// sentinel's text propagates here without duplication drift.
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

// isEmptyJSONErr returns true when ShouldBindJSON finds an empty
// request body (treated as zero-value defaults). EOF covers both
// "no body" and "all whitespace" cases.
func isEmptyJSONErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "EOF")
}
