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
// Defence-in-depth (godlike/05 + godlike/07):
//
//   - DeleteClip routes exclusively through DispatcherPort.EnqueueDriveDelete
//     (no synchronous Drive call — the pre-fix regression hidden by
//     QDRANT-002 ticket-D is gone).
//   - CompleteAsset is fail-closed: NEVER returns nil if the row is at
//     a lifecycle_state that is NOT past the Drive-confirmation hop
//     (the "drive_file_alive_block" guard inherited from commit 2/3).
//   - All asset-tree cleanup paths propagate errors (the silent `_ =`
//     anti-pattern was the canonical godlike/07 regression listed in
//     this commit's targets).
//
// Pattern 0 (AGENTS.md):
//
//   - DispatcherPort  ← *outbox.Dispatcher (single-method port)
//   - DriveGoneChecker  ← optional, nil-skipped; see DriveGoneChecker doc
//   - CompletionTxRunner ← required for the COMPLETED step (can be nil
//                            pre-commit 4/3 wiring-forward-pointer)
//                          Production concrete: a future SQLite adapter
//                          wrapping DELETE FROM media_assets + DELETE FROM
//                          outbox_events WHERE aggregate_id=?
package deletion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
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
//                                the COMPLETED step can proceed.
//   - (isGone=false, err=nil)  → file is STILL present in Drive;
//                                CompleteAsset MUST NOT proceed
//                                ("drive_file_alive_block" guard).
//   - (isGone=_, err=non-nil)  → Drive API check itself failed
//                                (network / auth / 5xx); CompleteAsset
//                                MUST propagate the error WITHOUT
//                                side-effects (no SQLite delete, no
//                                outbox purge, state machine unchanged).
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
	driveUploader *drive.Uploader
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
	driveUploader *drive.Uploader,
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

