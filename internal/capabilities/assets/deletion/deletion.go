// Package deletion — DeletionService + CompleteAsset (Blocco 3.1 commits 1/3 + 2/3 + 3/3, June-July 2026)
//
// History:
//
//   - Blocco 3.1 commit 1/3 (June 2026): state-machine foundation (the
//     canonical ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING →
//     INDEX_DELETE_PENDING → DELETED chain shape).
//   - Blocco 3.1 commit 2/3 (July 2026): INDEX_DELETED step added to
//     IndexDeleteHandler (SHA 2f043fff); pre-Drive-confirmation states
//     blocked by the Drive-block guard ("ancora vivo"); intermediate
//     INDEX_DELETED confirmation hop added between INDEX_DELETE_PENDING
//     and terminal DELETED.
//   - Blocco 3.1 commit 3/3 (July 2026) — THIS COMMIT: COMPLETED step
//     applied as the post-state-machine close-out. The COMPLETED step
//     runs AFTER the row has reached lifecycle_state=DELETED, performs
//     the final physical cleanup (hard-delete the media_assets row +
//     purge outbox_events + audit-log the COMPLETED marker), and is
//     idempotent on re-run.
//
// LONG-FILES-SPLIT-2026-07-06 Band A #5: the original 646-LOC
// deletion.go has been decomposed into 4 single-purpose files per
// AGENTS.md Pattern 5:
//
//	deletion.go        — thin service: ports, struct, constructor
//	delete_clip.go     — DeleteAsset, DeleteClip, DeleteByDriveFile,
//	                      FindClipByDriveFileID
//	complete_asset.go  — CompleteAsset, extractDriveFileID, isPastDriveDeleted,
//	                      driveLinkFileIDPattern
//	cleanup_orphan.go  — CleanupOrphanFiles
//
// godlike/06 SSOT (one canonical owner per fact): each file owns
// exactly one operation domain.
//
// godlike/07 minimum-blast-radius: pure code-motion, zero logic changes.
package assets

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
)

// DispatcherPort is the application-layer port for DeletionService's
// outbox emit (Pattern 0 — declared at the consumer side, satisfied
// by *outbox.Dispatcher in production. Structural satisfaction means
// callers don't need to import outbox — they pass the production
// concrete directly).
//
// Blocco 3.1 commit 4/3 (June 2026): the port is intentionally
// NARROW (single method). The wider outbox.Dispatcher surface is
// available elsewhere in the composition root — DeletionService
// itself only ever needs the drive-delete emission path. Consumers
// that need EnqueueIndexDelete / EnqueueAndRestore etc wire those
// directly via their own ports.
type DispatcherPort interface {
	EnqueueDriveDelete(ctx context.Context, assetID string, permanently bool) error
}

// DriveGoneChecker is the application-layer port for confirming that
// a Drive file is no longer present (Trashed or Permanently Deleted).
//
// Blocco 3.1 commit 3/3 (July 2026): wired optionally into CompleteAsset
// so the COMPLETED step can fail-closed on a stale-Drive-confirmation.
//
// Semantics:
//
//   - (isGone=true, err=nil)   → file is gone (Trashed or Deleted);
//     the COMPLETED step can proceed.
//   - (isGone=false, err=nil)  → file is STILL present in Drive;
//     CompleteAsset MUST NOT proceed
//     ("drive_file_alive_block" guard).
//   - (isGone=_, err=non-nil)  → Drive API check itself failed
//     (network / auth / 5xx); CompleteAsset
//     MUST propagate the error WITHOUT
//     side-effects (no SQLite delete, no
//     outbox purge, state machine unchanged).
//
// Production concrete: a thin adapter wrapping
// drive.FileLifecycle.FileIsNotTrashed (when wired). nil-port callers
// (pre-commit-4/3 production wiring forward-pointer, or callers that
// explicitly opt out of live Drive validation) trust the canonical
// lifecycle_state=DELETED stamp as the proof that DriveDeleteHandler
// already confirmed Drive removal.
type DriveGoneChecker interface {
	CheckDriveGone(ctx context.Context, fileID string) (isGone bool, err error)
}

