// Package clips — Ops sub-handler (Step 5 Split 4, June 2026).
//
// OVERRIDE ADR 0009 (clips.Handler capability-split) — user override
// recorded in commit messages; this commit extracts the 20 ops routes
// (command + folder + tag + enrich/reindex + reconcile/cleanup/verify
// + trash/delete + bulk-tags) into a dedicated *OpsHandler receiver.
// OpsDeps carries only the 12 deps these methods consume (cluster ×
// deps matrix §4, June 2026):
//
//   - DeletionSvc     (TrashClip, DeleteClip → DeletionService.DeleteClip)
//   - ClipOpsService  (VerifyClip, HandleFixHash, Cleanup, Reconcile
//    → appclips.ClipOpsService.Verify/FixHash/Cleanup/Reconcile)
//   - SourceResolver  (ListFolders, FolderStatus, RegenerateManifest,
//     TrashFolder, DeleteFolder, GetFolderChildren, GetTree,
//     GetBreadcrumb, ReindexClip — repoForSource gate)
//   - AssetTreeSvc    (GetFolderChildren, GetTree, GetBreadcrumb — tree fan-out)
//   - FolderMemSvc    (RegenerateManifest — manifest regen heuristic)
//   - DriveUploader   (TrashFolder, DeleteFolder — Drive.TrashFolder / DeleteFolder)
//   - BulkTagsUC      (BulkAddTags, BulkRemoveTags — appclips.BulkTagsUseCase)
//   - ReprocessUC     (ReprocessClip — appclips.ReprocessUseCase.Execute)
//   - EnrichUC        (ReindexClip — nil-check fidelity guard; the actual
//     dispatch routes via JobsSvc.Enqueue per S1a June 2026)
//   - JobsSvc         (EnrichMedia, ReindexClip, BatchReindex — media.enrich)
//   - ClipIndexer     (ReindexClip, BatchReindex — IndexClip / BatchReindex / IsEnabled)
//   - Log             (all methods)
//
// Pattern B (per-cluster RegisterRoutes with idem fn as parameter):
// the orchestrator Handler.RegisterRoutes single-calls
// oh.RegisterRoutes(r, h.idemWriter()). Ops inherits the same
// idempotency middleware as ingest/search so write routes (15 of 20)
// are atomic per AGENTS.md Pattern 8. Read routes (5) install no idem.
package clips

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// OpsDeps is the constructor bag for OpsHandler. The 12 fields below
// are exactly the deps the 20 moved methods touch — no more, no less.
// Cluster ownership follows the matrix in the Step 5 discovery report
// (June 2026, §4 Ops cluster).
//
// EnrichUC pointer is shared with IngestHandler and the orchestrator
// mirror via enrichUCOrLocal — single source of construction. Ops
// uses the pointer only as a nil-check fidelity guard inside
// ReindexClip's "enrichment deps wired?" branch (the actual
// enrichment dispatch routes through JobsSvc.Enqueue per S1a).
type OpsDeps struct {
	DeletionSvc    *deletion.DeletionService
	ClipOpsService *appclips.ClipOpsService
	SourceResolver *artifacts.SourceResolver
	AssetTreeSvc   *assettree.Service
	FolderMemSvc   *foldermemory.Service
	DriveUploader  *drive.Uploader
	BulkTagsUC     *appclips.BulkTagsUseCase
	ReprocessUC    *appclips.ReprocessUseCase
	EnrichUC       *appclips.EnrichUseCase
	JobsSvc        jobservice.Service
	ClipIndexer    *clipindexer.Service
	Log            *zap.Logger
}

// OpsHandler owns the 20 ops routes. Receiver-on-pattern-B:
// constructed in NewHandler from an OpsDeps shape extracted from
// the orchestrator Deps.
type OpsHandler struct {
	deletionSvc    *deletion.DeletionService
	clipOpsService *appclips.ClipOpsService
	sourceResolver *artifacts.SourceResolver
	assetTreeSvc   *assettree.Service
	folderMemSvc   *foldermemory.Service
	driveUploader  *drive.Uploader
	bulkTagsUC     *appclips.BulkTagsUseCase
	reprocessUC    *appclips.ReprocessUseCase
	enrichUC       *appclips.EnrichUseCase
	jobsSvc        jobservice.Service
	clipIndexer    *clipindexer.Service
	log            *zap.Logger
}

