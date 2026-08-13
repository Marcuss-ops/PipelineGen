// Package voiceover — process.go (Azione #1, July 2026).
//
// The legacy batch path (processLanguage) now delegates to the shared
// ProcessSegmentUseCase.Execute runner instead of calling
// synthesizeStage/destinationStage/finalizeStage inline. The 5 legacy
// stage files (stage_synthesize.go, stage_destination.go,
// stage_finalize.go, stage_persist.go, stage_postprocess.go) have been
// REMOVED.
//
// processLanguage retains ONLY the pre-compute work:
//  1. Build filename + ID (via BuildVoiceoverFilename + buildVoiceoverID)
//  2. Pre-read old record (for replace-mode swap context)
//  3. Output dir + subfolder sanitization + filename sanitization
//  4. Delegate to ProcessSegmentUseCase.Execute for TTS → Publish → TX+Finalize
//  5. Map VoiceoverItemResult back to BatchItem
//
// The semantic tagging (previously inline after synthesizeStage) is
// intentionally removed — ProcessSegmentUseCase.Execute does its own
// meta building. This is a known tradeoff per the Azione #1 migration;
// semantic enrichment can be re-added to ProcessSegmentUseCase in a
// future BACKFILL wave.
//
// Per-stage telemetry (tts/audio_post/publish/finalize) is emitted
// by ProcessSegmentUseCase.Execute internally via the shared stageLog
// helper. processLanguage does NOT wrap Execute in its own stage —
// the inner per-stage calls are the canonical granular telemetry.
// Operators can correlate via request_id + language as before.
package voiceover

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
	"github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
	"go.uber.org/zap"
)

// stageLog returns a closure that emits pipeline_stage_started now
// and pipeline_stage_completed when invoked. Mirrors the
// scripts/jobs/job_helpers.go canonical emission pattern (Pattern
// 3, AGENTS.md).
//
// FASE 5 (VO-OPERATIONAL-READINESS, July 2026): added assetID and
// project fields per the structured logging contract. Every stage
// now logs: job_id, asset_id, project, language, stage, status,
// duration_ms.
//
// FASE 7 (July 2026): added histogram observation via
// observability.VoiceoverStageDuration. Used by
// ProcessSegmentUseCase.Execute for per-stage telemetry; the
// batch path (processLanguage) inherits this via its delegation
// to Execute — no more duplicated stageTiming closure.
func stageLog(log *zap.Logger, jobID, assetID, project, stage, language string) func(status string) {
	start := time.Now()
	logFields := []zap.Field{
		zap.String("stage", stage),
		zap.String("job_id", jobID),
		zap.String("asset_id", assetID),
		zap.String("language", language),
	}
	if project != "" {
		logFields = append(logFields, zap.String("project", project))
	}
	log.Info("pipeline_stage_started", logFields...)
	return func(status string) {
		dur := time.Since(start)
		completedFields := append(logFields,
			zap.Int64("duration_ms", dur.Milliseconds()),
			zap.String("status", status),
		)
		log.Info("pipeline_stage_completed", completedFields...)
		observability.VoiceoverStageDuration.WithLabelValues(stage).Observe(dur.Seconds())
	}
}

// voiceOverrideFor returns the canonical per-language voice override
// for a single language key from a BatchRequest's VoiceOverrides map.
// nil-safe (returns "" when req is nil, the map is nil, the key is
// missing, OR the value is empty). The empty-string return propagates
// downstream to TTSInput.Voice as the default-voice signal.
//
// Moved here from stage_synthesize.go (removed in Azione #1, July 2026).
func voiceOverrideFor(req *BatchRequest, language Language) string {
	if req == nil || len(req.VoiceOverrides) == 0 {
		return ""
	}
	return req.VoiceOverrides[string(language)]
}

// resolveVoiceForLanguage returns the canonical voice for a language.
// Explicit per-request overrides are allowed; all registry-derived voices
// are mandatory. Missing, disabled, or incomplete registry entries fail
// closed instead of delegating voice selection to the TTS bridge.
func resolveVoiceForLanguage(req *BatchRequest, language Language, registry asset.LanguageRegistry, log *zap.Logger) (string, error) {
	if voice := voiceOverrideFor(req, language); voice != "" {
		return voice, nil
	}
	if registry == nil {
		return "", fmt.Errorf("voice registry is not configured for language %q", language)
	}
	spec, ok := registry.Resolve(string(language))
	if !ok {
		return "", fmt.Errorf("voice registry has no entry for language %q", language)
	}
	if !spec.Enabled {
		return "", fmt.Errorf("voice registry disabled language %q", language)
	}
	if !spec.GenerateTTS {
		return "", fmt.Errorf("voice registry disables TTS for language %q", language)
	}
	if spec.EdgeTTSVoice == "" {
		return "", fmt.Errorf("voice registry has no TTS voice for language %q", language)
	}
	return spec.EdgeTTSVoice, nil
}