// CompletionTxRunner is the application-layer port for the COMPLETED
// step's atomic post-state-machine cleanup. Pattern 0 narrow surface
// — application-layer consumers MUST NOT redeclare this locally.
//
// Blocco 3.1 commit 3/3 (July 2026): the production concrete wraps
// a single SQLite transaction that performs BOTH physical deletes:
//
//   - DELETE FROM media_assets WHERE id = ?           (hard delete the tombstone)
//   - DELETE FROM outbox_events WHERE aggregate_id = ? (purge the outbox state)
//
// Atomicity: both deletes run in the same BEGIN ... COMMIT tx so
// either BOTH land (success path) or NEITHER lands (rollback on error
// path). This is the load-bearing invariant for the COMPLETED step's
// "no partial side-effects on failure" contract — verified by the
// TestCompleteAsset_DriveCheckFailure_NoSQLiteDelete TDD test (the
// user-spec audit case).
//
// nil-port callers: CompleteAsset returns a typed wiring-error rather
// than silently no-op'ing the cleanup (godlike/05 fail-closed).
type CompletionTxRunner interface {
	RunCompletionTx(ctx context.Context, assetID string) error
}

// DeletionService handles deletion routing.
// Blocco 3.1 commit 4/3 (June 2026): the service no longer accepts
// synchronous Drive side-effects — every deletion route (Drive Trash
// or permanent Delete, Qdrant DeletePoints, SQLite SoftDelete, all
// lifecycle_state hops on the canonical state machine) is delegated
// to the outbox dispatcher. See the dispatcher's EnqueueDriveDelete
// docstring (internal/platform/sqlite/outbox/
// dispatcher_delete.go) for the full state-machine sequence.
//
// Blocco 3.1 commit 3/3 (July 2026): CompleteAsset was added as the
// post-state-machine close-out. The dispatch path (DeleteClip +
// async chain hops) and the close-out path (CompleteAsset) are
// invocationally separate; CompleteAsset is the canonical trailing
// call after the state machine reaches terminal DELETED.
type DeletionService struct {
	artlistRepo   *assets.ClipsRepository
	clipsRepo     *assets.ClipsRepository
	stockRepo     *assets.ClipsRepository
	voiceoverRepo *assets.VoiceoversRepository
	imagesRepo    *imagesrepo.ImagesRepository
	catalog       *artifacts.SourceCatalog
	// PR-WAVE-1-DRIVE-SSOT (July 2026): the legacy `*drive.Uploader`
	// field is RETIRED from the DeletionService struct entirely.
	// The field + ctor parameter were already unused by every
	// service method (DeleteClip, CompleteAsset, CleanupOrphanFiles
	// never read `s.driveUploader`); the back-compat retention was
	// the Blocco 3.1 commit 4/3 (June 2026) forward-pointer that this
	// commit resolves. The composition-root wire sites
	// (internal/app/wire_assets.go, internal/app/build_bundles_maint.go)
	// + the test fixtures (internal/application/assets/maintenance/
	// service_test.go, internal/application/assets/deletion/deletion_test.go)
	// have been updated to drop the corresponding ctor argument.
	assetTreeSvc  *assettree.Service
	assetIndexSvc *assetindex.Service
	dispatcher    DispatcherPort
	// driveGoneChecker + completionTxRunner are the Blocco 3.1
	// commit 3/3 close-out ports. driveGoneChecker is optional
	// (nil = trust lifecycle_state proof); completionTxRunner is
	// required for CompleteAsset to actually do cleanup (nil =
	// wiring-error fail-closed).
	driveGoneChecker DriveGoneChecker
	completionTx     CompletionTxRunner
	log              *zap.Logger
}

