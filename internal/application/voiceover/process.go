// Package voiceover — process.go (PR8 SLIM ORCHESTRATOR, June 2026).
//
// Per-language processLanguage orchestrator extracted from the
// monolithic 695-line pre-PR8 process.go. Owns:
//
//  1. func (s *Service) Generate — single-language wrapper over
//     GenerateBatch. Caller- compat preserved verbatim.
//
//  2. func (s *Service) processLanguage — per-language orchestrator
//     that calls the 3 PR8 stages in order:
//
//  1. synthesizeStage (TTS via audioProcessor)
//
//  2. destinationStage (Drive upload via Lifecycle)
//
//  3. finalizeStage (PR-VO-B3 dedupe gate + PR-VO-A2 atomic
//     swap + post-commit cleanup goroutine)
//     Each stage call is wrapped in pipeline_stage_started /
//     pipeline_stage_completed telemetry per AGENTS.md Pattern 3.
//
// Mechanical extraction. processLanguage's pre-compute / pre-read
// / MkdirAll / SanitizeFilename / meta-build / meta-merge blocks
// stay inline (they are bridges between stages, not their own
// stages). Bodies are verbatim from pre-PR8 source; the new
// behavior is the stage-call shape + telemetry.
//
// tools.Progress wiring: HandleJob in job_handler.go does NOT pass
// JobTools to handleBatchJob today (verified pre-PR8), so the
// voiceover stage telemetry surfaces only via log emission. A
// future PR could plumb JobTools through handleBatchJob to also
// surface stage progress on the worker progress channel.
package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// stageLog returns a closure that emits pipeline_stage_started now
// and pipeline_stage_completed when invoked. Mirrors the
// scripts/jobs/job_helpers.go canonical emission pattern (Pattern
// 3, AGENTS.md).
func stageLog(log *zap.Logger, requestID, stage, language string) func() {
	start := time.Now()
	log.Info("pipeline_stage_started",
		zap.String("stage", stage),
		zap.String("job_id", requestID),
		zap.String("language", string(language)))
	return func() {
		log.Info("pipeline_stage_completed",
			zap.String("stage", stage),
			zap.String("job_id", requestID),
			zap.String("language", string(language)),
			zap.Int64("duration_ms", time.Since(start).Milliseconds()))
	}
}