// processLanguage is the per-language orchestrator (Azione #1, July 2026).
//
// Pre-migration this called synthesizeStage → destinationStage →
// finalizeStage inline. Post-migration it delegates to the shared
// ProcessSegmentUseCase.Execute runner.
//
// Pre-compute work retained:
//   - BuildVoiceoverFilename + buildVoiceoverID (caller-side identity)
//   - Pre-read old record (replace-mode swap context)
//   - Output dir + subfolder sanitization + SanitizeFilename
//
// Semantic enrichment is owned by ProcessSegmentUseCase.publishStage,
// the single shared path immediately before publish. The batch runner
// supplies its configured SemanticTagger to that use case; callers that
// do not wire a tagger intentionally retain the best-effort empty-metadata
// behavior. Per-language voice overrides are resolved here (voiceOverrideFor)
// and passed through ProcessSegmentCommand.Voice.

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
	// Nil-safety: processSeg must be wired by the composition root.
	// Tests that construct Service{} directly without wiring processSeg
	// should be migrated to test ProcessSegmentUseCase.Execute directly.
	if s.processSeg == nil {
		var item BatchItem
		return item.fail(FailureDBUnavailable,
			fmt.Errorf("processLanguage: processSeg not wired (composition root — Azione #1 requires ProcessSegmentUseCase; tests should wire processSeg or call ProcessSegmentUseCase.Execute directly)"))
	}

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

	// Pre-read old record for replace-mode swap context (Azione #9).
	shouldSwap, oldDriveFileID, oldLocalPath, oldCleanedPath := s.readSwapContext(ctx, id, req.Strategy, folderID)

	item = BatchItem{
		ID:       id,
		Language: language,
		Filename: filename,
		Status:   StatusProcessing,
	}

	// Resolve output directory with optional subfolder (Azione #9).
	outputDir, subErr := s.ensureOutputDir(s.outputDir, req.Destination, language)
	if subErr != nil {
		return item.fail(FailureInvalidSubfolder, subErr)
	}

	// Sanitize the filename against path traversal and enforce .mp3.
	safePath, err := SanitizeFilename(outputDir, filename)
	if err != nil {
		return item.fail(FailureInvalidFilename, err)
	}
	filename = filepath.Base(safePath)
	item.Filename = filename

	// Build ProcessSegmentCommand and delegate to the shared runner.
	// Create a shallow copy of dest so we can override FolderPath with
	// the subfolder-aware outputDir without mutating the caller's copy.
	if dest == nil {
		return item.fail(FailureMissingFolder,
			fmt.Errorf("processLanguage: dest is nil (GenerateBatch resolveDestination should have caught this)"))
	}
	processDest := *dest
	processDest.FolderPath = outputDir

	removeSilence := false
	if req.RemoveSilence != nil {
		removeSilence = *req.RemoveSilence
	}

	// Resolve the canonical Edge TTS voice for this language.
	// Per-request VoiceOverrides win, then the language registry. Missing,
	// disabled, or incomplete registry entries fail closed.
	voice, voiceErr := resolveVoiceForLanguage(req, language, s.languageRegistry, s.log)
	if voiceErr != nil {
		return item.fail(FailureVoiceRegistry, voiceErr)
	}

	jobID := ""
	if val, ok := ctx.Value("script_job_id").(string); ok {
		jobID = val
	}
	if jobID == "" {
		jobID = corid.FromContext(ctx)
	}

	cmd := &ProcessSegmentCommand{
		ID:             id,
		JobID:          jobID,
		RequestID:      requestID,
		TextHash:       TextHash(textHash),
		Text:           req.Text,
		Language:       language,
		Voice:          voice,
		Filename:       filename,
		Strategy:       req.Strategy,
		Metadata:       req.Metadata,
		RemoveSilence:  removeSilence,
		Timing:         req.Timing,
		Dest:           &processDest,
		Project:        req.Project,
		ShouldSwap:     shouldSwap,
		OldDriveFileID: oldDriveFileID,
		OldLocalPath:   oldLocalPath,
		OldCleanedPath: oldCleanedPath,
	}

	// Per-stage telemetry (tts/audio_post/publish/finalize) is emitted
	// by ProcessSegmentUseCase.Execute internally via stageLog.
	// No outer wrapper here — that would mix a coarse "pipeline" label
	// into the same VoiceoverStageDuration histogram as the fine-grained
	// per-stage labels, breaking p99 aggregation.
	out, runErr := s.processSeg.Execute(ctx, cmd)

	if out == nil {
		return item.fail(FailureTTS, fmt.Errorf("pipeline_run_failed: %v", runErr))
	}

	// Log the underlying error when present (structured logging
	// path — operators can search by request_id + language).
	if runErr != nil {
		s.log.Warn("processLanguage: pipeline completed with error",
			zap.String("job_id", requestID),
			zap.String("asset_id", id),
			zap.String("language", string(language)),
			zap.String("status", string(out.Status)),
			zap.Error(runErr))
	}

	// Map VoiceoverItemResult back to BatchItem. The runner publishes
	// ErrorCode as the canonical machine-readable failure classification;
	// this boundary never parses human-readable Error text.
	item.ID = out.ID
	item.Language = out.Language
	item.Voice = out.Voice
	item.Filename = out.Filename
	item.Status = out.Status
	item.Error = out.Error
	item.ErrorCode = out.ErrorCode
	item.LocalPath = out.LocalPath
	item.CleanedPath = out.CleanedPath
	item.FileHash = out.FileHash
	item.SearchText = out.SearchText
	item.DriveLink = out.DriveLink
	item.DriveFileID = out.DriveFileID
	item.DownloadLink = out.DownloadLink

	if out.Status == StatusFailed && out.ErrorCode != "" {
		item.Errors = append(item.Errors, FailureCode(out.ErrorCode))
	}

	return item
}

