package voiceover

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	pathutil "github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
	ptrutil "github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

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

func (s *Service) GenerateBatch(ctx context.Context, req *BatchRequest) (*BatchResponse, error) {
	req = normalizeBatchRequest(req)

	if strings.TrimSpace(req.Text) == "" {
		return nil, fmt.Errorf("text is required")
	}

	// PR-VO-A4 (path-traversal fix, June 2026): validate the destination
	// at the request boundary so a path-traversal payload is rejected
	// before any per-language work runs. DestinationRequest.Validate is
	// nil-safe (an empty SubfolderName == "no subfolder" legitimate
	// signal, not an error). One bad batch never reaches the MkdirAll
	// site — fail-fast for the caller rather than corrupting the
	// working tree with a sub-optimal fallback.
	if err := req.Destination.Validate(); err != nil {
		s.log.Warn("PR-VO-A4: rejecting batch with path-traversal payload in destination",
			zap.String("subfolder_name", func() string {
				if req.Destination != nil {
					return req.Destination.SubfolderName
				}
				return ""
			}()),
			zap.Error(err))
		return nil, fmt.Errorf("invalid destination: %w", err)
	}

	requestID := buildRequestID()
	textHash := hashutil.SHA256String(req.Text)

	destinationReq := req.Destination
	if destinationReq == nil && s.cfg.Drive.VoiceoverFolder() != "" {
		destinationReq = &DestinationRequest{
			FolderID: s.cfg.Drive.VoiceoverFolder(),
		}
	}

	var dest *ResolvedDestination
	if destinationReq != nil {
		var err error
		dest, err = s.resolveDestination(ctx, destinationReq)
		if err != nil {
			return nil, err
		}
	}

	// Ensure dest is not nil to avoid panics when accessing fields
	if dest == nil {
		dest = &ResolvedDestination{}
	}

	if dest.FolderID == "" && s.cfg.Drive.VoiceoverFolder() != "" {
		dest.FolderID = s.cfg.Drive.VoiceoverFolder()
	}

	resp := &BatchResponse{
		OK:        true,
		RequestID: requestID,
	}

	for _, lang := range req.Languages {
		item := s.processLanguage(ctx, requestID, textHash, lang, req, dest)
		if item.Status == "failed" {
			resp.OK = false
		}
		resp.Items = append(resp.Items, item)
	}

	return resp, nil
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
	// never removed until the new one is durably persisted:
	//
	//   pre-read oldDriveFileID (above)
	//   ↓
	//   generate staging (TTS + FFmpeg silence removal)
	//   ↓
	//   upload to Drive via Lifecycle.ProcessAsset (lifecycle commits to media_assets)
	//   ↓
	//   tx { INSERT new voiceovers row; DELETE old voiceovers row }   (atomic swap)
	//   ↓
	//   post-commit: best-effort Drive.DeleteFile(oldDriveFileID) in a goroutine
	//
	// If any step before the transaction fails, the existing voiceovers row is preserved
	// (no data loss). If the transaction itself fails, the INSERT and DELETE either both
	// roll back or both commit; we never end up with the new audio on Drive but no DB row.
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
		// PR-VO-A4 (path-traversal fix, June 2026): even though
		// DestinationRequest.Validate is called in GenerateBatch's
		// fail-fast gate above, this is the leaf consumer that actually
		// calls os.MkdirAll. processLanguage can be reached from callers
		// that bypass GenerateBatch (e.g. the jobs/scripts subsystem
		// building BatchItem directly), and a future direct-construction
		// test could skip the boundary. Defense in depth: re-sanitize
		// the segment here, then pin the post-join result with
		// filepath.Rel. If either check trips, fail the per-language
		// item loudly rather than letting an unsafe MkdirAll corrupt
		// the working tree.
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
			// This should be unreachable given the sanitizer above
			// (single safe segment + Join with a clean root). Logged at
			// error level because reaching it indicates a future drift
			// between the two helpers OR a corrupted root config.
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

	// Build audio input for processor
	audioInput := &audioasset.AudioInput{
		Text:          req.Text,
		Language:      language,
		OutputDir:     outputDir,
		Filename:      filename,
		RemoveSilence: ptrutil.BoolDefault(req.RemoveSilence, false),
	}
	if dest != nil && dest.FolderID != "" {
		audioInput.Destination = &asset.ResolveRequest{
			Source:     "voiceover",
			FolderID:   dest.FolderID,
			FolderPath: dest.FolderPath,
			Group:      dest.Group,
		}
	}

	// Generate audio via audioasset processor
	result, err := s.audioProcessor.Generate(ctx, audioInput)
	if err != nil {
		return item.fail("generate_failed", err)
	}

	item.LocalPath = result.LocalPath
	item.CleanedPath = result.CleanedPath
	item.FileHash = result.FileHash
	item.DriveLink = result.DriveLink
	item.DriveFileID = result.DriveFileID
	// PR-VO-A1 (Lost Voice): single canonical source from
	// scripts/bridges/tts_edge.py stdout (captured as result.Voice in
	// internal/infrastructure/audio/processor.go).
	item.Voice = result.Voice
	if item.Voice == "" {
		// Bridge returned no voice — log so the degraded mode is
		// observable in production logs; fall back to language so the
		// persisted row is still inspectable.
		s.log.Warn("PR-VO-A1 fallback: bridge returned empty Voice, using language code",
			zap.String("language", language))
		item.Voice = language
	}
	item.Status = result.Status

	if result.Status == "" {
		item.Status = "processed"
	}

	// Process through LifecycleService (dedupe + upload + persist)
	meta := map[string]any{
		"text_hash":    textHash,
		"text_preview": textutil.Truncate(req.Text, 100),
		"language":     item.Language,
		"voice":        item.Voice,
		"strategy":     req.Strategy,
		"request_id":   requestID,
		"cleaned_path": item.CleanedPath,
	}

	// Call semantic tagger for rich metadata (search_text, tags)
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
	metaJSON, _ := json.Marshal(meta)

	// PR-VO-A3 (Outbox-based Qdrant indexing, June 2026): the previous
	// "fire-and-forget goroutine → clipIndexer.IndexClip" was the data-loss
	// bug — it ran BEFORE lifecycle.ProcessAsset committed, could not retry
	// durably, and could leave SQLite/Qdrant divergent. The enqueue now
	// happens INSIDE swapVoiceoverRow's SQLite transaction (via the
	// outboxEnqueuer port) so the metadata UPSERT (voiceovers row + the
	// outbox_events row) commit atomically. Nothing to do here at this
	// stage — the IndexClip call will run from the outbox worker after
	// commit. The legacy concurrent.SafeGoFunc("voiceover-indexing", ...),
	// Go's "Voice over-indexing background" goroutine, is intentionally
	// deleted.

	localPath := item.CleanedPath
	if localPath == "" {
		localPath = item.LocalPath
	}

	// Create FinalizeInput for LifecycleService
	input := &lifecycle.FinalizeInput{
		ID:           item.ID,
		Name:         textutil.Truncate(req.Text, 100),
		Filename:     item.Filename,
		Kind:         lifecycle.AssetKindAudio,
		Source:       "voiceover",
		Group:        dest.Group,
		Subfolder:    "",
		LocalPath:    localPath,
		FolderID:     dest.FolderID,
		FolderPath:   dest.FolderPath,
		DriveLink:    item.DriveLink,
		DriveFileID:  item.DriveFileID,
		DownloadLink: item.DownloadLink,
		FileHash:     item.FileHash,
		Metadata:     string(metaJSON),
		// fix/voiceover-require-drive-on-intent: Drive is required when the
		// caller expressed intent to write (explicit dest.FolderID or
		// config-level voiceover folder) — independent of whether a
		// previous upload populated item.DriveLink. The previous formula
		// OR'd `item.DriveLink != ""`, which silently demoted Drive from
		// required to optional on a failed re-upload and let the
		// lifecycle finalizer complete locally without surfacing the
		// failure. Intent is set at the request boundary, not derived
		// from the upload result.
		RequireLocal: false,
		RequireHash:  false,
		RequireDrive: dest.FolderID != "" || s.cfg.Drive.VoiceoverFolder() != "",
		VerifyDB:     true,
	}

	// Process through lifecycle (dedupe + upload + persist)
	lifecycleResult, err := s.lifecycleService.ProcessAsset(ctx, input, item.FileHash)
	if err != nil {
		return item.fail("lifecycle_failed", err)
	}
	if !lifecycleResult.OK {
		return item.fail("lifecycle_failed", fmt.Errorf("%s", lifecycleResult.Error))
	}

	// Update item with results
	item.DriveLink = lifecycleResult.DriveLink
	item.DriveFileID = lifecycleResult.DriveFileID
	item.DownloadLink = lifecycleResult.DownloadLink
	item.Status = "processed"

	// PR-VO-A2: atomic voiceovers row swap. The DELETE-old + INSERT-new happen in
	// the same SQLite transaction so a partial failure cannot leave the system with
	// a deleted record and no replacement (the original bug). Lifecycle has already
	// committed the new audio to media_assets + uploaded to Drive, so by the time we
	// enter the transaction the new state exists on Drive — the DB row is the only
	// piece left to durably fix up.
	now := time.Now().UTC()
	if swapErr := s.swapVoiceoverRow(ctx, VoiceoverSwapRow{
		ID:              id,
		RequestID:       requestID,
		TextHash:        textHash,
		TextPreview:     textutil.Truncate(req.Text, 100),
		Language:        language,
		Voice:           item.Voice,
		Filename:        item.Filename,
		LocalPath:       localPath,
		FolderID:        dest.FolderID,
		FolderPath:      dest.FolderPath,
		DriveFileID:     lifecycleResult.DriveFileID,
		DriveLink:       lifecycleResult.DriveLink,
		DownloadLink:    lifecycleResult.DownloadLink,
		FileHash:        item.FileHash,
		Status:          "processed",
		Strategy:        req.Strategy,
		Metadata:        string(metaJSON),
		ShouldSwap:      shouldSwap,
		Now:             now,
	}); swapErr != nil {
		// The new audio is already on Drive and in media_assets; surface the swap
		// failure loudly so operators see the partial state instead of a silent
		// orphan row. The caller decides whether the item should be marked failed.
		s.log.Error("voiceover row swap failed", zap.String("id", id), zap.Error(swapErr))
		return item.fail("db_swap_failed", swapErr)
	}

	// PR-VO-A2: best-effort post-commit cleanup of the now-orphaned Drive file
	// AND the local audio files. Fire-and-forget so a slow Drive delete (or a
	// large local-file removal) does not block the caller; errors surface in
	// the dedicated goroutine's log line and do not fail the request. The
	// pre-read at the top of processLanguage captures both oldDriveFileID and
	// oldLocalPath/oldCleanedPath so the goroutine has everything it needs.
	uploader := s.driveUploader
	if shouldSwap && oldDriveFileID != "" && oldDriveFileID != lifecycleResult.DriveFileID {
		voiceoverID := id
		oldDFID := oldDriveFileID
		oldLocal := oldLocalPath
		oldCleaned := oldCleanedPath
		logger := s.log
		concurrent.SafeGoFunc("vo-swap-cleanup", voiceoverID, func(voiceoverKey string) {
			cleanupCtx := context.WithoutCancel(ctx)
			// 1. Best-effort Drive cleanup of the orphaned file.
			if uploader != nil {
				if err := uploader.DeleteFile(cleanupCtx, oldDFID); err != nil {
					logger.Warn("voiceover Drive cleanup of old file failed (best-effort)",
						zap.String("voiceover_id", voiceoverKey),
						zap.String("old_drive_file_id", oldDFID),
						zap.Error(err))
				}
			}
			// 2. Best-effort local cleanup of the orphaned audio file(s).
			// fs.ErrNotExist is benign (file already gone); everything else
			// is logged so operators can spot a leaking filesystem.
			for _, p := range []string{oldLocal, oldCleaned} {
				if p == "" {
					continue
				}
				if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
					logger.Warn("voiceover local cleanup of old file failed (best-effort)",
						zap.String("voiceover_id", voiceoverKey),
						zap.String("path", p),
						zap.Error(err))
				}
			}
		})
	}

	return item
}

