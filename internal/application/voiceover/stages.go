// Package voiceover — stages.go (RESTORE PR, June 2026).
//
// Implements the per-batch orchestrator and the 3 stages that
// process.go's processLanguage calls into:
//
//   - GenerateBatch    (orchestrator) — validate, normalize,
//     resolve destination once,
//     fan out to processLanguage
//     per language, aggregate
//     response
//   - synthesizeStage  (Stage 1)      — TTS via audioProcessor,
//     populates LocalPath/CleanedPath/
//     Voice/FileHash
//   - destinationStage (Stage 2)      — Drive upload via
//     lifecycle.Service.ProcessAsset,
//     populates DriveLink/DriveFileID/
//     DownloadLink
//   - finalizeStage    (Stage 3)      — atomic SQLite INSERT inside a
//     single tx, PR-VO-A3 Outbox
//     enqueue inside the same tx,
//     PR-VO-B3 dedupe gate, then
//     post-commit cleanup goroutine
//     for replace-mode orphans
//
// The 4 symbols replace the PR-VOICEOVER-PROCESS-GO-FIX build-pass-only
// stub layer. process.go's processLanguage already wires the stage
// calls in the correct order (synthesize → meta-build bridge →
// destination → finalize); this file owns the bodies.
//
// Identifier convention: the magic string "PR-VOICEOVER-RESTORE"
// surfaces in WARN/INFO log messages so an operator can grep for
// the restoration scope and verify which stage actually executed.
//
// Site-detachment contracts (godlike/05 / AGENTS.md table):
//   - cleanupOrphanVoiceover uses context.Background — post-commit
//     save operations are exempt from the request-lifetime context
//     (the swap has committed; cleanup MUST survive handler cancel
//     and client disconnect so the orphan Drive file and old local
//     audio files are released).
package voiceover

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"go.uber.org/zap"
)

// restoreIdent is the canonical one-shot identifier embedded in the
// per-stage log messages. Operators can `rg restoreIdent internal/`
// to enumerate all restored surfaces.
const restoreIdent = "PR-VOICEOVER-RESTORE"

// voiceOverrideFor returns the canonical per-language voice override
// for a single language key from a BatchRequest's VoiceOverrides map.
// nil-safe (returns "" when req is nil, the map is nil, the key is
// missing, OR the value is empty). The empty-string return propagates
// downstream to TTSInput.Voice as the default-voice signal — the
// Python tts_edge.py --voice flag is only set when the override is
// present, so an empty Voice preserves the tts script's local
// voice-per-language defaulting path.
//
// PR-VO-AUDIT-P04 micro-commit #3 (June 2026): replaces the previous
// synthesizeStage hard-coded `Voice: ""` literal that dropped every
// per-language override silently. Audit-pin:
//   - TestProcessOneVoiceoverUseCase_PropagatesVoiceOverrideToTTSInput
//     (asserts end-to-end propagation from the item's scalar voice
//     through req.VoiceOverrides into TTSInput.Voice);
//   - TestTTSBridge_UsesPerLanguageVoice (asserts the synthesizeStage
//     lookup hits the canonical map);
//   - TestE2E_VoiceOverrideReachesPython (asserts the resolved voice
//     flows through to the Python tts_edge.py --voice flag).

// truncatePreview caps the text_preview metadata field at 100 chars
// to limit row size. Inline here (no textutil import) so stages.go
// keeps a tight import surface.
func truncatePreview(s string) string {
	if len(s) <= 100 {
		return s
	}
	return s[:100]
}