// NewOpsHandler constructs an OpsHandler with the supplied OpsDeps.
// Nil fields are tolerated for test fixtures (each method does its
// own nil-check); production wiring supplies all 12 via the
// orchestrator Deps shape.
func NewOpsHandler(d OpsDeps) *OpsHandler {
	return &OpsHandler{
		deletionSvc:    d.DeletionSvc,
		clipOpsService: d.ClipOpsService,
		sourceResolver: d.SourceResolver,
		assetTreeSvc:   d.AssetTreeSvc,
		folderMemSvc:   d.FolderMemSvc,
		driveUploader:  d.DriveUploader,
		bulkTagsUC:     d.BulkTagsUC,
		reprocessUC:    d.ReprocessUC,
		enrichUC:       d.EnrichUC,
		jobsSvc:        d.JobsSvc,
		clipIndexer:    d.ClipIndexer,
		log:            d.Log,
	}
}

// repoForSource resolves a clip source to its canonical repository
// via the shared SourceResolver. Mirrors SearchHandler.repoForSource
// (Split 1) and IngestHandler.repoForSource (Split 2) — each cluster
// that needs source → repo mapping owns its own private helper.
func (oh *OpsHandler) repoForSource(source string) *assets.ClipsRepository {
	if oh.sourceResolver == nil {
		return nil
	}
	return oh.sourceResolver.ResolveRepo(source)
}

// RegisterRoutes installs the 20 Ops routes on the supplied gin
// router group. Read routes install no idem middleware; write routes
// install it before the handler per AGENTS.md Pattern 8.
//
// Route table:
//
//	GET  /:source/folders                          -> ListFolders          (read)
//	GET  /:source/folders/:id                      -> FolderStatus         (read)
//	GET  /:source/folders/:id/children             -> GetFolderChildren    (read)
//	GET  /:source/tree                             -> GetTree              (read)
//	GET  /:source/breadcrumb                       -> GetBreadcrumb        (read)
//	POST /:source/clips/:id/verify                 -> VerifyClip           (write+idem)
//	POST /:source/clips/:id/fix-hash               -> HandleFixHash        (write+idem)
//	POST /:source/clips/:id/trash                  -> TrashClip            (write+idem)
//	POST /:source/clips/:id/delete                 -> DeleteClip           (write+idem)
//	POST /:source/clips/:id/reprocess              -> ReprocessClip        (write+idem)
//	POST /:source/clips/:id/reindex                -> ReindexClip          (write+idem)
//	POST /:source/bulk/tags/add                    -> BulkAddTags          (write+idem)
//	POST /:source/bulk/tags/remove                 -> BulkRemoveTags       (write+idem)
//	POST /:source/reconcile                        -> Reconcile            (write+idem)
//	POST /:source/cleanup                          -> Cleanup              (write+idem)
//	POST /:source/folders/:id/manifest             -> RegenerateManifest   (write+idem)
//	POST /:source/folders/:id/trash                -> TrashFolder          (write+idem)
//	POST /:source/folders/:id/delete               -> DeleteFolder         (write+idem)
//	POST /enrich                                   -> EnrichMedia          (write+idem)
//	POST /enrich/batch                             -> BatchReindex         (write+idem)
func (oh *OpsHandler) RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc) {
	// Read-only routes (no idempotency)
	r.GET("/:source/folders", oh.ListFolders)
	r.GET("/:source/folders/:id", oh.FolderStatus)
	r.GET("/:source/folders/:id/children", oh.GetFolderChildren)
	r.GET("/:source/tree", oh.GetTree)
	r.GET("/:source/breadcrumb", oh.GetBreadcrumb)

	// Write routes (idempotency-protected per PR8, June 2026)
	r.POST("/:source/clips/:id/verify", idem, oh.VerifyClip)
	r.POST("/:source/clips/:id/fix-hash", idem, oh.HandleFixHash)
	r.POST("/:source/clips/:id/trash", idem, oh.TrashClip)
	r.POST("/:source/clips/:id/delete", idem, oh.DeleteClip)
	r.POST("/:source/clips/:id/reprocess", idem, oh.ReprocessClip)
	r.POST("/:source/clips/:id/reindex", idem, oh.ReindexClip)
	r.POST("/:source/bulk/tags/add", idem, oh.BulkAddTags)
	r.POST("/:source/bulk/tags/remove", idem, oh.BulkRemoveTags)
	r.POST("/:source/reconcile", idem, oh.Reconcile)
	r.POST("/:source/cleanup", idem, oh.Cleanup)
	r.POST("/:source/folders/:id/manifest", idem, oh.RegenerateManifest)
	r.POST("/:source/folders/:id/trash", idem, oh.TrashFolder)
	r.POST("/:source/folders/:id/delete", idem, oh.DeleteFolder)
	r.POST("/enrich", idem, oh.EnrichMedia)
	r.POST("/enrich/batch", idem, oh.BatchReindex)
}