// VoiceoverSwapRow bundles the inputs to swapVoiceoverRow so the row-level
// INSERT/DELETE inside the SQLite transaction stays a flat struct literal.
type VoiceoverSwapRow struct {
	ID           string
	RequestID    string
	TextHash     string
	TextPreview  string
	Language     string
	Voice        string
	Filename     string
	LocalPath    string
	FolderID     string
	FolderPath   string
	DriveFileID  string
	DriveLink    string
	DownloadLink string
	FileHash     string
	Status       string
	Strategy     string
	Metadata     string
	ShouldSwap   bool
	Now          time.Time
}

// swapVoiceoverRow performs the atomic INSERT-new + (optional) DELETE-old in a
// single SQLite transaction. The function is the canonical PR-VO-A2 swap site;
// callers should NOT do inline BEGIN/COMMIT around voiceovers writes because
// any drift from this signature would re-create the original data-loss window.
//
// When ShouldSwap=false the operation degenerates to a plain INSERT (first-time
// generation); the condition is set in processLanguage based on the request
// Strategy field per the canonical asset.PipelineStrategy taxonomy.
func (s *Service) swapVoiceoverRow(ctx context.Context, row VoiceoverSwapRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if row.ShouldSwap {
		// DELETE only when swap is explicitly requested; the WHERE on `id` is the
		// protective fence so a future schema drift (e.g. soft-delete column)
		// cannot accidentally widen the delete scope.
		if _, err := tx.ExecContext(ctx, `DELETE FROM voiceovers WHERE id = ?`, row.ID); err != nil {
			return fmt.Errorf("delete old voiceovers row: %w", err)
		}
	}

	// INSERT new row. ON CONFLICT(id) DO UPDATE keeps the swap idempotent on a
	// double-replay (e.g. lifecycle commit succeeded, partial crash, retry comes
	// back through process.go with the same id).
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO voiceovers (
			id, request_id, text_hash, text_preview, language, voice, filename,
			local_path, cleaned_path, folder_id, folder_path, drive_file_id,
			drive_link, download_link, file_hash, duration_seconds, status,
			error, strategy, metadata, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, '', ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			request_id = excluded.request_id,
			text_hash = excluded.text_hash,
			text_preview = excluded.text_preview,
			language = excluded.language,
			voice = excluded.voice,
			filename = excluded.filename,
			local_path = excluded.local_path,
			cleaned_path = excluded.cleaned_path,
			folder_id = excluded.folder_id,
			folder_path = excluded.folder_path,
			drive_file_id = excluded.drive_file_id,
			drive_link = excluded.drive_link,
			download_link = excluded.download_link,
			file_hash = excluded.file_hash,
			status = excluded.status,
			strategy = excluded.strategy,
			metadata = excluded.metadata,
			updated_at = excluded.updated_at
	`,
		row.ID, row.RequestID, row.TextHash, row.TextPreview, row.Language,
		row.Voice, row.Filename, row.LocalPath, "", row.FolderID, row.FolderPath,
		row.DriveFileID, row.DriveLink, row.DownloadLink, row.FileHash,
		row.Status, row.Strategy, row.Metadata,
		row.Now.Format(time.RFC3339Nano), row.Now.Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("insert new voiceovers row: %w", err)
	}

	// PR-VO-A3 (Outbox-based Qdrant indexing, June 2026): enqueue the
	// canonical `asset.index.requested` event INSIDE this SAME tx so the
	// voiceovers INSERT and the outbox_events INSERT commit atomically.
	// Skipped when the outboxEnqueuer port is nil (production wiring ALWAYS
	// supplies one, but tests may construct Service{...} with the field
	// zero-valued — same nil-guard pattern as the previous ClipIndexFunc).
	// Skipped when FileHash is empty because the canonical envelope's
	// supersede gate requires a content fingerprint (the worker rejects
	// empty `source_version` as terminal and the event would be
	// dead-lettered on first claim — see application/jobs/outbox/indexing.go
	// IndexingHandler).
	if s.outboxEnqueuer != nil && row.FileHash != "" {
		if err := s.outboxEnqueuer.EnqueueIndexEvent(ctx, tx, row.ID, row.FileHash); err != nil {
			return fmt.Errorf("enqueue outbox index event in tx: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}

func (s *Service) resolveDestination(ctx context.Context, dest *DestinationRequest) (*ResolvedDestination, error) {
	if dest == nil {
		return &ResolvedDestination{}, nil
	}

	// PR-VO-A4 (path-traversal fix, June 2026): defense in depth.
	// resolveDestination is reached from processLanguage AND from any
	// future direct caller (handler-side retry paths, jobs subsystem
	// rebuilds, tests that construct *DestinationRequest directly). The
	// segment is forwarded into asset.ResolveRequest — which feeds into
	// the Drive hierarchy. Validate here so a path-traversal payload
	// cannot escape even if the upstream caller forgot to call
	// DestinationRequest.Validate. Pairs with the same check at the
	// MkdirAll site and at the request boundary — three layers, one
	// helper.
	if err := dest.Validate(); err != nil {
		return nil, err
	}

	resolved, err := s.assetDestResolver.Resolve(ctx, &asset.ResolveRequest{
		Source:          "voiceover",
		Group:           dest.Group,
		FolderID:        dest.FolderID,
		FolderPath:      dest.FolderPath,
		SubfolderName:   dest.SubfolderName,
		CreateSubfolder: dest.CreateSubfolder,
	})
	if err != nil {
		return nil, err
	}

	return &ResolvedDestination{
		FolderID:   resolved.FolderID,
		FolderPath: resolved.FolderPath,
		DriveLink:  resolved.DriveLink,
	}, nil
}

// GeneratePromo translates text to multiple languages via Ollama then generates
// a voiceover for each. This replaces scripts/generate_promo_voiceovers.py.
