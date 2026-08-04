// Package clips — Ops sub-handler (Step 5 Split 2, June 2026, override ADR 0009).
//
// OVERRIDE ADR 0009 (clips.Handler capability-split) — user override
// recorded in commit body; this commit extracts the 14 Ops-cluster
// routes into a dedicated *OpsHandler receiver. OpsDeps carries ONLY
// the 7 deps these methods consume.
//
// Fase 2 (June 2026): handler methods split into 4 focused files:
//   - folder_query_handler.go    (ListFolders, FolderStatus, GetFolderChildren,
//     GetTree, GetBreadcrumb, repoForSource)
//   - folder_command_handler.go  (RegenerateManifest, TrashFolder, DeleteFolder)
//   - clip_integrity_handler.go  (VerifyClip, HandleFixHash, Cleanup, Reconcile,
//     buildCleanupResponse, buildVerifyResponse,
//     mapClipOpsError)
//   - clip_maintenance_handler.go (deleteClip, TrashClip, DeleteClip)
//
// This file retains: OpsDeps, OpsHandler, NewOpsHandler, cleanupRequest
// type, and isEmptyJSONErr utility.
//
// Route table (12 routes = 5 read + 5 write+idem + 2 DELETE+idem):
//
//	GET  /:source/folders                         -> ListFolders          (read)
//	GET  /:source/folders/:id                     -> FolderStatus         (read)
//	GET  /:source/folders/:id/children            -> GetFolderChildren    (read)
//	GET  /:source/tree                            -> GetTree              (read)
//	GET  /:source/breadcrumb                      -> GetBreadcrumb        (read)
//	POST /:source/clips/:id/verify                -> VerifyClip           (write+idem)
//	POST /:source/clips/:id/fix-hash              -> HandleFixHash        (write+idem)
//	DELETE /:source/clips/:id                     -> TrashClip            (delete+idem)
//	POST /:source/reconcile                       -> Reconcile            (write+idem)
//	POST /:source/cleanup                         -> Cleanup              (write+idem)
//	POST /:source/folders/:id/manifest            -> RegenerateManifest   (write+idem)
//	DELETE /:source/folders/:id                   -> TrashFolder          (delete+idem)
package clips

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"

	"go.uber.org/zap"
)

// OpsDeps is the constructor bag for OpsHandler. The 7 fields below
// are exactly the deps the 14 moved methods touch — no more, no less.
// Cluster ownership follows the matrix in the Step 5 discovery report
// (June 2026, §4 Ops cluster, Split 2).
type OpsDeps struct {
	ClipOpsService *appclips.ClipOpsService
	DeletionSvc    *deletion.DeletionService
	ClipsRepo      appclips.ClipRepositoryPort
	DriveAdmin     appclips.ClipDriveUploaderPort
	AssetTreeSvc   *assettree.Service
	Log            *zap.Logger
}

// OpsHandler owns the 14 Ops-cluster routes. Receiver-on-pattern-B:
// constructed in NewHandler from an OpsDeps shape extracted from
// the orchestrator Deps.
type OpsHandler struct {
	clipOpsService *appclips.ClipOpsService
	deletionSvc    *deletion.DeletionService
	clipsRepo      appclips.ClipRepositoryPort
	driveAdmin     appclips.ClipDriveUploaderPort
	assetTreeSvc   *assettree.Service
	log            *zap.Logger
}

// NewOpsHandler constructs an OpsHandler with the supplied OpsDeps.
func NewOpsHandler(d OpsDeps) *OpsHandler {
	return &OpsHandler{
		clipOpsService: d.ClipOpsService,
		deletionSvc:    d.DeletionSvc,
		clipsRepo:      d.ClipsRepo,
		driveAdmin:     d.DriveAdmin,
		assetTreeSvc:   d.AssetTreeSvc,
		log:            d.Log,
	}
}

// cleanupRequest is the typed request body for POST /api/clips/:source/cleanup.
type cleanupRequest struct {
	DryRun     bool `json:"dry_run"`
	CheckDrive bool `json:"check_drive"`
	Deep       bool `json:"deep"`
}

// toCommand translates the request body into the canonical application-side CleanupInput.
func (r cleanupRequest) toCommand(source string, deepFromQuery bool) appclips.CleanupInput {
	return appclips.CleanupInput{
		Source:     source,
		DryRun:     r.DryRun,
		CheckDrive: r.CheckDrive,
		Deep:       r.Deep || deepFromQuery,
	}
}

// isEmptyJSONErr returns true when ShouldBindJSON finds an empty request body.
func isEmptyJSONErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "EOF")
}