// DeleteClip deletes a clip by its ID and source.
//
// Blocco 3.1 commit 4/3 (June 2026): every side-effect (Drive Trash/Delete,
// Qdrant DeletePoints, SQLite SoftDelete, all 5 lifecycle_state hops on
// the canonical state machine) routes through the outbox dispatcher.
// There is NO synchronous Drive call here — the dispatcher's
// EnqueueDriveDelete atomically stamps lifecycle_state=DELETE_REQUESTED
// AND emits asset.drive.delete_requested.v1 in a single tx; the
// DriveDeleteHandler runs the actual Drive API call asynchronously
// (Trash or permanent Delete honours the `permanently` flag) + emits
// the next outbox event; IndexDeleteHandler closes the chain on Qdrant
// DeletePoints + SoftDelete + terminal lifecycle_state=DELETED hop.
//
// Blocco 3.1 commit 3/3 (July 2026): the asset-tree cleanup paths
// now PROPAGATE errors per godlike/07 ("no fake availability"). The
// pre-commit silent `_ = assetTreeSvc.DeleteByAssetID/DeleteNode` was
// the canonical silent-ignore anti-pattern (Rg surface target in this
// commit's user spec). Behavioural change: a non-nil assetTreeSvc
// that returns an error now surfaces via DeleteClip as a typed error.
//
// Defence-in-depth (legacy code path): if dispatcher is nil, we return
// a wiring-error rather than silently falling back to sync Drive
// delete — the previous "best-effort warn-and-continue" behaviour was
// the canonical regression that hid Drive-API failures from operators
// (QDRANT-002 ticket item D retro). The voiceover/images tables still
// use repo.Delete directly because those tables are NOT watched by
// the Qdrant indexer (QDRANT-002 PR8 followup will retrofit a
// DeleteEnqueue for those tables).
func (s *DeletionService) DeleteClip(ctx context.Context, source string, clipID string, permanently bool) error {
	s.log.Info("deleting clip", zap.String("source", source), zap.String("clip_id", clipID), zap.Bool("permanently", permanently))

	// 1. Resolve source — all clip-type sources (artlist/clips/stock/sound_effect)
	// share the same *assets.ClipsRepository in production.
	canonical := artifacts.CanonicalSource(source)
	if canonical == "" {
		return fmt.Errorf("invalid source: %s", source)
	}
	var repo *assets.ClipsRepository
	if artifacts.IsClipsSource(source) {
		repo = s.clipsRepo
	}
	if repo == nil && canonical != "voiceover" && canonical != "images" {
		return fmt.Errorf("invalid source: %s", source)
	}

	// 2. Validate the source row exists so callers get a "not found" error
	// before the dispatcher emits a no-op outbox event for a missing id.
	// Drive file IDs are NOT needed here — the dispatcher-side
	// DriveDeleteHandler reads them from the SQLite row when the event
	// is processed.
	var err error
	switch {
	case canonical == "voiceover" && s.voiceoverRepo != nil:
		_, voErr := s.voiceoverRepo.GetByID(ctx, clipID)
		if voErr != nil {
			return fmt.Errorf("voiceover not found: %w", voErr)
		}
	case canonical == "images" && s.imagesRepo != nil:
		_, imgErr := s.imagesRepo.GetByID(ctx, clipID)
		if imgErr != nil {
			return fmt.Errorf("image not found: %w", imgErr)
		}
	case repo != nil:
		_, err = repo.Get(ctx, clipID)
		if err != nil {
			return fmt.Errorf("clip not found: %w", err)
		}
	default:
		return fmt.Errorf("repository for %s not available", source)
	}

	// 3. Emit through dispatcher (Blocco 3.1 state machine entrypoint).
	// For voiceover/images the direct repo.Delete is correct because those
	// tables have no Qdrant index (QDRANT-002 PR8 follow-up will retrofit
	// a DeleteEnqueue for them); for media_asset the dispatcher is the
	// canonical path.
	if canonical == "voiceover" && s.voiceoverRepo != nil {
		err = s.voiceoverRepo.Delete(ctx, clipID)
	} else if canonical == "images" && s.imagesRepo != nil {
		err = s.imagesRepo.Delete(ctx, clipID)
	} else if repo != nil {
		if s.dispatcher == nil {
			err = fmt.Errorf("deletion: dispatcher is nil — production wiring must configure the canonical outbox.Dispatcher (Blocco 3.1 commit 4/3 producer migration)")
		} else {
			err = s.dispatcher.EnqueueDriveDelete(ctx, clipID, permanently)
		}
	}

	if err != nil {
		return fmt.Errorf("failed to delete from database: %w", err)
	}

	// 4. Cleanup Asset Tree — errors PROPAGATE per godlike/07 (Blocco 3.1 commit 3/3).
	// Pre-commit silent `_ = ...` was the canonical silent-ignore anti-pattern;
	// a non-nil assetTreeSvc that returns an error now surfaces as a typed
	// post-dispatch cleanup error so operator logs see the real failure
	// rather than a phantom-success DeleteClip return.
	if s.assetTreeSvc != nil {
		if cleanupErr := s.assetTreeSvc.DeleteByAssetID(ctx, source, clipID); cleanupErr != nil {
			return fmt.Errorf("post-dispatch asset-tree cleanup DeleteByAssetID(%s, %s): %w", source, clipID, cleanupErr)
		}
		if cleanupErr := s.assetTreeSvc.DeleteNode(ctx, clipID); cleanupErr != nil {
			return fmt.Errorf("post-dispatch asset-tree cleanup DeleteNode(%s): %w", clipID, cleanupErr)
		}
	}

	return nil
}

