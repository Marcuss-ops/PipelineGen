// Package voiceover — stage_finalize.go (PR-VO-STAGES-SPLIT, P0 #2 in
// VO-DECOMPOSITION-2026-07-04 wave, deadline 2026-08-01).
//
// Stage 5 of the 5-stage voiceover pipeline: commit +
// post-commit verification. Owns:
//  1. BeginTx (the caller-owned transaction).
//  2. finalizer.Finalize (the 6-step atomic commit sequence:
//     dedupe → delete → insert → media_assets projection →
//     index outbox → cleanup outbox, per P0.4 Fase 3a).
//  3. Commit.
//  4. Post-commit SQL verification (per P0.4 Fase 4a + Audit
//     P0.5, July 2026): confirms both the voiceovers row and
//     the media_assets projection are durably present.
//
// Mechanical extraction from the pre-split stages.go finalizeStage
// (which combined the in-tx work + commit + verify). No behavior
// change in EXPAND. The current implementation still calls
// finalizer.Finalize directly (not the forward-pointer
// persistStage in stage_persist.go) because the tx lifecycle
// (BeginTx → Finalize → Commit) is kept inside this method for
// minimal-blast-radius. A future BACKFILL wave will thread the
// tx through persistStage + finalizeStage.
//
// Compile-time lock: process_voiceover_item.go reads the same
// FinalizeCommand / FinalizeResult / VoiceoverFinalizer /
// VoiceoverPostCommitVerifier types via the finalizer package —
// preserved verbatim.
package voiceover

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// finalizeStage is Stage 3 (PR-VO-B3 dedupe gate + PR-VO-A2 atomic
// swap + PR-VO-A3 outbox + media_assets projection UPSERT +
// voiceover.cleanup.requested durable orphan cleanup). Wired
// between the stageLog("finalize") wrappers in process.go.
//
// RESTORED body (June 2026, Step 9/12 + Step 10/12):
//
//  1. Open sqlite tx (BeginTx with parent ctx so a request cancel
//     aborts the tx cleanly).
//  2. PR-VO-B3 dedupe gate runs INSIDE the tx so the count query is
//     consistent with the upcoming INSERT. Empty driveFileID
//     short-circuits to nil (no gate).
//  3. PR-VO-A2 atomic swap: DELETE existing same-id row, INSERT
//     new row in the same tx. The pre-read in processLanguage
//     already captured oldDriveFileID / oldLocalPath /
//     oldCleanedPath for the durable cleanup event.
//  4. P0.7 2-PHASE SPLIT (Step 9/12): UPSERT the media_assets
//     projection row INSIDE the same tx via
//     lifecycle.Service.UpsertVoiceoverProjectionTx.
//  5. PR-VO-A3 outbox enqueue inside the same tx.
//  6. P0.7 Step 10/12 (June 2026): durable orphan cleanup via the
//     voiceover.cleanup.requested outbox event INSIDE the same tx.
//  7. Commit.
//  8. P0.4 Fase 4a + Audit P0.5 (July 2026): post-commit SQL
//     verification.
//
// Why a single tx: PR-VO-A2 (Replace-Safe pipeline) requires that
// the OLD voiceover record is never removed UNTIL the NEW one is
// durably persisted.
//
// PR-VO-TYPED-PRIMITIVES (July 2026): textHash is raw string
// (the 64-char legacy full SHA-256 of req.Text); language is
// the typed BCP-47 envelope.
func (s *Service) finalizeStage(
	ctx context.Context,
	item BatchItem,
	requestID string,
	textHash string,
	language Language,
	req *BatchRequest,
	dest *ResolvedDestination,
	metaJSON []byte,
	shouldSwap bool,
	oldDriveFileID string,
	oldLocalPath string,
	oldCleanedPath string,
) BatchItem {
	// P0.4 Fase 3a (July 2026): delegate to the unified
	// VoiceoverFinalizer. The finalizer runs the canonical 6-step
	// atomic commit sequence inside a caller-owned tx. This
	// replaces the previous inline 170-line body with a single
	// Finalize call.
	//
	// Nil-safe: when the composition root didn't wire the finalizer
	// (legacy test paths), surface a typed failure at the per-
	// language boundary rather than mid-tx.
	if s.finalizer == nil {
		return item.fail(FailureDBUnavailable,
			fmt.Errorf("%s: finalizer not wired (composition root — P0.4 Fase 3a requires VoiceoverFinalizer)", restoreIdent))
	}

	if s.voiceoverRepo == nil {
		return item.fail(FailureDBUnavailable,
			fmt.Errorf("%s: voiceoverRepo not wired (composition root)", restoreIdent))
	}

	tx, err := s.voiceoverRepo.BeginTx(ctx)
	if err != nil {
		return item.fail(FailureTxBegin,
			fmt.Errorf("%s: BeginTx: %w", restoreIdent, err))
	}
	defer func() { _ = tx.Rollback() }() // safe after successful Commit

	folderID := ""
	folderPath := ""
	if dest != nil {
		folderID = dest.FolderID
		folderPath = dest.FolderPath
	}

	cmd := &FinalizeCommand{
		ID:        item.ID,
		RequestID: requestID,
		// PR-VO-TYPED-PRIMITIVES (July 2026): textHash is the raw
		// string (the legacy 64-char full SHA-256 of req.Text).
		// FinalizeCommand.TextHash is raw string for the same
		// reason — it carries both 64-char (legacy batch) and
		// 16-char (per-item fan-out) values depending on the
		// call path; the typed TextHash envelope is canonical
		// ONLY for the per-item 16-char value.
		TextHash:       textHash,
		Text:           req.Text,
		Language:       language,
		Voice:          item.Voice,
		Filename:       item.Filename,
		Strategy:       req.Strategy,
		MetaJSON:       metaJSON,
		LocalPath:      item.LocalPath,
		CleanedPath:    item.CleanedPath,
		FileHash:       item.FileHash,
		FolderID:       folderID,
		FolderPath:     folderPath,
		DriveFileID:    item.DriveFileID,
		DriveLink:      item.DriveLink,
		DownloadLink:   item.DownloadLink,
		ShouldSwap:     shouldSwap,
		OldDriveFileID: oldDriveFileID,
		OldLocalPath:   oldLocalPath,
		OldCleanedPath: oldCleanedPath,
	}

	finalizeRes, err := s.finalizer.Finalize(ctx, tx, cmd)
	if err != nil {
		return item.fail(FailureTxBegin,
			fmt.Errorf("%s: Finalize: %w", restoreIdent, err))
	}

	// Dedupe reuse: the finalizer matched an existing row. Adopt
	// the matched ID as the canonical identifier (mirrors the
	// pre-Fase-3a dedupe-reuse branch).
	if finalizeRes != nil && finalizeRes.Reused {
		item.ID = finalizeRes.ID
		item.Status = StatusCompleted
		return item
	}

	if err := tx.Commit(); err != nil {
		return item.fail(FailureTxCommit,
			fmt.Errorf("%s: Commit: %w", restoreIdent, err))
	}

	// P0.4 Fase 4a + Audit P0.5 (July 2026): post-commit SQL verification.
	// Extracted to verifyPostCommit for reduced cyclomatic complexity
	// (Azione #7, July 2026).
	item, shouldReturn := s.verifyPostCommit(ctx, item, finalizeRes, language, requestID)
	if shouldReturn {
		return item
	}

	item.Status = StatusCompleted
	return item
}