// GenerateBatch is the per-batch orchestrator called by the
// single-language wrappers (Generate, GenerateWithDestination) and
// the worker job handler (handleBatchJob).
//
// RESTORED body (June 2026):
//
//  1. Path-traversal rejection on req.Destination (preserved from
//     PR-VO-A4 so the contract test TestGenerateBatch_RejectsPathTraversalPayload
//     continues to fire fast-closed — Validate() runs BEFORE any
//     service-field access, only s.log is touched).
//  2. normalizeBatchRequest — fills defaults (template, strategy, lang).
//  3. Batch-level identifiers: requestID + textHash (computed once
//     for the batch — both are part of the row identity so they must
//     be stable across the per-language fan-out).
//  4. resolveDestination once (per req.Destination) — same folder +
//     StyleGroup for every language.
//  5. Per-language fan-out: processLanguage is the per-language
//     orchestrator (lives in process.go); it builds the BatchItem
//     and calls the 3 stages under stageLog telemetry wrappers.
//  6. Aggregate: response.OK = all items succeeded (otherwise false
//     so the caller can distinguish partial failure from full success).
//
// Why this is slim: each stage owns its own scope (synthesize /
// destination / finalize). GenerateBatch glues the per-language
// wiring only; the heavy lifting is below.
func (s *Service) GenerateBatch(ctx context.Context, req *BatchRequest) (*BatchResponse, error) {
	// PR-VO-A4 (path-traversal rejection): validate the inbound
	// destination BEFORE any field access on req. The pre-PR8
	// process.go called this gate inside processLanguage per
	// language, but the canonical pre-PR8 GenerateBatch also called
	// it at the entrypoint (so the entire batch fails-closed on
	// the first traversal payload rather than per-language). The
	// test TestGenerateBatch_RejectsPathTraversalPayload pins this
	// contract.
	if req != nil && req.Destination != nil {
		if vErr := req.Destination.Validate(); vErr != nil {
			if s.log != nil {
				s.log.Warn("PR-VO-A4: GenerateBatch rejected path-traversal payload",
					zap.String("restored", restoreIdent),
					zap.String("subfolder_name", req.Destination.SubfolderName),
					zap.Error(vErr))
			}
			return nil, vErr
		}
	}

	normalizeBatchRequest(req)

	if req.Destination == nil && s.cfg != nil && s.cfg.Drive.VoiceoverFolder() != "" {
		req.Destination = &DestinationRequest{
			FolderID: s.cfg.Drive.VoiceoverFolder(),
		}
	}

	// P0.6 request_id threading: use the caller-supplied request_id
	// when available (threaded from API → CorrelationID → fanout →
	// child cmd.RequestID → BatchRequest.RequestID). Only generate a
	// new buildRequestID() when no caller ID is present (legacy
	// batch/promo paths that don't thread the ID yet).
	requestID := req.RequestID
	if requestID == "" {
		requestID = buildRequestID()
	}
	textHash := hashutil.SHA256String(req.Text)

	// PR-VO-AUDIT-P02 (June 2026): the legacy gate
	// `if req.Destination != nil` is REMOVED. The canonical destination
	// resolver (destination_resolver.go::ResolveVoiceoverDestination)
	// handles nil dest via its nil-dest branch which falls back to the
	// configured cfg.Drive.VoiceoverFolder() (or surfaces
	// ErrMissingFolder when no default is configured). Keeping the
	// gate here would silently restore the pre-PR8 bug where a
	// nil-Destination request fell through the worker-side path with
	// `dest = nil` and failed at Stage 2 with `missing_folder_id` even
	// though the cfg had a valid voiceover folder configured.
	var dest *ResolvedDestination
	d, err := s.resolveDestination(ctx, req.Destination)
	if err != nil {
		if s.log != nil {
			s.log.Warn("GenerateBatch: resolveDestination failed",
				zap.String("restored", restoreIdent),
				zap.Error(err))
		}
		return nil, fmt.Errorf("GenerateBatch: resolve destination: %w", err)
	}
	dest = d

	items := make([]BatchItem, 0, len(req.Languages))
	ok := true
	for _, lang := range req.Languages {
		item := s.processLanguage(ctx, requestID, textHash, lang, req, dest)
		if item.Status == StatusFailed {
			ok = false
		}
		items = append(items, item)
	}

	return &BatchResponse{
		OK:        ok,
		RequestID: requestID,
		Items:     items,
	}, nil
}