// ──────────────────────────────────────────────────────────────────────
// MOVED FROM clip_ops.go (deleted in Split 4, June 2026)
// ──────────────────────────────────────────────────────────────────────

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

// Reconcile POST + /api/clips/:source/reconcile — placeholder for
// PR 4 (codex/clips-reconcile-real). The current implementation
// delegates to ClipOpsService.Reconcile which logs a typed entry,
// then returns stub-OK. PR 4 will replace this with a durable
// catalog.sync job enqueue + queue-absent 503 mapping.
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

// ──────────────────────────────────────────────────────────────────────
// MOVED FROM clip_ops_handlers.go (deleted in Split 4, June 2026)
// HandleBulkUploadYouTubeClipsJob stays in clip_ops_handlers.go for
// Split 5 (BulkUpload cluster).
// ──────────────────────────────────────────────────────────────────────

// HandleFixHash POST + /api/clips/:source/clips/:id/fix-hash —
// thin transport over ClipOpsService.FixHash. Maps the typed
// FixHash error sentinels to HTTP status codes (BadRequest 400 /
// Conflict 409 / ServiceUnavailable 503 / Internal 500).
//
// Status codes:
//
//	200 — fix-hash completed (see report).
//	400 — voiceover source unsupported.
//	409 — missing drive_link (cannot recover hash without it).
//	503 — clip ops FixHash dispatcher unavailable.
//	500 — any other FixHash failure.
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

// ──────────────────────────────────────────────────────────────────────
// MOVED FROM clip_delete.go (deleted in Split 4, June 2026)
// ──────────────────────────────────────────────────────────────────────

// deleteClip is the shared body for both Trash and Delete endpoints.
// hardDelete=false mirrors TrashClip; hardDelete=true mirrors DeleteClip.
func (oh *OpsHandler) deleteClip(c *gin.Context, hardDelete bool) {
	source := c.Param("source")
	clipID := c.Param("id")
	action := "trashed"
	if hardDelete {
		action = "deleted"
	}
	if err := oh.deletionSvc.DeleteClip(c.Request.Context(), source, clipID, hardDelete); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  action,
		"source":  source,
		"clip_id": clipID,
	})
}

// TrashClip moves a clip to Drive trash and removes SQLite record.
//   - POST /:source/clips/:id/trash
func (oh *OpsHandler) TrashClip(c *gin.Context) { oh.deleteClip(c, false) }

// DeleteClip permanently deletes a clip from Drive and SQLite.
//   - POST /:source/clips/:id/delete
func (oh *OpsHandler) DeleteClip(c *gin.Context) { oh.deleteClip(c, true) }

// ──────────────────────────────────────────────────────────────────────
// MOVED FROM folder.go (deleted in Split 4, June 2026)
// ──────────────────────────────────────────────────────────────────────

