// Package voiceover — FASE 4 orphan-cleanup extraction
// (PR-VO-USECASE-PROCESS-DRY decomposition, per YouTube DoD wave
// process_segment.go split precedent).
//
// enqueueOrphanCleanup opens a SEPARATE tx and enqueues a
// voiceover.cleanup.requested outbox event for an orphaned Drive file
// (FASE 4, July 2026). Called ONLY from the Finalize-failure path
// when Stage 3 succeeded (DriveFileID is non-empty) and
// TxOutboxEnqueuer is wired.
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of the
// orphan-cleanup tx logic. The orchestrator delegates here.
package voiceover

import (
	"context"

	"go.uber.org/zap"
)

// enqueueOrphanCleanup opens a SEPARATE tx and enqueues a
// voiceover.cleanup.requested outbox event for an orphaned Drive file.
//
// The caller's Finalize tx was rolled back (defer in Execute), so
// this method opens a FRESH tx via VoiceoverRepository.BeginTx for
// the cleanup event. The cleanup tx is committed independently — a
// failure here does NOT change the original finalize_failed error
// (godlike/07 typed-error contract: the caller sees the Finalize
// error, not the cleanup error).
//
// Cleanup event fields:
//   - voiceoverID: cmd.ID (the voiceover row that was never inserted)
//   - oldDriveFileID: driveFileID (cleanup target: the orphaned Drive file)
//   - newDriveFileID: "" (no replacement was finalized)
//   - oldLocalPaths: [localPath, cleanedPath] (local temp files)
//
// godlike/07 NO-FAKE-AVAILABILITY: every failure in this method
// is logged at Warn level but does NOT propagate to the caller.
// The original Finalize error IS the canonical job outcome; the
// cleanup event is a best-effort recovery path. The background
// orphan sweeper (orphan_sweeper.go) is the safety net.
func (u *ProcessSegmentUseCase) enqueueOrphanCleanup(ctx context.Context, voiceoverID, driveFileID, localPath, cleanedPath string) {
	log := u.deps.Logger
	cleanupTx, txErr := u.deps.VoiceoverRepository.BeginTx(ctx)
	if txErr != nil {
		log.Warn("FASE 4 orphan-cleanup: BeginTx failed; orphan sweeper will recover",
			zap.String("voiceover_id", voiceoverID),
			zap.String("drive_file_id", driveFileID),
			zap.Error(txErr),
		)
		return
	}
	defer func() { _ = cleanupTx.Rollback() }()

	var oldLocalPaths []string
	if localPath != "" {
		oldLocalPaths = append(oldLocalPaths, localPath)
	}
	if cleanedPath != "" && cleanedPath != localPath {
		oldLocalPaths = append(oldLocalPaths, cleanedPath)
	}

	if err := u.deps.TxOutboxEnqueuer.EnqueueCleanupEvent(ctx, cleanupTx,
		voiceoverID,
		driveFileID,   // oldDriveFileID — cleanup target: the orphaned Drive file
		"",            // newDriveFileID — no replacement was finalized
		oldLocalPaths, // local temp files to remove
	); err != nil {
		log.Warn("FASE 4 orphan-cleanup: EnqueueCleanupEvent failed; orphan sweeper will recover",
			zap.String("voiceover_id", voiceoverID),
			zap.String("drive_file_id", driveFileID),
			zap.Error(err),
		)
		return
	}

	if err := cleanupTx.Commit(); err != nil {
		log.Warn("FASE 4 orphan-cleanup: cleanup tx commit failed; orphan sweeper will recover",
			zap.String("voiceover_id", voiceoverID),
			zap.String("drive_file_id", driveFileID),
			zap.Error(err),
		)
		return
	}

	log.Debug("FASE 4 orphan-cleanup: enqueued voiceover.cleanup.requested for orphaned Drive file",
		zap.String("voiceover_id", voiceoverID),
		zap.String("drive_file_id", driveFileID),
	)
}
