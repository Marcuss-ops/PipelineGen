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
//        1. synthesizeStage (TTS via audioProcessor)
//        2. destinationStage (Drive upload via Lifecycle)
//        3. finalizeStage (PR-VO-B3 dedupe gate + PR-VO-A2 atomic
//           swap + post-commit cleanup goroutine)
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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		zap.String("language", language))
	return func() {
		log.Info("pipeline_stage_completed",
			zap.String("stage", stage),
			zap.String("job_id", requestID),
			zap.String("language", language),
			zap.Int64("duration_ms", time.Since(start).Milliseconds()))
	}
}

func (s *Service) Generate(ctx context.Context, text, language, filename string) (*VoiceoverResult, error) {
	req := &BatchRequest{
		Text:             text,
		Languages:        []string{language},
		FilenameTemplate: filename,
		RemoveSilence:    ptrutil.Bool(false),
		Strategy:         "replace",
	}
	if s.cfg.Drive.VoiceoverFolder() != "" {
		req.Destination = &DestinationRequest{
			FolderID: s.cfg.Drive.VoiceoverFolder(),
		}
	}
	resp, err := s.GenerateBatch(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(resp.Items) == 0 {
		return nil, fmt.Errorf("no voiceover generated")
	}

	item := resp.Items[0]
	if item.Error != "" {
		return nil, fmt.Errorf("%s (status: %s)", item.Error, item.Status)
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
	textHash string,
	language string,
	req *BatchRequest,
	dest *ResolvedDestination,
) BatchItem {
	filename := s.buildFilename(req, language, textHash)

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
	// PR-VO-A2: pre-read captures the orphan-candidate rows in single SELECT
	// so the post-commit best-effort cleanup goroutine can remove both the
	// orphaned Drive file and the local audio file in one trip. errors.Is
	// check on sql.ErrNoRows is the canonical idiom — relying on the
	// message string of the underlying error (e.g. "sql: no rows in
	// result set") couples this code to a specific driver / Go version.
	rowErr := s.db.QueryRowContext(ctx,
		"SELECT drive_file_id, drive_link, local_path, cleaned_path FROM voiceovers WHERE id = ?", id,
	).Scan(&oldDriveFileID, &existingDriveLink, &oldLocalPath, &oldCleanedPath)
	if rowErr != nil && !errors.Is(rowErr, sql.ErrNoRows) {
		s.log.Warn("processLanguage: pre-read of existing voiceover failed", zap.String("id", id), zap.Error(rowErr))
	}
	if !shouldSwap && folderID != "" && rowErr == nil && existingDriveLink == "" {
		// Existing row with no drive link — force regeneration so the persisted row
		// catches up to the freshly uploaded file.
		s.log.Info("processLanguage: existing record has no drive link, forcing swap", zap.String("id", id))
		shouldSwap = true
	}

	item := BatchItem{
		ID:       id,
		Language: language,
		Filename: filename,
		Status:   "processing",
	}

	outputDir := s.outputDir
	if req.Destination != nil && req.Destination.CreateSubfolder && req.Destination.SubfolderName != "" {
		// PR-VO-A4 (path-traversal fix, June 2026): defense in depth.
		safeSub, subErr := pathutil.SanitizeSubfolderSegment(req.Destination.SubfolderName)
		if subErr != nil {
			s.log.Warn("PR-VO-A4: rejected path-traversal payload in subfolder_name",
				zap.String("language", language),
				zap.String("subfolder_name", req.Destination.SubfolderName),
				zap.Error(subErr))
			return item.fail("invalid_subfolder_name", fmt.Errorf("path traversal rejected: %w", subErr))
		}
		outputDir = filepath.Join(s.outputDir, safeSub)
		if werr := pathutil.EnsureWithinDir(s.outputDir, outputDir); werr != nil {
			s.log.Error("PR-VO-A4: filepath.Rel guard tripped (sanitizer and Rel disagree — investigate)",
				zap.String("output_dir", outputDir),
				zap.String("root", s.outputDir),
				zap.Error(werr))
			return item.fail("invalid_subfolder_name", fmt.Errorf("path escape rejected: %w", werr))
		}
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			s.log.Warn("failed to create local subfolder for voiceover", zap.String("dir", outputDir), zap.Error(err))
			outputDir = s.outputDir
		}
	}

	// Sanitize the filename against path traversal and enforce .mp3.
	safePath, err := SanitizeFilename(outputDir, filename)
	if err != nil {
		return item.fail("invalid_filename", err)
	}
	filename = filepath.Base(safePath)
	item.Filename = filename // keep persisted metadata in sync with sanitized path

	// ── Stage 1: synthesize (TTS) ──────────────────────────────────────
	emitSynthesizeCompleted := stageLog(s.log, requestID, "synthesize", language)
	item = s.synthesizeStage(ctx, item, req, outputDir, filename, language)
	emitSynthesizeCompleted()
	if strings.TrimSpace(item.Status) == "failed" {
		return item
	}

	// Build meta map (kept inline — the bridge between synthesize and
	// destination stages; not its own stage file).
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
	emitDestinationCompleted := stageLog(s.log, requestID, "destination", language)
	item = s.destinationStage(ctx, item, req, dest, metaJSON)
	emitDestinationCompleted()
	if strings.TrimSpace(item.Status) == "failed" {
		return item
	}

	// ── Stage 3: finalize (dedupe gate + atomic swap + cleanup) ───────
	emitFinalizeCompleted := stageLog(s.log, requestID, "finalize", language)
	item = s.finalizeStage(ctx, item, requestID, textHash, language, req, dest, metaJSON,
		shouldSwap, oldDriveFileID, oldLocalPath, oldCleanedPath)
	emitFinalizeCompleted()

	return item
}