// ListFolders lists all folders for a source.
func (oh *OpsHandler) ListFolders(c *gin.Context) {
	source := c.Param("source")

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	folders, err := repo.ListFolders(c.Request.Context(), "")
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// Apply limit
	if limit > 0 && limit < len(folders) {
		folders = folders[:limit]
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"count":   len(folders),
		"folders": folders,
	})
}

// FolderStatus returns the status of a folder.
func (oh *OpsHandler) FolderStatus(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()

	// Get folder
	folder, err := repo.GetFolder(ctx, folderID)
	if err != nil {
		// Try by folder_id (Drive ID)
		folders, err2 := repo.ListFolders(ctx, "")
		if err2 != nil {
			apiutil.InternalError(c, err2)
			return
		}
		found := false
		for _, f := range folders {
			if f.FolderID == folderID {
				folder = f
				found = true
				break
			}
		}
		if !found {
			apiutil.NotFound(c, "folder not found")
			return
		}
	}

	// Get clips in folder
	clipList, _ := repo.ListByFolderID(ctx, folder.FolderID)
	if len(clipList) == 0 {
		clipList, _ = repo.ListByFolderPath(ctx, folder.FolderPath)
	}

	// Compute stats (inline — buildFolderStats lives as method body only)
	stats := asset.ClipFolderStats{}
	for _, clip := range clipList {
		stats.ClipCount++
		if clip.DriveLink() != "" || clip.DownloadLink() != "" {
			stats.ProcessedCount++
		}
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"folder":     folder,
		"stats":      stats,
		"clip_count": len(clipList),
	})
}

// RegenerateManifest regenerates manifest files for a folder.
func (oh *OpsHandler) RegenerateManifest(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	if oh.folderMemSvc == nil {
		apiutil.InternalError(c, nil)
		return
	}

	oh.log.Info("regenerating manifest for folder", zap.String("id", folderID))

	apiutil.OK(c, gin.H{
		"ok":     true,
		"source": source,
		"folder": folderID,
	})
}

// TrashFolder moves a folder to Drive trash.
func (oh *OpsHandler) TrashFolder(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	var driveFolderID string
	var dbFolderID string
	ctx := c.Request.Context()

	folder, err := repo.GetFolder(ctx, folderID)
	if err == nil && folder != nil {
		driveFolderID = folder.FolderID
		dbFolderID = folder.ID
		if folder.FolderPath != "" {
			if err := os.RemoveAll(folder.FolderPath); err != nil {
				oh.log.Error("failed to remove local folder path", zap.String("path", folder.FolderPath), zap.Error(err))
			}
		}
	} else {
		driveFolderID = folderID
		folders, err2 := repo.ListFolders(ctx, "")
		if err2 == nil {
			for _, f := range folders {
				if f.FolderID == folderID {
					dbFolderID = f.ID
					if f.FolderPath != "" {
						if err := os.RemoveAll(f.FolderPath); err != nil {
							oh.log.Error("failed to remove local folder path", zap.String("path", f.FolderPath), zap.Error(err))
						}
					}
					break
				}
			}
		}
	}

	if driveFolderID != "" {
		if oh.driveUploader == nil {
			apiutil.InternalError(c, fmt.Errorf("drive uploader not configured"))
			return
		}
		if err := oh.driveUploader.TrashFolder(ctx, driveFolderID); err != nil {
			oh.log.Error("failed to trash folder in Google Drive", zap.String("folder_id", driveFolderID), zap.Error(err))
			apiutil.InternalError(c, err)
			return
		}
	}

	if dbFolderID != "" {
		if err := repo.DeleteFolder(ctx, dbFolderID); err != nil {
			oh.log.Error("failed to delete folder from database", zap.String("id", dbFolderID), zap.Error(err))
		}
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"action": "trashed",
		"source": source,
		"folder": folderID,
	})
}