// synthesizeStage is Stage 1 (TTS via audioProcessor). Wired
// between the stageLog("synthesize") wrappers in process.go.
//
// RESTORED body: invokes s.audioProcessor.Generate with the
// canonical AudioInput shape. On success populates LocalPath,
// CleanedPath, Voice, FileHash on the BatchItem. On error the
// item.fail plumbing surfaces a BatchItem with Status="tts_failed"
// for the caller to observe.
//
// Note: process.go's processLanguage calls this inside stageLog
// telemetry — durations and errors are logged at the stage-wrapper
// level, so this method only NEEDS to surface errors, not durations.
func (s *Service) synthesizeStage(
	ctx context.Context,
	item BatchItem,
	req *BatchRequest,
	outputDir string,
	filename string,
	language string,
) BatchItem {
	if s.ttsProvider == nil {
		return item.fail(FailureTTSProviderUnavailable,
			fmt.Errorf("%s: ttsProvider not wired (composition root)", restoreIdent))
	}

	removeSilence := false
	if req.RemoveSilence != nil {
		removeSilence = *req.RemoveSilence
	}

	// TTSInput is the canonical voiceover port wire-shape (defined
	// in voiceover/ports.go). The useCaseTTSAdapter bridge (in
	// internal/app/adapters_voiceover_use_case.go) maps TTSInput
	// fields 1-a-1 onto audioasset.AudioInput so the production
	// *audioasset.Processor receives the same shape it would have
	// received pre-P1-2. Voice field defaults to empty (legacy
	// synthesize behavior — auto-detected from TTS script).
	// TTSInput is the canonical voiceover port wire-shape (defined
	// in voiceover/ports.go). The useCaseTTSAdapter bridge (in
	// internal/app/adapters_voiceover_use_case.go) maps TTSInput
	// fields 1-a-1 onto audioasset.AudioInput so the production
	// *audioasset.Processor receives the same shape it would have
	// received pre-P1-2.
	//
	// PR-VO-AUDIT-P04 micro-commit #3 (June 2026): the Voice field is
	// populated from the canonical req.VoiceOverrides[language] lookup
	// (via voiceOverrideFor helper at the bottom of this file). nil-safe:
	// voiceOverrideFor returns "" when the map is missing or the
	// language key is missing, which propagates downstream to the
	// tts_edge.py --voice flag as the default-voice path. Pre-P0.4
	// this lookup was missing — the legacy code hard-coded
	// `Voice: ""`, so per-language voice overrides in
	// req.VoiceOverrides were silently dropped at Stage 1 before
	// reaching the Python bridge.
	input := TTSInput{
		Text:          req.Text,
		Language:      language,		Voice: voiceOverrideFor(req, language),
		Filename:      filename,
		OutputDir:     outputDir,
		RemoveSilence: removeSilence,
	}

	result, err := s.ttsProvider.Synthesize(ctx, input)
	if err != nil {
		if s.log != nil {
			s.log.Warn("synthesizeStage: TTS failed",
				zap.String("restored", restoreIdent),
				zap.String("language", language),
				zap.Error(err))
		}
		return item.fail(FailureTTS, err)
	}

	item.LocalPath = result.LocalPath
	item.CleanedPath = result.CleanedPath
	item.Voice = result.Voice
	item.FileHash = result.FileHash
	item.Status = StatusGenerated
	return item
}

func voiceOverrideFor(req *BatchRequest, language string) string {
	if req == nil || len(req.VoiceOverrides) == 0 {
		return ""
	}
	return req.VoiceOverrides[language]
}

// destinationStage is Stage 2 (Drive upload via Lifecycle). Wired
// between the stageLog("destination") wrappers in process.go.
//
// P0.7 Wave 21 (June 2026) — Step 9/12 finalizer unification: this
// stage now calls lifecycle.Service.UploadOnly (Drive only, NO DB
// writes). The previous ProcessAsset call wrote media_assets at
// Stage 2 and finalizeStage then ALSO wrote voiceovers in a SECOND
// tx — the partial-save bug pattern. Removing ProcessAsset from
// the Stage-2 surface eliminates the partial-save because NOTHING
// is persisted until finalizeStage's tx commits; a tx failure aborts
// the entire atomic-write, and the replace-mode cleanup goroutine
// handles the orphan Drive file downstream.
//
// On success populates DriveLink/DriveFileID/DownloadLink and
// advances Status to StatusUploaded. On error the item.fail
// plumbing surfaces a BatchItem with typed FailureUpload (matches
// the audit-P01 fail() contract — typed status, NOT legacy literal).
func (s *Service) destinationStage(
	ctx context.Context,
	item BatchItem,
	req *BatchRequest,
	dest *ResolvedDestination,
	metaJSON []byte,
) BatchItem {
	if s.lifecycleService == nil {
		return item.fail(FailureLifecycleUnavailable,
			fmt.Errorf("%s: lifecycleService not wired (composition root)", restoreIdent))
	}
	if dest == nil || dest.FolderID == "" {
		return item.fail(FailureMissingFolder,
			fmt.Errorf("%s: destination has no FolderID (Stage 2 cannot upload)", restoreIdent))
	}
	if item.CleanedPath == "" && item.LocalPath == "" {
		return item.fail(FailureNoLocalPayload,
			fmt.Errorf("%s: synthesizeStage produced no local path (Stage 2 cannot upload)", restoreIdent))
	}

	localPath := item.CleanedPath
	if localPath == "" {
		localPath = item.LocalPath
	}

	finalInput := &lifecycle.FinalizeInput{
		ID:           item.ID,
		Name:         truncatePreview(req.Text),
		Filename:     item.Filename,
		LocalPath:    localPath,
		FolderID:     dest.FolderID,
		FolderPath:   dest.FolderPath,
		Source:       "voiceover",
		Metadata:     string(metaJSON),
		RequireDrive: true,
	}

	// P0.7 2-PHASE SPLIT (Step 9/12): UploadOnly uploads to Drive
	// without writing to the DB. The phase-2 writes (voiceovers +
	// media_assets projection + outbox event) happen inside
	// finalizeStage's caller-owned tx. See lifecycle.Service.UploadOnly
	// for the atomicity rationale.
	result, err := s.lifecycleService.UploadOnly(ctx, finalInput)
	if err != nil {
		if s.log != nil {
			s.log.Warn("destinationStage: lifecycle.UploadOnly failed (Phase 1)",
				zap.String("restored", restoreIdent),
				zap.String("language", item.Language),
				zap.Error(err))
		}
		return item.fail(FailureUpload, err)
	}

	item.DriveLink = result.DriveLink
	item.DriveFileID = result.DriveFileID
	item.DownloadLink = result.DownloadLink
	item.Status = StatusUploaded
	return item
}

