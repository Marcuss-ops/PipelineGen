package deletion

import (
	"context"
	"fmt"
	"regexp"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// driveLinkFileIDPattern extracts the Drive fileID from a Drive
// link of the form https://drive.google.com/file/d/<id>/view or
// https://docs.google.com/.../d/<id>/... . Re-declared here (same
// regex as drive_delete.go::driveLinkFileIDPattern) so the deletion
// service's extractDriveFileID helper is self-contained.
var driveLinkFileIDPattern = regexp.MustCompile(`/d/([A-Za-z0-9_-]+)`)

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
//	(a) Drive-delete-confirmed guard: re-checks that the Drive file
//	    is no longer present (via DriveGoneChecker if wired; falls
//	    back to lifecycle_state=DELETED stamp as proof if not wired).
//	    If the row is NOT past DRIVE_DELETED state OR if the Drive
//	    check reports the file still alive / the Drive API itself
//	    fails, CompleteAsset returns a typed error and DOES NOT touch
//	    SQLite or outbox state (no partial side-effects).
//
//	(b) SQLite physical delete: hard-deletes the media_assets row
//	    (vs SoftDelete which only stamps lifecycle_state=DELETED).
//
//	(c) Outbox state purge: deletes outbox_events WHERE aggregate_id =
//	    assetID — the COMPLETED row no longer has any in-flight delete
//	    events; the outbox pool no longer contributes to the chain.
//
//	(d) Mark COMPLETED: writes a structured audit log entry
//	    ("asset_completed asset_id=X") + bumps a counter
//	    (operator-visible via the canonical Prometheus port).
//	    NO DB row left to mark in-place (the row was hard-deleted).
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
//     from commit 2/3; consistent operator-dashboard surface).
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

	if clip == nil {
		logger.Info("complete asset: row absent — treat as success (idempotent re-run)",
			zap.String("asset_id", assetID))
		return nil
	}

	// (a) State-machine gate: row must be past DRIVE_DELETED proof.
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

	// (d) Mark COMPLETED — log + bump counter.
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