// DeleteFolder permanently deletes a folder.
func (oh *OpsHandler) DeleteFolder(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	var driveFolderID string
	var dbFolderID string
	ctx := c.Request.Context()

	folder, err := repo.GetFolder(ctx, folderID)
	if err == nil && folder != nil {
		driveFolderID = folder.FolderID
		dbFolderID = folder.ID
		if folder.FolderPath != "" {
			if err := os.RemoveAll(folder.FolderPath); err != nil {
				oh.log.Error("failed to remove local folder path", zap.String("path", folder.FolderPath), zap.Error(err))
			}
		}
	} else {
		driveFolderID = folderID
		folders, err2 := repo.ListFolders(ctx, "")
		if err2 == nil {
			for _, f := range folders {
				if f.FolderID == folderID {
					dbFolderID = f.ID
					if f.FolderPath != "" {
						if err := os.RemoveAll(f.FolderPath); err != nil {
							oh.log.Error("failed to remove local folder path", zap.String("path", f.FolderPath), zap.Error(err))
						}
					}
					break
				}
			}
		}
	}

	if driveFolderID != "" {
		if oh.driveUploader == nil {
			apiutil.InternalError(c, fmt.Errorf("drive uploader not configured"))
			return
		}
		if err := oh.driveUploader.DeleteFolder(ctx, driveFolderID); err != nil {
			oh.log.Error("failed to delete folder in Google Drive", zap.String("folder_id", driveFolderID), zap.Error(err))
			apiutil.InternalError(c, err)
			return
		}
	}

	if dbFolderID != "" {
		if err := repo.DeleteFolder(ctx, dbFolderID); err != nil {
			oh.log.Error("failed to delete folder from database", zap.String("id", dbFolderID), zap.Error(err))
		}
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"action": "deleted",
		"source": source,
		"folder": folderID,
	})
}

// ──────────────────────────────────────────────────────────────────────
// MOVED FROM folder_tree.go (deleted in Split 4, June 2026)
// ──────────────────────────────────────────────────────────────────────

// GetFolderChildren returns the children of a specific folder.
func (oh *OpsHandler) GetFolderChildren(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	if folderID == "root" {
		folderID = ""
	}

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	var children []*asset.AssetNode
	var err error

	if oh.assetTreeSvc != nil {
		treeNodes, treeErr := oh.assetTreeSvc.ListChildrenPaged(ctx, source, folderID, limit, offset)
		if treeErr == nil {
			for _, tn := range treeNodes {
				children = append(children, appclips.TreeNodeToAssetNode(tn))
			}
		} else {
			err = treeErr
		}
	} else {
		children = []*asset.AssetNode{}
		clipChildren, clipErr := repo.GetFolderChildren(ctx, folderID)
		if clipErr == nil {
			for _, clip := range clipChildren {
				children = append(children, appclips.TreeNodeToAssetNode(appclips.ClipToAssetNode(clip)))
			}
		} else {
			err = clipErr
		}
	}

	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":       true,
		"source":   source,
		"count":    len(children),
		"children": children,
	})
}

// GetTree returns the direct children of a given parent folder.
func (oh *OpsHandler) GetTree(c *gin.Context) {
	source := c.Param("source")
	parentID := c.Query("parent_id")

	if parentID == "root" {
		parentID = ""
	}

	if oh.assetTreeSvc == nil {
		apiutil.InternalError(c, nil)
		return
	}

	treeNodes, err := oh.assetTreeSvc.ListChildren(c.Request.Context(), source, parentID)
	if err != nil {
		oh.log.Error("failed to list children", zap.Error(err), zap.String("source", source), zap.String("parent_id", parentID))
		apiutil.InternalError(c, err)
		return
	}

	var children []*asset.AssetNode
	for _, tn := range treeNodes {
		children = append(children, appclips.TreeNodeToAssetNode(tn))
	}

	if len(children) == 0 {
		// Fallback to clips repository if asset tree is empty
		if repo := oh.repoForSource(source); repo != nil {
			clipChildren, clipErr := repo.GetFolderChildren(c.Request.Context(), parentID)
			if clipErr == nil {
				for _, clip := range clipChildren {
					children = append(children, appclips.TreeNodeToAssetNode(appclips.ClipToAssetNode(clip)))
				}
			}
		}
	}
	apiutil.OK(c, gin.H{
		"ok":       true,
		"source":   source,
		"children": children,
	})
}

