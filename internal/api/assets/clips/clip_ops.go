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
// callers send (dry_run, check_local, check_drive, repair, delete,
// deep, batch_size). Empty bodies are accepted with zero-value
// defaults via the isEmptyJSONErr sentinel.
//
// PR 5 (June 2026 — codex/clips-cleanup-job): expanded to include
// check_local, repair, delete (the new spec dimensions) plus
// batch_size (configurable row-cap; cleaner defaults to 250).
type cleanupRequest struct {
	DryRun     bool `json:"dry_run"`
	CheckLocal bool `json:"check_local"`
	CheckDrive bool `json:"check_drive"`
	Repair     bool `json:"repair"`
	Delete     bool `json:"delete"`
	Deep       bool `json:"deep"`
	BatchSize  int  `json:"batch_size,omitempty"`
}

// toCommand translates the request body into the canonical
// application-side CleanupInput. The "deep=true" query parameter
// (?deep=true) is OR'd with the body's Deep field AND promotes
// Repair=true + Delete=true so legacy callers who set deep still
// trigger a meaningful cleanup pass.
func (r cleanupRequest) toCommand(source string, deepFromQuery bool) appclips.CleanupInput {
	deep := r.Deep || deepFromQuery
	return appclips.CleanupInput{
		Source:     source,
		DryRun:     r.DryRun,
		CheckLocal: r.CheckLocal || deep,
		CheckDrive: r.CheckDrive || deep,
		Repair:     r.Repair || deep,
		Delete:     r.Delete || deep,
		Deep:       deep,
		BatchSize:  r.BatchSize,
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
	started, err := h.clipOpsService.Cleanup(c.Request.Context(), input)
	if err != nil {
		mapClipOpsError(c, err)
		return
	}
	apiutil.OK(c, buildCleanupQueuedResponse(source, input, started))
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

// reconcileRequest is the typed request body for POST
// /api/clips/:source/reconcile. Mirrors the JSON keys production
// callers send (folder_id, fix, dry_run). Empty bodies are
// accepted with the URL-path source prepended via toCommand.
type reconcileRequest struct {
	FolderID string `json:"folder_id"`
	Fix      bool   `json:"fix"`
	DryRun   bool   `json:"dry_run"`
}

// toCommand translates the request body + URL-path source into the
// canonical ReconcileCommand.
func (r reconcileRequest) toCommand(source string) appclips.ReconcileCommand {
	return appclips.ReconcileCommand{
		Source:   source,
		FolderID: r.FolderID,
		Fix:      r.Fix,
		DryRun:   r.DryRun,
	}
}

// Reconcile POST + /api/clips/:source/reconcile — durable async
// reconcile. PR 4 (June 2026 — codex/clips-reconcile-real) replaces
// the stub-by-log body with a real catalog.sync job enqueue.
//
// Request body: {"folder_id": string, "fix": bool, "dry_run": bool}.
//
// Response shape (200): {"ok": true, "status": "queued",
// "job_id": "<job-id>", "status_url": "/api/jobs/<job-id>",
// "source": "<source>", "folder_id": "<folder>", "fix": bool,
// "dry_run": bool}. Caller polls status_url for results.
//
// Status codes:
//
//	200 — durable job enqueued.
//	400 — invalid JSON body OR invalid source.
//	500 — service error (enqueue failure).
//	503 — clip ops service not wired (composition bug).
//	503 with body.code == "RECONCILE_QUEUE_UNAVAILABLE" — broker
//	     unwired (JobsServicePort nil at composition time).
func (h *Handler) Reconcile(c *gin.Context) {
	source := c.Param("source")
	var req reconcileRequest
	if err := c.ShouldBindJSON(&req); err != nil && !isEmptyJSONErr(err) {
		apiutil.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	cmd := req.toCommand(source)

	if h.clipOpsService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "clip ops service not wired")
		return
	}
	started, err := h.clipOpsService.Reconcile(c.Request.Context(), cmd)
	if err != nil {
		mapClipOpsError(c, err)
		return
	}
	apiutil.OK(c, gin.H{
		"ok":         true,
		"status":     "queued",
		"job_id":     started.JobID,
		"status_url": "/api/jobs/" + started.JobID,
		"source":     cmd.Source,
		"folder_id":  cmd.FolderID,
		"fix":        cmd.Fix,
		"dry_run":    cmd.DryRun,
	})
}

// ── Response helpers ────────────────────────────────────────────────────────

// buildCleanupResponse converts the prev-PR5 *CleanupReport into a
// gin.H response. PR 5 retired the synchronous-return path; this
// helper is kept for any in-tree callers that still hold a *CleanupReport
// (e.g. queued-only deep paths that load the final report via the
// jobs Service). Field set matches the pre-PR2 keys verbatim (the
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

// buildCleanupQueuedResponse converts a *CleanupStarted into the
// PR 5 cleanup-shape queued response. Echoes the request flags
// back so callers can confirm the queued payload matches their
// intent. status_url is the literal "/api/jobs/<job-id>" path
// the application services stay URL-agnostic by handing the
// job_id up to the handler.
func buildCleanupQueuedResponse(source string, in appclips.CleanupInput, started *appclips.CleanupStarted) gin.H {
	if started == nil {
		return gin.H{"ok": false, "status": "error", "source": source, "message": "cleanup: nil started"}
	}
	return gin.H{
		"ok":          true,
		"status":      "queued",
		"job_id":      started.JobID,
		"status_url":  "/api/jobs/" + started.JobID,
		"active_key":  started.ActiveKey,
		"source":      source,
		"dry_run":     in.DryRun,
		"check_local": in.CheckLocal,
		"check_drive": in.CheckDrive,
		"repair":      in.Repair,
		"delete":      in.Delete,
		"batch_size":  started.BatchSize,
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
		// S1d-Wave 22 PR-5 polish amend: project the typed
		// HashInfo channel so callers see the canonical
		// recoverable-MD5 signal without scanning
		// heterogeneous issues. Two typed fields, no
		// `recovered` boolean (REMOVED per code-review Finding
		// 1; was a dead field with no producer-path). The
		// legacy `hash_recoverable` / `hash_recoverable_value`
		// flat fields above stay for JSON back-compat.
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
// corresponding HTTP response. Used by both Cleanup + Reconcile
// handlers. The typed-sentinel errors are preferred over
// string-matching (PR 4, June 2026).
func mapClipOpsError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, appclips.ErrQueueUnavailable) {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":      false,
			"code":    "RECONCILE_QUEUE_UNAVAILABLE",
			"message": err.Error(),
		})
		return
	}
	if errors.Is(err, appclips.ErrInvalidSource) {
		apiutil.BadRequest(c, "invalid source: "+err.Error())
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