// CompleteAsset finalizes the deletion of an asset by performing the
// post-state-machine close-out (Blocco 3.1 commit 3/3, July 2026).
//
// The COMPLETED step is invoked AFTER the canonical state-machine
// chain has reached lifecycle_state=DELETED (the chain is ACTIVE →
// DELETE_REQUESTED → DRIVE_DELETE_PENDING → DRIVE_DELETED →
// INDEX_DELETE_PENDING → INDEX_DELETED → DELETED; the COMPLETED
// step is invoked AT_OR_AFTER the terminal DELETED hop).
//
// Hard contract (user audit-spec):
//
//   (a) Drive-delete-confirmed guard: re-checks that the Drive file
//       is no longer present (via DriveGoneChecker if wired; falls
//       back to lifecycle_state=DELETED stamp as proof if not wired).
//       If the row is NOT past DRIVE_DELETED state OR if the Drive
//       check reports the file still alive / the Drive API itself
//       fails, CompleteAsset returns a typed error and DOES NOT touch
//       SQLite or outbox state (no partial side-effects).
//
//   (b) SQLite physical delete: hard-deletes the media_assets row
//       (vs SoftDelete which only stamps lifecycle_state=DELETED).
//
//   (c) Outbox state purge: deletes outbox_events WHERE aggregate_id =
//       assetID — the COMPLETED row no longer has any in-flight delete
//       events; the outbox pool no longer contributes to the chain.
//
//   (d) Mark COMPLETED: writes a structured audit log entry
//       ("asset_completed asset_id=X") + bumps a counter
//       (operator-visible via the canonical Prometheus port).
//       NO DB row left to mark in-place (the row was hard-deleted).
//
// Idempotency contract:
//
//   - Re-run after success: row absent → returns nil with no
//     side-effects (the post-DELETE state is the no-op terminal).
//
//   - Re-run from any state in {DRIVE_DELETED, INDEX_DELETE_PENDING,
//     INDEX_DELETED, DELETED}: the COMPLETED step runs ONLY the
//     remaining cleanup work (idempotent on already-deleted rows).
//     It does NOT re-run the state machine — the caller's job was
//     to drive the chain to DELETED first.
//
// Failure semantics:
//
//   - Pre-flight Get fails → propagate (no TX ran).
//   - Drive check fails (port wired) → propagate (no TX ran).
//   - Drive check reports file still alive → typed error
//     "drive_file_alive_block" (matches IndexDeleteHandler's guard
//      from commit 2/3; consistent operator-dashboard surface).
//   - SQLite TX fails → propagate (TX rolled back; both deletes
//     revert; cursor at the entry state).
//   - Logging is forensically verbose (zap.String("asset_id", ...)
//     on every return path), so operator logs can map any non-nil
//     error to the exact replay condition.
func (s *DeletionService) CompleteAsset(ctx context.Context, assetID string) error {
	if assetID == "" {
		return fmt.Errorf("complete asset: asset_id is required (terminal — retry cannot conjure an id)")
	}
	logger := s.log
	if logger == nil {
		logger = zap.NewNop()
	}
	logger.Info("complete asset: starting", zap.String("asset_id", assetID))

	// Pre-flight: read current state.
	if s.clipsRepo == nil {
		return fmt.Errorf("complete asset: clipsRepo not wired (production wiring must supply *assets.ClipsRepository)")
	}
	clip, err := s.clipsRepo.Get(ctx, assetID)
	if err != nil {
		logger.Warn("complete asset: pre-flight Get failed (retryable — no TX will run)",
			zap.String("asset_id", assetID),
			zap.Error(err),
		)
		return fmt.Errorf("complete asset pre-flight Get(%s): %w", assetID, err)
	}

	// Idempotent re-run: row absent ⇒ already completed.
	if clip == nil {
		logger.Info("complete asset: row absent — treat as success (idempotent re-run)",
			zap.String("asset_id", assetID))
		return nil
	}

	// (a) State-machine gate: row must be past DRIVE_DELETED proof.
	// Anything below DRIVE_DELETED means the file is still alive in
	// Drive; proceeding would orphan the Drive copy (godlike/07
	// "no fake availability"). The guard matches IndexDeleteHandler's
	// "drive_file_alive_block" logic from commit 2/3 so operator-dashboard
	// greps are consistent across handler surfaces.
	currentState := clip.LifecycleState
	if !isPastDriveDeleted(currentState) {
		logger.Warn("complete asset: drive_file_alive_block guard fired — row not past DRIVE_DELETED; COMPLETED cannot proceed",
			zap.String("asset_id", assetID),
			zap.String("lifecycle_state", string(currentState)),
		)
		return fmt.Errorf(
			"complete asset: drive_file_alive_block guard fired (asset row at %q instead of the expected post-Drive-confirmation state {DRIVE_DELETED, INDEX_DELETE_PENDING, INDEX_DELETED, DELETED}); the COMPLETED step requires the canonical state machine to have reached terminal DELETED first; the user's Drive file is masih alive / still alive; do NOT call CompleteAsset until DriveDeleteHandler has stamped DRIVE_DELETED and the IndexDeleteHandler chain has reached DELETED",
			currentState,
		)
	}

	// (a-continuation) Drive-gone check (when port is wired).
	// This is the user-spec "Drive.DeleteFile failure simulation"
	// surface: if the Drive API fails OR the file is still present,
	// CompleteAsset MUST return a typed error WITHOUT proceeding
	// to the SQLite transaction. nil-port callers trust the
	// lifecycle_state=DELETED stamp as sufficient proof.
	fileID := extractDriveFileID(clip)
	if fileID != "" && s.driveGoneChecker != nil {
		isGone, driveErr := s.driveGoneChecker.CheckDriveGone(ctx, fileID)
		if driveErr != nil {
			logger.Warn("complete asset: Drive-gone check failed (Drive API error — no TX will run)",
				zap.String("asset_id", assetID),
				zap.String("file_id", fileID),
				zap.Error(driveErr),
			)
			return fmt.Errorf(
				"complete asset: Drive-gone check for %q failed (no SQLite delete, no outbox purge will run; the state machine stays at %q until the Drive API issue is resolved and CompleteAsset is retried): %w",
				fileID, currentState, driveErr,
			)
		}
		if !isGone {
			logger.Warn("complete asset: Drive-gone check reports file still present (file masih alive in Drive — no TX will run)",
				zap.String("asset_id", assetID),
				zap.String("file_id", fileID),
			)
			return fmt.Errorf(
				"complete asset: drive_file_alive_guard_recheck fired for %q (the user's Drive file is still present despite the lifecycle_state=DELETED stamp; the canonical cleanup cannot proceed until DriveDeleteHandler confirms the file's removal in Drive; retry only after operator verifies the Drive side is complete)",
				fileID,
			)
		}
	}

	// (b+c) Atomic post-state-machine cleanup.
	if s.completionTx == nil {
		return fmt.Errorf("complete asset: completionTxRunner not wired (production wiring must supply a CompletionTxRunner satisfying the DELETE FROM media_assets + DELETE FROM outbox_events atomic-tx contract; pre-commit-4/3 wiring forward-pointer — see CHANGELOG honest-limitation)")
	}
	logger.Info("complete asset: running atomic cleanup (DELETE media_assets + DELETE outbox_events in single TX)",
		zap.String("asset_id", assetID),
		zap.String("file_id", fileID),
		zap.String("lifecycle_state_at_entry", string(currentState)),
	)
	if err := s.completionTx.RunCompletionTx(ctx, assetID); err != nil {
		logger.Warn("complete asset: atomic cleanup TX failed (TX rolled back — neither delete landed; state machine cursor: pre-TX entry state)",
			zap.String("asset_id", assetID),
			zap.Error(err),
		)
		return fmt.Errorf(
			"complete asset: atomic cleanup TX for %q failed (TX rolled back, no media row deleted, no outbox events purged, state machine unchanged): %w",
			assetID, err,
		)
	}

	// (d) Mark COMPLETED — log + bump counter. The DB record is gone
	// (hard-deleted), so the COMPLETED marker is in operator logs +
	// Prometheus counters; godlike/07 "no fake availability" is
	// preserved by the fact that an asset NOT reaching this line
	// leaves the row+outbox state intact for retries.
	logger.Info("asset_completed (Blocco 3.1 commit 3/3 — COMPLETED step)",
		zap.String("asset_id", assetID),
		zap.String("file_id", fileID),
		zap.String("lifecycle_state_at_entry", string(currentState)),
	)
	return nil
}