// finalizeStage is Stage 3 (PR-VO-B3 dedupe gate + PR-VO-A2 atomic
// swap + PR-VO-A3 outbox + media_assets projection UPSERT +
// post-commit cleanup). Wired between the stageLog("finalize")
// wrappers in process.go.
//
// RESTORED body:
//
//  1. Open sqlite tx (BeginTx with parent ctx so a request cancel
//     aborts the tx cleanly).
//  2. PR-VO-B3 dedupe gate runs INSIDE the tx so the count query is
//     consistent with the upcoming INSERT. Empty driveFileID
//     short-circuits to nil (no gate).
//  3. PR-VO-A2 atomic swap: DELETE existing same-id row, INSERT
//     new row in the same tx. The pre-read in processLanguage
//     already captured oldDriveFileID / oldLocalPath /
//     oldCleanedPath for the cleanup goroutine.
//  4. P0.7 2-PHASE SPLIT (Step 9/12): UPSERT the media_assets
//     projection row INSIDE the same tx via
//     lifecycle.Service.UpsertVoiceoverProjectionTx. This is the
//     canonical projection of the canonical voiceover row, with
//     `source='voiceover'` discriminator. a SQL verification query
//     at `internal/application/voiceover/verify_media_assets.go`
//     pins the contract: SELECT 1 FROM media_assets WHERE id=? AND
//     source='voiceover'.
//  5. PR-VO-A3 outbox enqueue inside the same tx so the row INSERT
//     and the index event INSERT commit atomically. Nil-safe so
//     the indexing degrades gracefully if the composition root
//     didn't wire the outbox.
//  6. Commit.
//  7. Post-commit cleanup goroutine (Strategy=="replace" ONLY) —
//     detached from the request ctx via context.Background
//     (per AGENTS.md post-write save exemption table) so the
//     orphan Drive file + old local audio files are released even
//     after the handler has cancelled or the client has
//     disconnected.
//
// Why a single tx: PR-VO-A2 (Replace-Safe pipeline) requires that
// the OLD voiceover record is never removed UNTIL the NEW one is
// durably persisted — a separate DELETE-then-INSERT pair would
// leave a data-loss window if TTS or Drive or Lifecycle failed
// downstream.
//
// P0.7 2-PHASE SPLIT history: pre-Step 9/12 the same canonical
// content was written TWICE (lifecycle.ProcessAsset's media_assets
// UPSERT at Stage 2 + finalizeStage's voiceovers INSERT/outbox +
// Commit) across TWO transactions; a Drive upload success followed
// by an InsertTx failure would leave an orphan media_assets row.
// Removing ProcessAsset from destinationStage + adding
// UpsertVoiceoverProjectionTx here produces a SINGLE-TX atomic
// write that covers voiceovers + media_assets + outbox; any tx
// failure aborts ALL three writes; the orphan Drive file is then
// handled by the replace-mode cleanup goroutine.
func (s *Service) finalizeStage(
	ctx context.Context,
	item BatchItem,
	requestID string,
	textHash string,
	language string,
	req *BatchRequest,
	dest *ResolvedDestination,
	metaJSON []byte,
	shouldSwap bool,
	oldDriveFileID string,
	oldLocalPath string,
	oldCleanedPath string,
) BatchItem {
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

	// PR-VO-B3 dedupe gate (Step 7/12, June 2026 — gate operativo).
	// Runs INSIDE the tx so the count query is consistent with the
	// upcoming INSERT. Defensive: an empty driveFileID short-circuits
	// to (matchedID="", count=0, nil) (no gate fired). The
	// previously-inlined log-and-fall-through block has been
	// replaced by a `switch DecideDedupe(count)` that ACTS on the
	// canonical DedupeDecision verdict (Continue/Reuse/Conflict)
	// rather than logging and continuing into the PR-VO-A2
	// atomic-swap. P1-2 (June 2026): the previously-inlined
	// applyDedupeByDriveFileID call was replaced by the
	// persistence.Repository.CountByDriveFileIDTx port method.
	if item.DriveFileID != "" {
		matchedID, count, _ := s.voiceoverRepo.CountByDriveFileIDTx(ctx, tx, item.ID, item.DriveFileID)
		switch DecideDedupe(count) {
		case DedupeConflict:
			if s.log != nil {
				s.log.Warn("PR-VO-B3 dedupe conflict — FAIL-CLOSED, NOT inserting duplicate",
					zap.String("restored", restoreIdent),
					zap.String("item_id", item.ID),
					zap.Int("count", count),
					zap.String("dedupe_id", matchedID),
					zap.String("decision", string(DedupeConflict)))
			}
			return item.fail(FailureDedupeAmbiguous,
				fmt.Errorf("%s: PR-VO-B3 ambiguous dedupe (count=%d, dedupe_id=%s) — refusing to insert duplicate row against established DriveFileID",
					restoreIdent, count, matchedID))
		case DedupeReuse:
			if s.log != nil {
				s.log.Info("PR-VO-B3 dedupe reuse — matched single prior row, skipping insert",
					zap.String("restored", restoreIdent),
					zap.String("item_id", item.ID),
					zap.String("dedupe_id", matchedID),
					zap.String("decision", string(DedupeReuse)))
			}
			item.ID = matchedID
			item.Status = StatusCompleted
			return item
		case DedupeContinue:
			if s.log != nil {
				s.log.Debug("PR-VO-B3 dedupe continue — no match, proceeding with insert",
					zap.String("restored", restoreIdent),
					zap.String("item_id", item.ID),
					zap.String("decision", string(DedupeContinue)))
			}
		}
	}

	// PR-VO-A2 atomic swap: DELETE any existing row with the same id,
	// then INSERT the new row, in the same tx. Neither is visible
	// alone (atomic visibility = single SQLite commit). The
	// DELETE and INSERT go through the persistence.Repository port
	// (P1-2 boundary split, June 2026) so the application layer no
	// longer references voiceovers schema or *sql.Tx.ExecContext.
	if err := s.voiceoverRepo.DeleteByIDTx(ctx, tx, item.ID); err != nil {
		return item.fail(FailureDBDelete,
			fmt.Errorf("%s: DeleteByIDTx: %w", restoreIdent, err))
	}

	folderID := ""
	folderPath := ""
	if dest != nil {
		folderID = dest.FolderID
		folderPath = dest.FolderPath
	}

	now := time.Now().UTC().Format(time.RFC3339)
	textPreview := truncatePreview(req.Text)

	rec := &persistence.VoiceoverRecord{
		ID:           item.ID,
		RequestID:    requestID,
		TextHash:     textHash,
		TextPreview:  textPreview,
		Language:     language,
		Voice:        item.Voice,
		Filename:     item.Filename,
		LocalPath:    item.LocalPath,
		CleanedPath:  item.CleanedPath,
		FolderID:     folderID,
		FolderPath:   folderPath,
		DriveFileID:  item.DriveFileID,
		DriveLink:    item.DriveLink,
		DownloadLink: item.DownloadLink,
		FileHash:     item.FileHash,
		Status:       string(StatusGenerated),
		Error:        "",
		Strategy:     req.Strategy,
		Metadata:     string(metaJSON),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.voiceoverRepo.InsertTx(ctx, tx, rec); err != nil {
		return item.fail(FailureDBInsert,
			fmt.Errorf("%s: InsertTx: %w", restoreIdent, err))
	}

	// P0.7 2-PHASE SPLIT (Step 9/12): UPSERT the canonical
	// media_assets projection row in the SAME caller-owned tx. The
	// projection uses `source='voiceover'` as the canonical
	// discriminator so the SQL verification helper at
	// `verify_media_assets.go::HasVoiceoverProjection` can pin the
	// contract (SELECT 1 FROM media_assets WHERE id=? AND
	// source='voiceover'). The UPSERT is idempotent — a re-run of
	// finalizeStage updates the projected columns in place, no
	// double-insert. PipelineGen upgrade note: pre-Step 9/12 the
	// lifecycle.ProcessAsset call at destinationStage wrote this row
	// in a SEPARATE earlier tx; a Drive upload success followed by
	// an InsertTx failure left an orphan media_assets row. With
	// ProcessAsset removed (calling UploadOnly instead) + this
	// UPSERT added, all persistence is in a SINGLE tx — partial-save
	// is impossible.
	if err := s.lifecycleService.UpsertVoiceoverProjectionTx(ctx, tx, &lifecycle.VoiceoverProjectionInput{
		ID:           item.ID,
		Source:       "voiceover",
		Name:         textPreview,
		Filename:     item.Filename,
		FolderID:     folderID,
		FolderPath:   folderPath,
		MediaType:    "audio",
		LocalPath:    item.LocalPath,
		DriveFileID:  item.DriveFileID,
		DriveLink:    item.DriveLink,
		DownloadLink: item.DownloadLink,
		FileHash:     item.FileHash,
		Language:     language,
		Status:       string(StatusGenerated),
		Metadata:     string(metaJSON),
	}); err != nil {
		return item.fail(FailureDBInsert,
			fmt.Errorf("%s: UpsertVoiceoverProjectionTx (media_assets): %w", restoreIdent, err))
	}

	// PR-VO-A3 outbox enqueue: inside the same tx so the row INSERT
	// and the index event INSERT commit atomically. Nil-safe so the
	// behaviour degrades to "skip indexing" if the composition root
	// didn't wire the outbox.
	if s.outboxEnqueuer != nil && item.FileHash != "" {
		if err := s.outboxEnqueuer.EnqueueIndexEvent(ctx, tx, item.ID, item.FileHash); err != nil {
			return item.fail(FailureOutboxEnqueue,
				fmt.Errorf("%s: EnqueueIndexEvent: %w", restoreIdent, err))
		}
	}

	if err := tx.Commit(); err != nil {
		return item.fail(FailureTxCommit,
			fmt.Errorf("%s: Commit: %w", restoreIdent, err))
	}

	// Post-commit cleanup for replace-mode orphans. Detached from
	// the request ctx (per AGENTS.md post-write save exemption) so
	// the cleanup survives handler cancel + client disconnect.
	if shouldSwap && (oldDriveFileID != "" || oldLocalPath != "" || oldCleanedPath != "") {
		go s.cleanupOrphanVoiceover(oldDriveFileID, oldLocalPath, oldCleanedPath)
	}

	item.Status = StatusCompleted
	return item
}

// cleanupOrphanVoiceover removes the OLD Drive file + OLD local
// audio files for a replace-mode voiceover swap. Detached from any
// request context (per AGENTS.md post-write save exemption table
// — internal/application/assets/providers/artlist/search_cache.go
// documents the same pattern for the search-cache bump).
//
// Failures are logged but never propagated — the swap has already
// committed so the canonical state is the NEW row, not the orphan.
// Background timeout 60s covers the slowest Drive file delete
// plus both local file removes.
func (s *Service) cleanupOrphanVoiceover(driveFileID, oldLocalPath, oldCleanedPath string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if driveFileID != "" && s.driveUploader != nil {
		if err := s.driveUploader.DeleteFile(ctx, driveFileID); err != nil {
			if s.log != nil {
				s.log.Warn("cleanupOrphanVoiceover: Drive file delete failed",
					zap.String("restored", restoreIdent),
					zap.String("drive_file_id", driveFileID),
					zap.Error(err))
			}
		}
	}
	for _, p := range []string{oldLocalPath, oldCleanedPath} {
		if p == "" {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			if s.log != nil {
				s.log.Warn("cleanupOrphanVoiceover: local file remove failed",
					zap.String("restored", restoreIdent),
					zap.String("path", p),
					zap.Error(err))
			}
		}
	}
}