// GetBreadcrumb returns the path from root down to the specified node ID.
func (oh *OpsHandler) GetBreadcrumb(c *gin.Context) {
	source := c.Param("source")
	id := c.Query("id")

	if id == "" {
		apiutil.BadRequest(c, "missing id parameter")
		return
	}

	if oh.assetTreeSvc == nil {
		apiutil.InternalError(c, nil)
		return
	}

	breadcrumb, err := oh.assetTreeSvc.GetBreadcrumb(c.Request.Context(), id)
	if err != nil {
		oh.log.Error("failed to get breadcrumb", zap.Error(err), zap.String("source", source), zap.String("id", id))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"breadcrumb": breadcrumb,
	})
}

// ──────────────────────────────────────────────────────────────────────
// MOVED FROM clip_action.go (ReprocessClip only — DownloadClip,
// ReuploadClip, FindDuplicates remain on *Handler until Split 3 =
// Action lands)
// ──────────────────────────────────────────────────────────────────────

// ReprocessClip reprocesses a clip (download/process/upload).
func (oh *OpsHandler) ReprocessClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	var req struct {
		Force       bool  `json:"force"`
		UploadDrive bool  `json:"upload_drive"`
		Normalize   *bool `json:"normalize"`
	}
	_ = c.ShouldBindJSON(&req)

	result, err := oh.reprocessUC.Execute(c.Request.Context(), appclips.ReprocessRequest{
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

// ──────────────────────────────────────────────────────────────────────
// MOVED FROM clip_enrich.go (EnrichMedia, ReindexClip, BatchReindex
// + the EnrichAndIndexClip helper — DownloadClip, ReuploadClip,
// FindDuplicates remain on *Handler until Split 3 = Action lands)
// ──────────────────────────────────────────────────────────────────────

// EnrichAndIndexClip runs the full enrichment pipeline in background with a 3-minute timeout:
//  1. LLM semantic tagger → search_text, tags, subjects
//  2. Clip indexer → embedding computation
//  3. Vector store (Qdrant) upsert
//
// Kept as a method on *OpsHandler to preserve the legacy public
// surface (callers in batch / external test code expect this method
// on the clips.Handler). The orchestrator *Handler.EnrichAndIndexClip
// thin-delegates here. If Ops is not wired, the orchestrator falls
// back to its own h.enrichUC mirror (which is the same instance
// across all sub-handlers by construction).
func (oh *OpsHandler) EnrichAndIndexClip(ctx context.Context, clip *asset.Asset, source string) {
	if oh.enrichUC == nil {
		return
	}
	oh.enrichUC.EnrichAndIndex(ctx, clip, source)
}

// EnrichMedia triggers semantic enrichment + embedding for any media asset.
// Works with ALL media types (image, clip, stock, artlist, youtube, audio, voiceover).
// If search_text is missing, calls the semantic tagger to generate it.
// Then generates embedding via the Python embedding server, persists to DB.
// Finally upserts to Qdrant vector store.
//
// Usage:
//
//	POST /api/artlist/enrich    — uses path param :source from route
//	POST /api/enrich            — uses source from JSON body
//
// S1a (June 2026): the previous implementation called
// `h.enrichUC.EnrichMedia(...)` which spawned
// `concurrent.SafeGo(...) + context.WithoutCancel(...)` goroutines
// inside the application tier — the
// "handler-tier goroutine simulating a background job"
// anti-pattern that AGENTS.md §7 + Pattern 8 explicitly forbid.
// Canonical path: enqueue a `media.enrich` job so the
// `MediaEnrichWorker` runs the work in the broker pool / remote
// worker (same code path as CreateClip / UploadVideoClip /
// ReindexClip). Handler returns the job_id so the operator can
// poll `GET /api/jobs/:id/full`.
func (oh *OpsHandler) EnrichMedia(c *gin.Context) {
	var req struct {
		AssetID      string `json:"asset_id"`
		Source       string `json:"source"`
		SkipEmbedGen bool   `json:"skip_embed_gen"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	// Fallback: use path param :source if body doesn't have source
	if req.Source == "" {
		req.Source = c.Param("source")
	}

	if req.AssetID == "" {
		apiutil.BadRequest(c, "asset_id is required")
		return
	}

	if oh.jobsSvc == nil {
		// S1a (June 2026): jobs service unavailable — the previous
		// path spawned a goroutine via concurrent.SafeGo inside
		// the application tier; we no longer do that. Truthful
		// refusal: ask the operator to wire jobsSvc and retry.
		apiutil.Error(c, http.StatusServiceUnavailable,
			"EnrichMedia requires the jobs service (S1a removed the in-process SafeGo fallback); wire jobsSvc to use /api/media/enrich")
		return
	}

	oh.log.Info("dispatching media.enrich via jobs system",
		zap.String("asset_id", req.AssetID),
		zap.String("source", req.Source),
		zap.Bool("skip_embed_gen", req.SkipEmbedGen),
	)

	job, err := oh.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type: jobservice.TypeMediaEnrich,
		Payload: map[string]any{
			"asset_id":       req.AssetID,
			"source":         req.Source,
			"skip_embed_gen": req.SkipEmbedGen,
		},
		ActiveKey: "enrich_clip_" + req.AssetID,
	})
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to enqueue media.enrich job: %w", err))
		return
	}
	apiutil.OK(c, gin.H{
		"ok":         true,
		"action":     "enqueued",
		"job_id":     job.ID,
		"status_url": "/api/jobs/" + job.ID + "/full",
		"asset_id":   req.AssetID,
		"source":     req.Source,
		"method":     "media.enrich_worker_via_jobs",
		"message":    "enrichment + indexing dispatched to jobs system (worker will run)",
	})
}

// ReindexClip triggers re-indexing of an existing clip (semantic enrichment + vector store).
// Useful after manually creating/updating a clip to make it searchable.
func (oh *OpsHandler) ReindexClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := oh.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()

	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Run semantic enrichment first if search_text is empty but we have a name.
	// S1a (June 2026): replaced `concurrent.SafeGo` + detached ctx (a
	// forbidden "HTTP simulating background job" anti-pattern per
	// AGENTS.md §7 + Pattern 8) with a canonical `media.enrich` job
	// enqueue. The worker runs in the local broker pool / remote
	// worker, owns its own ctx (3-min hard cap from the registry), and
	// emits pipeline_stage_started/_completed logs.
	//
	// ActiveKey is the canonical coalesce key: a single in-flight
	// `enrich_clip_<id>` job per clip regardless of which route
	// triggered it. Sourcing service uses the local EnqueueRequest and
	// therefore has NO activeKey, but the jobs broker's claim path
	// inspects (job_type, payload.asset_id) so identical work via
	// different entry-points still collapses naturally.
	//
	// OpsDeps.EnrichUC is passed purely for nil-check fidelity: the
	// actual enrichment dispatch routes through JobsSvc.Enqueue (see
	// S1a migration comment on EnrichMedia above).
	enrichNeeded := clip.SearchText == "" && clip.Name != "" && oh.enrichUC != nil
	if enrichNeeded {
		if oh.jobsSvc == nil {
			// Truthful refusal: we no longer run enrichment in the
			// request goroutine (the previous SafeGo path), so the only
			// honest answer is "service unavailable, the jobs system is
			// required". Test fixtures wire jobsSvc directly and skip
			// this branch.
			apiutil.Error(c, http.StatusServiceUnavailable,
				"reindex requires the jobs service (S1a removed the in-process SafeGo fallback); wire jobsSvc to use reindex")
			return
		}
		job, err := oh.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type: jobservice.TypeMediaEnrich,
			Payload: map[string]any{
				"asset_id": clipID,
				"source":   source,
			},
			ActiveKey: "enrich_clip_" + clipID,
		})
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to enqueue media.enrich job: %w", err))
			return
		}
		apiutil.OK(c, gin.H{
			"ok":         true,
			"action":     "enqueued",
			"job_id":     job.ID,
			"status_url": "/api/jobs/" + job.ID + "/full",
			"clip_id":    clipID,
			"method":     "async_enrich+index_via_jobs",
			"message":    "enrichment + indexing dispatched to jobs system (worker will run)",
		})
		return
	}

	if oh.clipIndexer != nil && oh.clipIndexer.IsEnabled() {
		// Full pipeline: indexer generates embedding + upserts to vector store
		if err := oh.clipIndexer.IndexClip(ctx, clipID); err != nil {
			apiutil.InternalError(c, fmt.Errorf("index failed: %w", err))
			return
		}
		apiutil.OK(c, gin.H{
			"ok":      true,
			"action":  "reindexed",
			"clip_id": clipID,
			"method":  "clip_indexer",
		})
		return
	}

	// direct-vector-store fallback removed — vector
	// capability deleted. The clip indexer is the canonical
	// semantic-search backend.

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "skipped",
		"clip_id": clipID,
		"reason":  "no indexer configured and no search_text available",
	})
}

// BatchReindex finds all assets missing embeddings and re-indexes them via the job system.
// Returns immediately with a job_id that can be polled via GET /api/jobs/:id/full.
//
// POST /api/media/enrich/batch
// Body: {"source": "artlist", "media_type": "video", "limit": 100}
func (oh *OpsHandler) BatchReindex(c *gin.Context) {
	var req struct {
		Source    string `json:"source"`
		MediaType string `json:"media_type"`
		Limit     int    `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if oh.clipIndexer == nil || !oh.clipIndexer.IsEnabled() {
		apiutil.InternalError(c, fmt.Errorf("clip indexer not available"))
		return
	}

	// Enqueue as a job so callers can poll progress via GET /api/jobs/:id/full
	if oh.jobsSvc != nil {
		job, err := oh.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type: "media.reindex",
			Payload: map[string]any{
				"source":     req.Source,
				"media_type": req.MediaType,
				"limit":      req.Limit,
			},
			ActiveKey: fmt.Sprintf("batch_reindex_%s_%s", req.Source, req.MediaType),
		})
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		apiutil.OK(c, gin.H{
			"ok":         true,
			"action":     "batch_reindex_enqueued",
			"job_id":     job.ID,
			"status_url": "/api/jobs/" + job.ID + "/full",
			"message":    "Batch reindex job enqueued",
		})
		return
	}

	// Fallback: fire-and-forget goroutine if jobs service not available
	ctx := c.Request.Context()
	result, err := oh.clipIndexer.BatchReindex(ctx, req.Source, req.MediaType, req.Limit)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "batch_reindex_started",
		"total":   result.Total,
		"message": fmt.Sprintf("%d assets queued for re-indexing (background)", result.Total),
	})
}

// ──────────────────────────────────────────────────────────────────────
// MOVED FROM clip_bulk.go (deleted in Split 4, June 2026)
// ──────────────────────────────────────────────────────────────────────

// BulkAddTags adds tags to multiple clips in one request.
func (oh *OpsHandler) BulkAddTags(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		IDs  []string `json:"ids"`
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	result, err := oh.bulkTagsUC.AddTags(c.Request.Context(), appclips.BulkTagsRequest{
		Source: source,
		IDs:    req.IDs,
		Tags:   req.Tags,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  result.Source,
		"count":   result.Count,
		"message": result.Message,
	})
}

// BulkRemoveTags removes tags from multiple clips.
func (oh *OpsHandler) BulkRemoveTags(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		IDs  []string `json:"ids"`
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	result, err := oh.bulkTagsUC.RemoveTags(c.Request.Context(), appclips.BulkTagsRequest{
		Source: source,
		IDs:    req.IDs,
		Tags:   req.Tags,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  result.Source,
		"count":   result.Count,
		"message": result.Message,
	})
}