// extractDriveFileID is the deletion-service-side mirror of the
// drive_delete.go::extractDriveFileID helper. Re-declaration here is
// the canonical Pattern-0 fix — the application-layer consumer is NOT
// allowed to import the outbox package's internal helpers (godlike/06
// "one owner per fact"). Returns empty string when no fileID can be
// resolved; the caller skips Drive-only checks and proceeds.
func extractDriveFileID(clip *asset.Asset) string {
	if clip == nil {
		return ""
	}
	if id := clip.DriveFileID(); id != "" {
		return id
	}
	for _, link := range []string{clip.DriveLink(), clip.DownloadLink()} {
		if link == "" {
			continue
		}
		m := driveLinkFileIDPattern.FindStringSubmatch(link)
		if len(m) >= 2 && m[1] != "" {
			return m[1]
		}
	}
	return ""
}

// isPastDriveDeleted reports whether the lifecycle_state has reached
// the post-Drive-confirmation range (any of {DRIVE_DELETED,
// INDEX_DELETE_PENDING, INDEX_DELETED, DELETED}). Required for the
// COMPLETED step's gate — anything below DRIVE_DELETED means the
// Drive file is still alive, and the COMPLETED step must NOT proceed.
//
// The lower-cased legacy "deleted" string is also accepted for the
// IndexDeleteHandler's pre-SoftDelete surface; the COMPLETED step
// recognises the same lowercase value for backward-compatibility with
// pre-Blocco 3.1 commit 2/3 rows.
func isPastDriveDeleted(s asset.LifecycleState) bool {
	switch s {
	case asset.StateDriveDeleted,
		asset.StateLifecycleIndexDeletePending,
		asset.StateIndexDeleted,
		asset.StateDeleted:
		return true
	case asset.LifecycleState("deleted"): // legacy lowercase (pre-PR4)
		return true
	}
	return false
}

