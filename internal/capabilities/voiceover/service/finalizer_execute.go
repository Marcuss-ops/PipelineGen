package voiceover

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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
	// non-empty key; the coarser idx_voiceovers_job_language (migration
	// 133) enforces ONE row per (job_id, language) pair at the DB
	// structural level. The gate reads the matched row for identity.
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

	rec := &persistence.VoiceoverRecord{
		ID:              cmd.ID,
		RequestID:       cmd.RequestID,
		TextHash:        cmd.TextHash,
		TextPreview:     textPreview,
		Language:        string(cmd.Language),
		Voice:           cmd.Voice,
		Filename:        cmd.Filename,
		LocalPath:       cmd.LocalPath,
		CleanedPath:     cmd.CleanedPath,
		FolderID:        cmd.FolderID,
		FolderPath:      cmd.FolderPath,
		DriveFileID:     cmd.DriveFileID,
		DriveLink:       cmd.DriveLink,
		DownloadLink:    cmd.DownloadLink,
		LegacyFileMD5:   cmd.LegacyFileMD5,
		DurationSeconds: cmd.DurationSeconds,
		Status:          string(StatusGenerated),
		Strategy:        cmd.Strategy,
		Metadata:        string(cmd.MetaJSON),
		CreatedAt:       now,
		UpdatedAt:       now,
		IdempotencyKey:  cmd.IdempotencyKey,
		JobID:           cmd.JobID,
		Fingerprint:     cmd.Fingerprint,
	}
	if err := f.deps.VoiceoverRepo.InsertTx(ctx, tx, rec); err != nil {
		return nil, fmt.Errorf("voiceoverFinalizer: InsertTx: %w", err)
	}

	// ── Step 4 + Step 5: media_assets projection + asset.index.requested outbox ──
	// PR-ASSET-COMMITTER-COMMITASSET (July 2026): when Committer is
	// wired, BOTH writes (media_assets row + asset.index.requested
	// outbox event) are produced by a SINGLE Committer.CommitTx call
	// inside the caller's tx — atomic, single producer, no out-of-band
	// path. The legacy ports (LifecycleService, Outbox) remain as
	// pre-Cutover fallbacks for callers that have not yet wired the
	// committer.
	//
	// godlike/06 SSOT: when Committer is wired, it is the SOLE canonical
	// producer of both writes; the dispatcher is the SOLE canonical
	// consumer of the outbox event.
	//
	// The canonical AssetCommitter owns the media_assets write even when the
	// bytes are not materialized yet. Empty LegacyFileMD5 means REGISTERED-only:
	// CommitTx persists the asset and deliberately suppresses the index event.
	// The LifecycleService branch remains only as a migration compatibility
	// seam for old compositions where the committer is not wired.
	if f.deps.Committer != nil {
		if _, err := f.deps.Committer.CommitTx(ctx, tx, buildVoiceoverCommitRequest(cmd, textPreview)); err != nil {
			return nil, fmt.Errorf("voiceoverFinalizer: Committer.CommitTx (media_assets + outbox): %w", err)
		}
		required = append(required, formatRequiredState(requiredStepMediaAssetsProjection, requiredStateExecuted))
		// Empty LegacyFileMD5 is an explicit registered-only guard: no semantic
		// indexing event is emitted until content identity is known.
		if cmd.LegacyFileMD5 != "" {
			required = append(required, formatRequiredState(requiredStepIndexOutbox, requiredStateExecuted))
		} else {
			required = append(required, formatRequiredState(requiredStepIndexOutbox, requiredStateGuarded, "empty LegacyFileMD5"))
		}
	} else {
		// ── Legacy pre-Cutover path (Step 4 + Step 5 separately) ──
		// Audit P0 #2: LifecycleService nil is a fatal wiring error
		// (fail-fast at Finalize() entry), so a non-nil
		// LifecycleService here is always present.
		if err := f.deps.LifecycleService.UpsertVoiceoverProjectionTx(ctx, tx, &VoiceoverProjectionInput{
			ID:            cmd.ID,
			Source:        "voiceover",
			Name:          textPreview,
			Filename:      cmd.Filename,
			FolderID:      cmd.FolderID,
			FolderPath:    cmd.FolderPath,
			MediaType:     "audio",
			LocalPath:     cmd.LocalPath,
			DriveFileID:   cmd.DriveFileID,
			DriveLink:     cmd.DriveLink,
			DownloadLink:  cmd.DownloadLink,
			LegacyFileMD5: cmd.LegacyFileMD5,
			Language:      cmd.Language,
			Status:        string(StatusGenerated),
			Metadata:      string(cmd.MetaJSON),
		}); err != nil {
			return nil, fmt.Errorf("voiceoverFinalizer: UpsertVoiceoverProjectionTx (media_assets): %w", err)
		}
		required = append(required, formatRequiredState(requiredStepMediaAssetsProjection, requiredStateExecuted))

		// Audit P0 #2: Outbox nil is fatal (fail-fast above). LegacyFileMD5
		// empty is a data-state guard-skip with execution marker.
		if cmd.LegacyFileMD5 != "" {
			if err := f.deps.Outbox.EnqueueIndexEvent(ctx, tx, cmd.ID, "voiceover", cmd.LegacyFileMD5); err != nil {
				return nil, fmt.Errorf("voiceoverFinalizer: EnqueueIndexEvent: %w", err)
			}
			required = append(required, formatRequiredState(requiredStepIndexOutbox, requiredStateExecuted))
		} else {
			required = append(required, formatRequiredState(requiredStepIndexOutbox, requiredStateGuarded, "empty LegacyFileMD5"))
		}
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

func buildVoiceoverCommitRequest(cmd *FinalizeCommand, textPreview string) assetspersistence.CommitRequest {
	language := ""
	if cmd.Language != "" {
		language = string(cmd.Language)
	}

	// Preserve semantic enrichment in the canonical asset projection as
	// well as in the voiceovers row. The legacy finalizer already stores
	// MetaJSON verbatim; the AssetCommitter path must not silently replace
	// semantic search text/tags with the plain text preview.
	var semanticMeta struct {
		SearchText string   `json:"search_text"`
		Tags       []string `json:"semantic_tags"`
		Subjects   []string `json:"semantic_subjects"`
		Mood       []string `json:"semantic_mood"`
	}
	_ = json.Unmarshal(cmd.MetaJSON, &semanticMeta)
	searchText := textPreview
	if semanticMeta.SearchText != "" {
		searchText = semanticMeta.SearchText
	}
	extra := map[string]any{"language": language, "request_id": cmd.RequestID}
	if len(semanticMeta.Subjects) > 0 {
		extra["semantic_subjects"] = semanticMeta.Subjects
	}
	if len(semanticMeta.Mood) > 0 {
		extra["semantic_mood"] = semanticMeta.Mood
	}

	locations := []assetspersistence.LocationCommit{}
	if cmd.DriveFileID != "" {
		locations = append(locations, assetspersistence.LocationCommit{
			Kind:          "drive",
			Provider:      "drive",
			ExternalID:    cmd.DriveFileID,
			URI:           cmd.DriveLink,
			WebViewLink:   cmd.DriveLink,
			DownloadURL:   cmd.DownloadLink,
			LegacyFileMD5: cmd.LegacyFileMD5,
			IsPrimary:     true,
		})
	}
	_, initIndex := asset.NewIndexableAssetState()
	return assetspersistence.CommitRequest{
		AssetID:        cmd.ID,
		Source:         "voiceover",
		Name:           textPreview,
		Filename:       cmd.Filename,
		MediaType:      "audio",
		ContentHash:    cmd.LegacyFileMD5,
		Description:    textPreview,
		SearchText:     searchText,
		LifecycleState: string(asset.StatePublished),
		IndexState:     string(initIndex),
		LocalPath:      cmd.LocalPath,
		FolderID:       cmd.FolderID,
		FolderPath:     cmd.FolderPath,
		Title:          textPreview,
		Metadata: assetspersistence.TypedMetadata{
			Title:         textPreview,
			Description:   textPreview,
			SourceVersion: cmd.LegacyFileMD5,
			Tags:          semanticMeta.Tags,
			Extra:         extra,
		},
		Locations:      locations,
		EmitIndexEvent: cmd.LegacyFileMD5 != "",
	}
}
