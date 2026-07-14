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
//	delete_clip.go     — DeleteClip, DeleteByDriveFile, FindClipByDriveFileID
//	complete_asset.go  — CompleteAsset, extractDriveFileID, isPastDriveDeleted,
//	                      driveLinkFileIDPattern
//	cleanup_orphan.go  — CleanupOrphanFiles
//
// godlike/06 SSOT (one canonical owner per fact): each file owns
// exactly one operation domain.
//
// godlike/07 minimum-blast-radius: pure code-motion, zero logic changes.
package deletion

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
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
// docstring (internal/infrastructure/database/sqlite/outbox/
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
	imagesRepo    *assets.ImagesRepository
	// driveUploader is RETIRED at Blocco 3.1 commit 4/3 (June 2026).
	// The field + ctor parameter are retained for back-compat with
	// 3 production callers (internal/app/build_bundles_domain.go,
	// internal/app/module_media.go, internal/application/assets/
	// maintenance/service_test.go) but ignored by DeleteClip. Future
	// commit retires the field; tracked under the Blocco 3.1 commit
	// 4/3 forward-pointer in architecture/current.yaml.
	driveUploader any
	assetTreeSvc  *assettree.Service
	assetIndexSvc *assetindex.Service
	dispatcher    DispatcherPort
	// driveGoneChecker + completionTxRunner are the Blocco 3.1
	// commit 3/3 close-out ports. driveGoneChecker is optional
	// (nil = trust lifecycle_state proof); completionTxRunner is
	// required for CompleteAsset to actually do cleanup (nil =
	// wiring-error fail-closed).
	//
	// Production wiring (forward-pointer to commit 4/3): both
	// fields are nil until commit 4/3 lands the runtime adapters.
	// The library surface (CompleteAsset, the test pinning) is the
	// canonical contract that the production adapters will satisfy.
	driveGoneChecker DriveGoneChecker
	completionTx     CompletionTxRunner
	log              *zap.Logger
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
// Blocco 3.1 commit 3/3 (July 2026): driveGoneChecker +
// completionTxRunner are new trailing parameters; both optional
// from the caller's perspective (pass nil to defer wiring until
// commit 4/3 lands the production adapters). Existing callers that
// do NOT use CompleteAsset can pass nil for both without functional
// regression on DeleteClip paths.
func NewDeletionService(
	artlistRepo, clipsRepo, stockRepo *assets.ClipsRepository,
	voiceoverRepo *assets.VoiceoversRepository,
	imagesRepo *assets.ImagesRepository,
	driveUploader any,
	assetTreeSvc *assettree.Service,
	assetIndexSvc *assetindex.Service,
	dispatcher DispatcherPort,
	driveGoneChecker DriveGoneChecker,
	completionTxRunner CompletionTxRunner,
	log *zap.Logger,
) *DeletionService {
	return &DeletionService{
		artlistRepo:      artlistRepo,
		clipsRepo:        clipsRepo,
		stockRepo:        stockRepo,
		voiceoverRepo:    voiceoverRepo,
		imagesRepo:       imagesRepo,
		driveUploader:    driveUploader,
		assetTreeSvc:     assetTreeSvc,
		assetIndexSvc:    assetIndexSvc,
		dispatcher:       dispatcher,
		driveGoneChecker: driveGoneChecker,
		completionTx:     completionTxRunner,
		log:              log,
	}
}