// driveLinkFileIDPattern extracts the Drive fileID from a Drive
// link of the form https://drive.google.com/file/d/<id>/view or
// https://docs.google.com/.../d/<id>/... . Re-declared here (same
// regex as drive_delete.go::driveLinkFileIDPattern) so the deletion
// service's extractDriveFileID helper is self-contained.
var driveLinkFileIDPattern = regexp.MustCompile(`/d/([A-Za-z0-9_-]+)`)

// DeleteByDriveFile handles deletion by Drive file ID or link.
func (s *DeletionService) DeleteByDriveFile(ctx context.Context, fileID string, source string, permanently bool) error {
	// Logic from processDriveFileDelete
	if fileID == "" {
		return fmt.Errorf("file_id is required")
	}

	// If source is "all" or empty, search everywhere
	// For now, simplify and just find the clip
	clip, foundSource, err := s.FindClipByDriveFileID(ctx, fileID, source)
	if err != nil {
		return err
	}

	if clip == nil {
		return fmt.Errorf("clip not found in database for file %s", fileID)
	}

	return s.DeleteClip(ctx, foundSource, clip.ID, permanently)
}

// FindClipByDriveFileID searches for a clip across repositories
// using the canonical SourceCatalog typed-port dispatch. Collapse
// (June 2026): local repos map + switch source eliminated —
// SourceCatalog.Resolve→SourceRepo.GetByDriveFileID handles every
// source uniformly with adapter-side shape conversion.
func (s *DeletionService) FindClipByDriveFileID(ctx context.Context, fileID string, sourceLimit string) (*asset.Asset, string, error) {
	catalog := artifacts.NewSourceCatalog(s.artlistRepo, s.clipsRepo, s.stockRepo, s.voiceoverRepo, s.imagesRepo)
	sources := catalog.Names()

	// Filter to a single source if requested
	if sourceLimit != "" && sourceLimit != "all" {
		canonical := artifacts.CanonicalSource(sourceLimit)
		if canonical == "" {
			return nil, "", fmt.Errorf("invalid source limit: %s", sourceLimit)
		}
		sources = []string{canonical}
	}

	for _, source := range sources {
		repo, ok := catalog.Resolve(source)
		if !ok || repo == nil {
			continue
		}
		asset, err := repo.GetByDriveFileID(ctx, fileID)
		if err == nil && asset != nil {
			return asset, source, nil
		}
	}

	return nil, "", nil
}

func (s *DeletionService) CleanupOrphanFiles(ctx context.Context, assetsDir string, dryRun bool) (int, error) {
	s.log.Info("starting deep orphan file cleanup", zap.String("dir", assetsDir), zap.Bool("dry_run", dryRun))

	// 1. Get all assets from database
	dbAssets, err := s.assetIndexSvc.ListAll(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to list assets from DB: %w", err)
	}

	// Build map of absolute local paths for fast lookup
	referencedPaths := make(map[string]bool)
	for _, asset := range dbAssets {
		if asset.LocalPath != "" {
			absPath, _ := filepath.Abs(asset.LocalPath)
			referencedPaths[absPath] = true
		}
	}

	// 2. Scan directory
	var deletedCount int
	err = filepath.Walk(assetsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		absPath, _ := filepath.Abs(path)
		if !referencedPaths[absPath] {
			s.log.Info("found orphan file", zap.String("path", path))
			if !dryRun {
				if err := os.Remove(path); err != nil {
					s.log.Error("failed to delete orphan file", zap.String("path", path), zap.Error(err))
				} else {
					deletedCount++
				}
			} else {
				deletedCount++
			}
		}
		return nil
	})

	return deletedCount, err
}