// readSwapContext pre-reads the existing voiceover record for replace-mode
// swap context. Returns the swap flag and old-row identifiers (empty when
// no prior row exists). Logs pre-read failures as warnings without aborting
// (the batch path tolerates a missing pre-read — it just loses the swap
// context and treats it as a fresh insert on the next finalize).
//
// Azione #9 (July 2026): extracted from processLanguage to reduce
// cyclomatic complexity.
func (s *Service) readSwapContext(ctx context.Context, id, strategy, folderID string) (shouldSwap bool, oldDriveFileID, oldLocalPath, oldCleanedPath string) {
	shouldSwap = strategy == "replace"

	oldRec, preReadErr := s.voiceoverRepo.PreReadByID(ctx, id)
	if preReadErr != nil {
		s.log.Warn("processLanguage: pre-read of existing voiceover failed",
			zap.String("id", id), zap.Error(preReadErr))
		return
	}
	if oldRec == nil {
		return
	}

	oldDriveFileID = oldRec.DriveFileID
	oldLocalPath = oldRec.LocalPath
	oldCleanedPath = oldRec.CleanedPath

	if !shouldSwap && folderID != "" && oldRec.DriveLink == "" {
		s.log.Info("processLanguage: existing record has no drive link, forcing swap", zap.String("id", id))
		shouldSwap = true
	}
	return
}

// ensureOutputDir resolves the local output directory for a per-language
// batch item, applying subfolder sanitization when req.Destination specifies
// CreateSubfolder + SubfolderName. Returns the resolved output directory
// or an error (path traversal rejected, guard mismatch, or mkdir failure).
//
// Azione #9 (July 2026): extracted from processLanguage to reduce
// cyclomatic complexity.
func (s *Service) ensureOutputDir(baseOutputDir string, dest *DestinationRequest, language Language) (string, error) {
	if dest == nil || !dest.CreateSubfolder || dest.SubfolderName == "" {
		return baseOutputDir, nil
	}

	safeSub, subErr := pathutil.SanitizeSubfolderSegment(dest.SubfolderName)
	if subErr != nil {
		s.log.Warn("PR-VO-A4: rejected path-traversal payload in subfolder_name",
			zap.String("language", string(language)),
			zap.String("subfolder_name", dest.SubfolderName),
			zap.Error(subErr))
		return "", fmt.Errorf("path traversal rejected: %w", subErr)
	}

	outputDir := filepath.Join(baseOutputDir, safeSub)
	if werr := pathutil.EnsureWithinDir(baseOutputDir, outputDir); werr != nil {
		s.log.Error("PR-VO-A4: filepath.Rel guard tripped (sanitizer and Rel disagree — investigate)",
			zap.String("output_dir", outputDir),
			zap.String("root", baseOutputDir),
			zap.Error(werr))
		return "", fmt.Errorf("path escape rejected: %w", werr)
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create local subfolder %q: %w", outputDir, err)
	}
	return outputDir, nil

}
