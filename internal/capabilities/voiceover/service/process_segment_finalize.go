// Package voiceover — Stage 4 finalize extraction
// (BON-08: Execute decomposition).
//
// finalizeStage owns BeginTx → Finalize (dedupe gate + insert +
// projection + index/cleanup outbox) → Commit, including the orphan
// cleanup required when publish succeeded but persistence did not.
// buildFinalizeCommand and finalDurationSeconds are pure builders shared
// with the async publish path, so both writers persist the same shape.
package voiceover

import (
	"context"
	"fmt"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// finalDurationSeconds binds the persisted duration to the FINAL audio:
// the cleaned file after silence removal, otherwise the raw provider
// stream. Keeping this rule in one place stops the sync and async
// writers from drifting.
func finalDurationSeconds(ttsOut TTSOutput, post *AudioPostOutput) float64 {
	if post != nil && post.DurationUS > 0 {
		return float64(post.DurationUS) / 1e6
	}
	return ttsOut.Duration.Seconds()
}

// buildFinalizeCommand assembles the canonical Stage 4 DTO. The
// fingerprint and duration are resolved by the caller so this builder
// stays pure and table-testable.
func buildFinalizeCommand(
	cmd *ProcessSegmentCommand,
	out *VoiceoverItemResult,
	pub *publishStageResult,
	fingerprint string,
	durationSeconds float64,
) *FinalizeCommand {
	return &FinalizeCommand{
		Fingerprint:     fingerprint,
		IdempotencyKey:  pub.IdemKey,
		JobID:           cmd.JobID,
		ID:              cmd.ID,
		RequestID:       cmd.RequestID,
		TextHash:        string(cmd.TextHash),
		Text:            cmd.Text,
		Language:        cmd.Language,
		Voice:           out.Voice,
		Filename:        cmd.Filename,
		Strategy:        cmd.Strategy,
		MetaJSON:        pub.MetaJSON,
		LocalPath:       out.LocalPath,
		CleanedPath:     out.CleanedPath,
		ContentSHA256:   out.ContentSHA256,
		DurationSeconds: durationSeconds,
		FolderID:        cmd.Dest.FolderID,
		FolderPath:      cmd.Dest.FolderPath,
		DriveFileID:     out.DriveFileID,
		DriveLink:       out.DriveLink,
		DownloadLink:    out.DownloadLink,
		ShouldSwap:      cmd.ShouldSwap,
		OldDriveFileID:  cmd.OldDriveFileID,
		OldLocalPath:    cmd.OldLocalPath,
		OldCleanedPath:  cmd.OldCleanedPath,
	}
}

// cleanupOrphanedUpload enqueues the FASE 4 cleanup event when publish
// succeeded but the DB work did not, and the recovery port is wired.
func (u *ProcessSegmentUseCase) cleanupOrphanedUpload(ctx context.Context, cmd *ProcessSegmentCommand, out *VoiceoverItemResult) {
	if out == nil || out.DriveFileID == "" || u.deps.TxOutboxEnqueuer == nil {
		return
	}
	u.enqueueOrphanCleanup(ctx, cmd.ID, out.DriveFileID, out.LocalPath, out.CleanedPath)
}

// finalizeStage runs Stage 4 and marks the result envelope on failure.
// It returns the typed PipelineError unchanged; the caller owns metrics
// and terminal progress.
func (u *ProcessSegmentUseCase) finalizeStage(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	out *VoiceoverItemResult,
	pub *publishStageResult,
	ttsOut TTSOutput,
	post *AudioPostOutput,
	log *zap.Logger,
) (*FinalizeResult, error) {
	emitFinalize := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "finalize", string(cmd.Language))
	tx, err := u.deps.VoiceoverRepository.BeginTx(ctx)
	if err != nil {
		emitFinalize("failed")
		out.ErrorCode = string(FailureTxBegin)
		out.Error = fmt.Sprintf("%s: %v", FailureTxBegin, err)
		return nil, newPipelineErrorCode(StageTxBegin, true, FailureTxBegin, err)
	}
	defer func() { _ = tx.Rollback() }() // safe after Commit

	finalizeCmd := buildFinalizeCommand(
		cmd, out, pub,
		u.contentFingerprint(cmd.TextHash, cmd.Language, out.Voice, cmd.Dest.FolderID, cmd.Timing, cmd.RemoveSilence, voiceoverPostPolicy(cmd.TTSRate, cmd.SilencePolicy)),
		finalDurationSeconds(ttsOut, post),
	)

	var finalizeRes *FinalizeResult
	err = kernobs.MeasureStage(ctx, canonicalStageFinalize, func(stageCtx context.Context) error {
		var finalizeErr error
		finalizeRes, finalizeErr = u.deps.Finalizer.Finalize(stageCtx, tx, finalizeCmd)
		return finalizeErr
	})
	if err != nil {
		emitFinalize("failed")
		out.ErrorCode = string(FailureFinalize)
		out.Error = fmt.Sprintf("%s: %v", FailureFinalize, err)

		// Release the connection-pool lock before opening the cleanup tx.
		_ = tx.Rollback()
		u.cleanupOrphanedUpload(ctx, cmd, out)
		return nil, newPipelineErrorCode(StageTxCommit, true, FailureFinalize, err)
	}

	// If the dedupe gate matched an existing row, adopt its ID.
	if finalizeRes != nil && finalizeRes.Reused {
		out.ID = finalizeRes.ID
	}

	if err := tx.Commit(); err != nil {
		emitFinalize("failed")
		out.ErrorCode = string(FailureTxCommit)
		out.Error = fmt.Sprintf("%s: %v", FailureTxCommit, err)
		// The upload succeeded before the tx opened, so a failed commit
		// leaves a remote file with no row. Never leak it silently.
		u.cleanupOrphanedUpload(ctx, cmd, out)
		return nil, newPipelineErrorCode(StageTxCommit, true, FailureTxCommit, err)
	}
	emitFinalize("completed")
	return finalizeRes, nil
}