// PR-VO-AUDIT-P02 (June 2026): the inline cfg.Drive.VoiceoverFolder()
// fallback that used to pre-populate req.Destination here has been
// REMOVED. The fallback now lives in the canonical destination
// resolver (destination_resolver.go::ResolveVoiceoverDestination)
// so Service.Generate → Service.GenerateBatch → Service.resolveDestination
// route identically with the worker-side call paths. Pre-refactor
// behaviour: nil req.Destination was a hard missing_folder_id in
// stages.go::GenerateBatch (the gate `if req.Destination != nil`)
// — the cfg-fallback surfaced only via THIS single-language wrapper
// which silently diverged from the worker-side path. The audit calls
// that the BACKFILL/CUTOVER-incomplete state of the voiceover module.
// Post-refactor: nil req.Destination is a valid input that the
// canonical resolver handles via its nil-dest branch.
func (s *Service) Generate(ctx context.Context, text, language, filename string) (*VoiceoverResult, error) {
	req := &BatchRequest{
		Text:             text,
		// PR-VO-TYPED-PRIMITIVES (July 2026): untyped string literal
		// implicitly converts to the Language named type.
		Languages:        []Language{Language(language)},
		FilenameTemplate: filename,
		RemoveSilence:    ptrutil.Bool(false),
		Strategy:         "replace",
	}
	resp, err := s.GenerateBatch(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(resp.Items) == 0 {
		return nil, fmt.Errorf("no voiceover generated")
	}

	item := resp.Items[0]
	if !item.isSuccessful() {
		msg := item.Error
		if msg == "" {
			msg = "voiceover generation did not complete"
		}
		return nil, fmt.Errorf("%s (status: %s)", msg, item.Status)
	}

	return &VoiceoverResult{
		OK:          true,
		Voice:       item.Voice,
		Path:        item.LocalPath,
		DriveLink:   item.DriveLink,
		DriveFileID: item.DriveFileID,
	}, nil
}

func (s *Service) processLanguage(
	ctx context.Context,
	requestID string,
	// PR-VO-TYPED-PRIMITIVES (July 2026): textHash is raw string
	// (the legacy 64-char full SHA-256 of req.Text from
	// GenerateBatch). The typed TextHash envelope is canonical ONLY
	// for the per-item 16-char value used in the fan-out path.
	textHash string,
	language Language,
	req *BatchRequest,
	dest *ResolvedDestination,
) BatchItem {
	// E4: Service.buildFilename → canonical BuildVoiceoverFilename.
	// Inputs are pre-validated by req via the higher-layer
	// BatchRequest normalization (~line 472 in types.go).
	//
	// `item` is pre-declared so the early-return paths below
	// (filename build failure, subfolder sanitisation failure,
	// buffer-overflow rejection) can route via item.fail(...) — the
	// original PR8 extracted processLanguage from a monolithic
	// function and missed the var-declaration reorder.
	var item BatchItem
	filename, err := BuildVoiceoverFilename(FilenameSpec{
		Text:     req.Text,
		Language: language,
		TextHash: textHash,
		Template: req.FilenameTemplate,
	})
	if err != nil {
		return item.fail(FailureInvalidFilename, fmt.Errorf("filename build: %w", err))
	}

	folderID := ""
	if dest != nil {
		folderID = dest.FolderID
	}

	id := buildVoiceoverID(textHash, language, folderID)

	// PR-VO-A2 (Replace-Safe pipeline, June 2026): the previous code deleted the existing voiceover record
	// BEFORE generation, leaving a data-loss window if TTS / Drive / Lifecycle failed downstream.
	// The new flow threads the swap through a single SQLite transaction so the old record is
	// never removed until the new one is durably persisted.
	shouldSwap := req.Strategy == "replace"
	oldDriveFileID := ""
	oldLocalPath := ""
	oldCleanedPath := ""
	var existingDriveLink string
	// P1-2 boundary split (June 2026): the previous raw
	// s.db.QueryRowContext(...).Scan(...) block is replaced by a
	// PreReadByID call on the persistence.Repository port. Test
	// fixtures inject a stub persistence.Repository (see `stubRepo`
	// in service_p01_state_machine_test.go) satisfying
	// `var _ persistence.Repository = stubRepo{}` so the per-test
	// path threads the canonical surface without poking the
	// production SQLite layer.
	oldRec, preReadErr := s.voiceoverRepo.PreReadByID(ctx, id)
	if preReadErr != nil {
		s.log.Warn("processLanguage: pre-read of existing voiceover failed",
			zap.String("id", id), zap.Error(preReadErr))
		// Defensive — preserve the pre-PR-VO-A2 "carry on"
		// semantics so a transient pre-read error doesn't block the
		// swap stage from running.
		oldRec = nil
	}
	if oldRec != nil {
		oldDriveFileID = oldRec.DriveFileID
		existingDriveLink = oldRec.DriveLink
		oldLocalPath = oldRec.LocalPath
		oldCleanedPath = oldRec.CleanedPath
	}
	if !shouldSwap && folderID != "" && oldRec != nil && existingDriveLink == "" {
		// Existing row with no drive link — force regeneration so the persisted row
		// catches up to the freshly uploaded file.
		s.log.Info("processLanguage: existing record has no drive link, forcing swap", zap.String("id", id))
		shouldSwap = true
	}

	item = BatchItem{
		ID:       id,
		Language: language,
		Filename: filename,
		Status:   StatusProcessing,
	}

	outputDir := s.outputDir
	if req.Destination != nil && req.Destination.CreateSubfolder && req.Destination.SubfolderName != "" {
		// PR-VO-A4 (path-traversal fix, June 2026): defense in depth.
		safeSub, subErr := pathutil.SanitizeSubfolderSegment(req.Destination.SubfolderName)
			if subErr != nil {
				s.log.Warn("PR-VO-A4: rejected path-traversal payload in subfolder_name",
					zap.String("language", string(language)),
					zap.String("subfolder_name", req.Destination.SubfolderName),
					zap.Error(subErr))
				return item.fail(FailureInvalidSubfolder, fmt.Errorf("path traversal rejected: %w", subErr))
			}
		outputDir = filepath.Join(s.outputDir, safeSub)
		if werr := pathutil.EnsureWithinDir(s.outputDir, outputDir); werr != nil {
			s.log.Error("PR-VO-A4: filepath.Rel guard tripped (sanitizer and Rel disagree — investigate)",
				zap.String("output_dir", outputDir),
				zap.String("root", s.outputDir),
				zap.Error(werr))
			return item.fail(FailureInvalidSubfolder, fmt.Errorf("path escape rejected: %w", werr))
		}
		if err := os.MkdirAll(outputDir, 0755); err != nil { // Wave A Item 16: fail-fast on MkdirAll failure (was: log+continue)
			return item.fail(FailureInvalidSubfolder, fmt.Errorf("failed to create local subfolder %q: %w", outputDir, err))
			
		}
	}

	// Sanitize the filename against path traversal and enforce .mp3.
	safePath, err := SanitizeFilename(outputDir, filename)
	if err != nil {
		return item.fail(FailureInvalidFilename, err)
	}
	filename = filepath.Base(safePath)
	item.Filename = filename // keep persisted metadata in sync with sanitized path

	// ── Stage 1: synthesize (TTS) ──────────────────────────────────────
	emitSynthesizeCompleted := stageLog(s.log, requestID, "synthesize", string(language))
	item = s.synthesizeStage(ctx, item, req, outputDir, filename, language)
	emitSynthesizeCompleted()
	if item.Status == StatusFailed {
		return item
	}

	// Build meta map (kept inline — the bridge between synthesize and
	// destination stages; not its own stage file).
	//
	// PR-VO-TYPED-PRIMITIVES (July 2026): the "language" + "style_group"
	// values are now typed (Language / StyleGroup) — Go's named-type
	// rules let the typed string value live in a map[string]any
	// unchanged (the value IS a string at the interface{} level).
	// The JSON wire shape is byte-equivalent.
	meta := map[string]any{
		"text_hash":    textHash,
		"text_preview": textutil.Truncate(req.Text, 100),
		"language":     item.Language,
		"voice":        item.Voice,
		"strategy":     req.Strategy,
		"request_id":   requestID,
		"cleaned_path": item.CleanedPath,
	}
	if s.semanticTagger != nil {
		semResult, err := s.semanticTagger(ctx, req.Text, "", "voiceover", "voiceover")
		if err != nil {
			s.log.Warn("processLanguage: semantic tagger failed", zap.Error(err))
		} else {
			meta["search_text"] = semResult.SearchText
			meta["semantic_tags"] = semResult.Tags
			meta["semantic_subjects"] = semResult.Subjects
			meta["semantic_mood"] = semResult.Mood
			item.SearchText = semResult.SearchText
		}
	}
	mergeUserMetadata(meta, dest, req.Metadata, s.log)
	metaJSON, _ := json.Marshal(meta)

	// ── Stage 2: destination (Drive upload via Lifecycle) ──────────────
	emitDestinationCompleted := stageLog(s.log, requestID, "destination", string(language))
	item = s.destinationStage(ctx, item, req, dest, metaJSON)
	emitDestinationCompleted()
	if item.Status == StatusFailed {
		return item
	}

	// ── Stage 3: finalize (dedupe gate + atomic swap + cleanup) ───────
	emitFinalizeCompleted := stageLog(s.log, requestID, "finalize", string(language))
	item = s.finalizeStage(ctx, item, requestID, textHash, language, req, dest, metaJSON,
		shouldSwap, oldDriveFileID, oldLocalPath, oldCleanedPath)
	emitFinalizeCompleted()

	return item
}