// DeletionServiceDeps bundles the dependencies for DeletionService so
// the constructor stays under the archcheck 8-parameter cap.
//
// PR-NEST-FLAT-DEPS-DELETION (July 2026): the previous flat shape had
// 10 mandatory fields, tripping the `max_struct_deps=8` archcheck
// gate. The struct now nests the 10 fields into 5 purpose-grouped
// sub-bundles (each ≤5 fields, all ≤8):
//
//   - Repos       (5): ArtlistRepo, ClipsRepo, StockRepo, VoiceoverRepo,
//     ImagesRepo — the 5 SQLite-backed repositories.
//   - Index       (2): AssetTreeSvc, AssetIndexSvc — the tree + index
//     service dependencies.
//   - Dispatcher (1): Dispatcher — the outbox dispatcher port.
//   - Finalize    (2): DriveGoneChecker, CompletionTxRunner — the
//     CompleteAsset-side ports (PR-WAVE-1-DRIVE-SSOT
//     July 2026 introduced both; nested here to keep
//     the same struct_shape-id).
//   - Log         (1): *zap.Logger.
//
// DeletionServiceDeps itself carries 5 sub-bundle fields → 5 fields,
// well below the 8-field cap. The nesting follows the canonical
// godlike/06 SSOT pattern established by PR-NEST-FLAT-DEPS-ARLIST
// (internal/capabilities/assets/providers/artlist/service.go:
// ServicePorts + ServiceDependencies{Infra, Ports, Domain, Repos,
// Finalizer}).
//
// godlike/06 SSOT: this is the SINGLE canonical Deps surface for
// DeletionService. New repository / service / port fields MUST land
// in one of the 5 sub-bundles (or extend the count by adding a new
// purpose-grouped sub-bundle) so DeletionServiceDeps stays ≤8 fields.
type DeletionServiceDeps struct {
	Repos      DeletionRepoDeps
	Catalog    *artifacts.SourceCatalog
	Index      DeletionIndexDeps
	Dispatcher DispatcherPort
	Finalize   DeletionFinalizeDeps
	Log        *zap.Logger
}

// DeletionRepoDeps groups the 5 SQLite-backed repositories the
// DeletionService indexes / mutates. Field count: 5 (≤8 cap).
type DeletionRepoDeps struct {
	ArtlistRepo   *assets.ClipsRepository
	ClipsRepo     *assets.ClipsRepository
	StockRepo     *assets.ClipsRepository
	VoiceoverRepo *assets.VoiceoversRepository
	ImagesRepo    *imagesrepo.ImagesRepository
}

// DeletionIndexDeps groups the live index / tree services the
// DeletionService reads. Field count: 2.
type DeletionIndexDeps struct {
	AssetTreeSvc  *assettree.Service
	AssetIndexSvc *assetindex.Service
}

// DeletionFinalizeDeps groups the CompleteAsset-side ports.
// Field count: 2.
type DeletionFinalizeDeps struct {
	// DriveGoneChecker is optional (nil = trust lifecycle_state proof).
	DriveGoneChecker DriveGoneChecker
	// CompletionTxRunner is required for CompleteAsset to actually do
	// cleanup (nil = wiring-error fail-closed).
	CompletionTxRunner CompletionTxRunner
}

// NewDeletionService creates a new deletion service.
//
// QDRANT-002 PR7: dispatcher is the canonical outbox.Dispatcher.
// Blocco 3.1 commit 4/3 (June 2026): dispatcher's type is the
// port interface DispatcherPort so test fixtures can substitute a
// recording mock without spinning up an in-memory SQLite + txmgr
// fixture. Production wiring passes *outbox.Dispatcher which
// satisfies the port structurally.
//
// Blocco 3.1 commit 3/3 (July 2026): DriveGoneChecker +
// CompletionTxRunner are optional from the caller's perspective
// (pass nil to defer wiring until commit 4/3 lands the production
// adapters). Existing callers that do NOT use CompleteAsset can
// pass nil for both without functional regression on DeleteClip paths.
// PR-WAVE-1-DRIVE-SSOT (July 2026): the `driveUploader *drive.Uploader`
// parameter is REMOVED from the canonical ctor signature. The removed
// parameter carried the legacy back-compat drive.Uploader reference that
// no service method consumed; all deletion routes flow through the
// canonical `dispatcher` (Outbox DispatcherPort) which owns the async
// Drive API surface (godlike/06 SSOT one-canonical-owner-per-fact,
// PR-DRIVE-CLEANUP).
func NewDeletionService(deps DeletionServiceDeps) *DeletionService {
	return &DeletionService{
		artlistRepo:      deps.Repos.ArtlistRepo,
		clipsRepo:        deps.Repos.ClipsRepo,
		stockRepo:        deps.Repos.StockRepo,
		voiceoverRepo:    deps.Repos.VoiceoverRepo,
		imagesRepo:       deps.Repos.ImagesRepo,
		catalog:          deps.Catalog,
		assetTreeSvc:     deps.Index.AssetTreeSvc,
		assetIndexSvc:    deps.Index.AssetIndexSvc,
		dispatcher:       deps.Dispatcher,
		driveGoneChecker: deps.Finalize.DriveGoneChecker,
		completionTx:     deps.Finalize.CompletionTxRunner,
		log:              deps.Log,
	}
}
