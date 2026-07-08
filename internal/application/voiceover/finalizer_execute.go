package voiceover

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Finalize runs the canonical 6-step atomic commit sequence inside the
// caller-owned transaction. The caller opens the tx, calls Finalize,
// then commits.
//
// Audit P0 #2 (July 2026): production-required deps (LifecycleService
// + Outbox) are fail-fast at Finalize() entry. The pre-P0 #2 surface
// degraded unwired Required steps to SkippedSteps — a silent-success
// pattern that hid the wiring fallback from operators. P0 #2 closes
// the gap by treating unwired-required-deps as a hard error, leaving
// only data-state guard-skips as recordable RequiredSteps entries.
func (f *voiceoverFinalizer) Finalize(ctx context.Context, tx *sql.Tx, cmd *FinalizeCommand) (*FinalizeResult, error) {
	if cmd == nil {
		return nil, fmt.Errorf("voiceoverFinalizer.Finalize: nil cmd")
	}
	if tx == nil {
		return nil, fmt.Errorf("voiceoverFinalizer.Finalize: nil tx (caller must open before calling Finalize)")
	}

	// ── Audit P0 #2 (July 2026): fail-fast wiring check ──
	// Production-required deps MUST be wired at composition time.
	// godlike/07 ZERO LEGACY + "no fake availability": we cannot lie
	// the operator by recording "executed" on a step that never ran
	// because its dep was nil. Such a state would corrupt the
	// downstream post-commit SQL verifier (audit-P0.5) which
	// observes empty media_assets rows and reports
	// StateCompletedUnverified — masking the wiring failure as a
	// verifier warn-level. Hard fail here, surface typed message,
	// let the caller (Service.finalizeStage) translate to
	// BatchItem.Status=StatusFailed.
	if f.deps.LifecycleService == nil {
		return nil, fmt.Errorf(errRequiredStepNotWired+
			" (LifecycleService / UpsertVoiceoverProjectionTx missing at composition)",
			requiredStepMediaAssetsProjection)
	}
	if f.deps.Outbox == nil {
		return nil, fmt.Errorf(errRequiredStepNotWired+
			" (Outbox / TxOutboxEnqueuer missing at composition; BOTH index + cleanup outbox steps fatal)",
			requiredStepIndexOutbox)
	}

	var optional []string
	var required []string

	// ── Step 0: FASE 3 Idempotency Gate (July 2026) ──
	// Runs BEFORE the dedupe gate (Step 1). When a prior attempt
	// of the same job+text+language triple already wrote a row with
	// this idempotency_key, the gate short-circuits the entire 6-step
	// sequence — the row IS the canonical outcome. The UNIQUE INDEX
	// idx_voiceovers_idempotency (migration 132) enforces ONE row per
	// non-empty key; the gate reads the matched row for identity.
	//
	// godlike/07 idempotency contract:
	//   - Empty IdempotencyKey → skip gate (backward-compat with
	//     pre-FASE-3 callers).
	//   - Non-empty IdempotencyKey + match found → Reused=true,
	//     Steps 1-6 skipped, caller's tx commits empty (safe).
	//   - Non-empty IdempotencyKey + no match → fall through to
	//     Step 1 (dedupe) + Steps 2-6 (first-time insert).
	//   - FindByIdempotencyKeyTx returns non-ErrNoRows error →
	//     fail-closed (idempotency-gate semantics must be validated
	//     BEFORE the dedupe gate runs).
	if cmd.IdempotencyKey != "" {
		matchedID, err := f.deps.VoiceoverRepo.FindByIdempotencyKeyTx(ctx, tx, cmd.IdempotencyKey)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("voiceoverFinalizer: idempotency lookup: %w", err)
		}
		if matchedID != "" {
			f.deps.Logger.Info("voiceoverFinalizer: FASE 3 idempotency reuse — matched prior row with same idempotency_key, skipping all 6 steps",
				zap.String("item_id", cmd.ID),
				zap.String("matched_id", matchedID),
				zap.String("idempotency_key", cmd.IdempotencyKey))
			return &FinalizeResult{
				ID:            matchedID,
				Reused:        true,
				OptionalSteps: []string{"idempotency: reuse existing row (FASE 3)"},
			}, nil
		}
	}

	// ── Step 1: Dedupe Gate (PR-VO-B3) ──
	// Optional — guard-skipped when DriveFileID is empty. Early-
	// return on dedupe-reuse hit (Reused=true) skips Steps 2-6.
	if cmd.DriveFileID != "" {
		// Audit P0 #3 (July 2026): a SQLite transient failure
		// during the dedupe lookup MUST propagate. A swallowed err
		// here would silently degrade to count==0 ⇒ DedupeContinue,
		// letting the finalizer proceed with DeleteByIDTx + InsertTx
		// + UpsertVoiceoverProjectionTx + EnqueueIndexEvent +
		// EnqueueCleanupEvent even though the dedupe gate's
		// semantics were never validated. The caller would then
		// observe a successful StatusCompleted against a partially-
		// verified guarantee. We fail-closed here.
		matchedID, count, err := f.deps.VoiceoverRepo.CountByDriveFileIDTx(ctx, tx, cmd.ID, cmd.DriveFileID)
		if err != nil {
			return nil, fmt.Errorf("voiceoverFinalizer: dedupe lookup: %w", err)
		}
		switch DecideDedupe(count) {
		case DedupeConflict:
			return nil, fmt.Errorf("voiceoverFinalizer: PR-VO-B3 ambiguous dedupe (count=%d, dedupe_id=%s) — refusing to insert duplicate row against established DriveFileID",
				count, matchedID)
		case DedupeReuse:
			f.deps.Logger.Info("voiceoverFinalizer: dedupe reuse — matched single prior row, skipping insert",
				zap.String("item_id", cmd.ID),
				zap.String("dedupe_id", matchedID))
			return &FinalizeResult{
				ID:            matchedID,
				Reused:        true,
				OptionalSteps: []string{"dedupe: reuse existing row"},
			}, nil
		case DedupeContinue:
			// Proceed with insert.
		}
	} else {
		optional = append(optional, "dedupe: empty DriveFileID")
	}

	// ── Step 2: Atomic Swap — DELETE old row ──
	// Mandatory; no execution-state variants.
	if err := f.deps.VoiceoverRepo.DeleteByIDTx(ctx, tx, cmd.ID); err != nil {
		return nil, fmt.Errorf("voiceoverFinalizer: DeleteByIDTx: %w", err)
	}

	// ── Step 3: INSERT new row ──
	// Mandatory; no execution-state variants.
	now := time.Now().UTC().Format(time.RFC3339)
	textPreview := cmd.Text
	if len(textPreview) > 100 {
		textPreview = textPreview[:100]
	}

	rec := &VoiceoverRecord{
		ID:             cmd.ID,
		RequestID:      cmd.RequestID,
		TextHash:       cmd.TextHash,
		TextPreview:    textPreview,
		Language:       string(cmd.Language),
		Voice:          cmd.Voice,
		Filename:       cmd.Filename,
		LocalPath:      cmd.LocalPath,
		CleanedPath:    cmd.CleanedPath,
		FolderID:       cmd.FolderID,
		FolderPath:     cmd.FolderPath,
		DriveFileID:    cmd.DriveFileID,
		DriveLink:      cmd.DriveLink,
		DownloadLink:   cmd.DownloadLink,
		FileHash:       cmd.FileHash,
		Status:         string(StatusGenerated),
		Strategy:       cmd.Strategy,
		Metadata:       string(cmd.MetaJSON),
		CreatedAt:      now,
		UpdatedAt:      now,
		IdempotencyKey: cmd.IdempotencyKey,
		JobID:          cmd.JobID,
	}
	if err := f.deps.VoiceoverRepo.InsertTx(ctx, tx, rec); err != nil {
		return nil, fmt.Errorf("voiceoverFinalizer: InsertTx: %w", err)
	}

	// ── Step 4: media_assets projection UPSERT (REQUIRED) ──
	// P0.4 Fase 3a (July 2026): this step was MISSING in the child
	// pipeline (Path A). The unified finalizer now writes the
	// media_assets row for BOTH paths. Audit P0 #2: LifecycleService
	// nil is a fatal wiring error (fail-fast above), so a non-nil
	// LifecycleService here is always present.
	if err := f.deps.LifecycleService.UpsertVoiceoverProjectionTx(ctx, tx, &VoiceoverProjectionInput{
		ID:           cmd.ID,
		Source:       "voiceover",
		Name:         textPreview,
		Filename:     cmd.Filename,
		FolderID:     cmd.FolderID,
		FolderPath:   cmd.FolderPath,
		MediaType:    "audio",
		LocalPath:    cmd.LocalPath,
		DriveFileID:  cmd.DriveFileID,
		DriveLink:    cmd.DriveLink,
		DownloadLink: cmd.DownloadLink,
		FileHash:     cmd.FileHash,
		Language:     cmd.Language,
		Status:       string(StatusGenerated),
		Metadata:     string(cmd.MetaJSON),
	}); err != nil {
		return nil, fmt.Errorf("voiceoverFinalizer: UpsertVoiceoverProjectionTx (media_assets): %w", err)
	}
	required = append(required, formatRequiredState(requiredStepMediaAssetsProjection, requiredStateExecuted))

	// ── Step 5: Outbox — asset.index.requested (REQUIRED) ──
	// Audit P0 #2: Outbox nil is fatal (fail-fast above). FileHash
	// empty is a data-state guard-skip with execution marker.
	if cmd.FileHash != "" {
		if err := f.deps.Outbox.EnqueueIndexEvent(ctx, tx, cmd.ID, cmd.FileHash); err != nil {
			return nil, fmt.Errorf("voiceoverFinalizer: EnqueueIndexEvent: %w", err)
		}
		required = append(required, formatRequiredState(requiredStepIndexOutbox, requiredStateExecuted))
	} else {
		required = append(required, formatRequiredState(requiredStepIndexOutbox, requiredStateGuarded, "empty FileHash"))
	}

	// ── Step 6: Outbox — voiceover.cleanup.requested (REQUIRED) ──
	// Extracted to finalizer_cleanup_outbox.go (PR-VO-FINALIZER-STEP6-EXTRACT,
	// P0 #3 in VO-DECOMPOSITION-2026-07-04, deadline 2026-08-01).
	// The P0.7 Step 10/12 atomic swap-and-cleanup triplo contract
	// (OldDriveFileID + OldLocalPath + OldCleanedPath) is preserved
	// VERBATIM inside the extracted function. The fail-fast Outbox
	// nil check stays at Finalize() entry above (godlike/07 ZERO
	// LEGACY: wiring is a precondition for BOTH Step 5 index outbox
	// AND Step 6 cleanup outbox).
	step6Marker, err := executeCleanupOutboxStep(ctx, tx, f.deps.Outbox, cmd)
	if err != nil {
		return nil, err
	}
	required = append(required, step6Marker)

	return &FinalizeResult{
		ID:            cmd.ID,
		Reused:        false,
		OptionalSteps: optional,
		RequiredSteps: required,
	}, nil
}

// formatRequiredState formats a RequiredSteps entry as
// "<step>: <state> (<reason>)" — state is one of `executed` or
// `guarded`, the optional reason is appended only on guarded entries
// to disambiguate which data-state guard fired. Pure string helper
// (no side effects) so it is trivially callable from inside Finalize
// without changing the deps or result mutation pattern.
func formatRequiredState(step, state string, reason ...string) string {
	if len(reason) == 0 {
		return step + ": " + state
	}
	return step + ": " + state + " (" + reason[0] + ")"
}