// verifyPostCommit runs the post-commit SQL verification (P0.4 Fase 4a +
// Audit P0.5, July 2026). The tx has committed — this is purely diagnostic.
// We confirm that both the voiceovers row and the media_assets projection
// are durably present. A missing row after a successful commit signals a
// silent schema/driver bug.
//
// Returns (item, true) when the caller should return immediately
// (reconciliation required — item.Status is already set to StatusFailed).
// Returns (item, false) otherwise (verifier passed, absent, or succeeded
// with warn-level divergence — caller should set StatusCompleted).
func (s *Service) verifyPostCommit(
	ctx context.Context,
	item BatchItem,
	finalizeRes *FinalizeResult,
	language Language,
	requestID string,
) (BatchItem, bool) {
	if s.postCommitVerifier == nil {
		return item, false
	}

	verifyErr := s.postCommitVerifier.Verify(ctx, item.ID)
	if verifyErr == nil {
		if finalizeRes != nil {
			finalizeRes.CompletionState = StateCompleted
		}
		return item, false
	}

	if errors.Is(verifyErr, ErrReconciliationRequired) {
		if finalizeRes != nil {
			finalizeRes.CompletionState = StateReconciliationRequired
		}
		if s.log != nil {
			s.log.Warn("finalizeStage: post-commit verification: canonical row missing — REQUIRES RECONCILIATION (will not report StatusCompleted)",
				zap.String("restored", restoreIdent),
				zap.String("voiceover_id", item.ID),
				zap.String("language", string(language)),
				zap.String("request_id", requestID),
				zap.String("completion_state", string(StateReconciliationRequired)),
				zap.Error(verifyErr))
		}
		item.Status = StatusFailed
		item.Error = "post_commit_reconciliation_required: " + verifyErr.Error()
		item.Errors = append(item.Errors, FailureReconciliationRequired)
		return item, true
	}

	// Non-severe divergence: secondary projection missing only.
	if finalizeRes != nil {
		finalizeRes.CompletionState = StateCompletedUnverified
	}
	if s.log != nil {
		s.log.Warn("finalizeStage: post-commit verification failed — row(s) missing after successful commit (warn-level — secondary projection missing only)",
			zap.String("restored", restoreIdent),
			zap.String("voiceover_id", item.ID),
			zap.String("language", string(language)),
			zap.String("request_id", requestID),
			zap.String("completion_state", string(StateCompletedUnverified)),
			zap.Error(verifyErr))
	}
	return item, false
}

// NOTE (P0.7 Step 10/12, June 2026): the pre-fix
// `func (s *Service) cleanupOrphanVoiceover(driveFileID, oldLocalPath, oldCleanedPath string)`
// method has been REMOVED. Orphan cleanup is now durable via the
// voiceover.cleanup.requested.v1 outbox event consumed by
// `internal/application/voiceover/outbox.VoiceoverCleanupHandler`.
// Failure modes are retryable via the outbox pool's exponential
// backoff; the `os.IsNotExist` short-circuit on local file remove
// is idempotent.
