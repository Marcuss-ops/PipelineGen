// Package voiceover — BACKFILL typed-port use case (Wave 21
// PR-VOICEOVER-TYPED-PORT-RECOVERY-PHASE2, B-2 step closure).
//
// AGENTS.md Pattern 0 (port abstraction layer, June 2026):
// use cases own their dependency wiring via ServiceDeps{...}; concrete
// adapters are injected in the composition root (internal/app/
// build_bundles_voiceover.go) and never directly referenced from the
// service layer.
//
// B-2 BACKFILL scope:
//   - GenerateVoiceoverUseCase is a 1-a-1 delegate to VoiceoverGenerator.
//   - No business logic augmentation at B-2 — pure typed-port
//     introduction.
//   - Wired in build_bundles_voiceover.go but NOT YET CONSUMED by
//     call sites in scripts/ or handlers. CUTOVER (B-3) flips the call
//     sites; CONTRACT (B-4) removes back-compat aliases.
//
// Future B-3+ BACKFILL stages can layer:
//   - idempotency cache lookup (per Wave 21 PR-G.2 BACKFILL readiness)
//   - post-write save context detachment (per AGENTS.md post-write
//     save exemption table)
//   - audit-log emit (per Wave 22 context.WithoutCancel gate review)
package voiceover

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// ServiceDeps wires dependencies for GenerateVoiceoverUseCase per
// AGENTS.md Pattern 0. Generator is the BACKFILL port (production
// supplies *Service via compile-time assertion in ports.go; tests
// inject stubs).
type ServiceDeps struct {
	// Generator is the canonical voiceover generation port (B-2 BACKFILL).
	Generator VoiceoverGenerator

	// Logger is optional (nil-safe via zap.NewNop() in the constructor).
	Logger *zap.Logger
}

// GenerateVoiceoverUseCase is the BACKFILL typed-port-adapter for
// voiceover generation. It is a 1-a-1 delegate to VoiceoverGenerator —
// no business orchestration augmentation at B-2 scope; future BACKFILL
// stages layer decorators via a UseCase decorator chain without touching
// the Service core or the wire-up.
type GenerateVoiceoverUseCase struct {
	deps ServiceDeps
}

// NewGenerateVoiceoverUseCase constructs the use case with mandatory
// deps. Panics on nil Generator — fail-fast per AGENTS.md WireUp pattern.
// Logger is optional (nil-safe via zap.NewNop()).
func NewGenerateVoiceoverUseCase(deps ServiceDeps) *GenerateVoiceoverUseCase {
	if deps.Generator == nil {
		panic("voiceover.NewGenerateVoiceoverUseCase: Generator is required (ServiceDeps.Generator)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &GenerateVoiceoverUseCase{deps: deps}
}

// Execute delegates 1-a-1 to the underlying VoiceoverGenerator.
// B-2 BACKFILL scope: NO orchestration additions — pure typed-port.
//
// Signature mirrors main's *Service.Generate shape (positional
// ctx + text + language + filename).
func (u *GenerateVoiceoverUseCase) Execute(ctx context.Context, text, language, filename string) (*VoiceoverResult, error) {
	return u.deps.Generator.Generate(ctx, text, language, filename)
}

// ────────────────────────────────────────────────────────────────────────
// PR-VOICEOVER-COMMAND-EXTRACT (Blocco 2, June 2026): canonical Command-
// driven use case. Depends ONLY on ports (Pattern 0, AGENTS.md).
// ────────────────────────────────────────────────────────────────────────

// UseCaseDeps wires dependencies for the canonical GenerateVoiceoversUseCase
// (Blocco 2). All 7 ports + DB are mandatory; pass non-nil concretes at
// the composition root. Logger is the only optional dep.
//
// AudioPostProcessor is nil-safe — the use case guards at the call site
// (only invoked when cmd.RemoveSilence == true). Composition roots can
// supply a no-op processor if audio cleanup is not desired.
type UseCaseDeps struct {
	TTSProvider         TTSProvider
	DestinationResolver DestinationResolver
	AudioPostProcessor  AudioPostProcessor
	AssetLifecycle      AssetLifecycle
	VoiceoverRepository VoiceoverRepository
	TransactionalOutbox TransactionalOutbox
	DB                  *sql.DB
	Logger              *zap.Logger

	// DefaultParallelism is the fallback when cmd.Parallelism == 0.
	// Clamped to >= 1. Production: 3.
	DefaultParallelism int
	// MaxParallelism is the upper bound on cmd.Parallelism. Clamped
	// to <= 8. Production: VOICEOVER_MAX_PARALLELISM env (default 4).
	MaxParallelism int
}

// GenerateVoiceoversUseCase is the canonical singular Command-driven
// use case. Block 2 ships with sequential per-language fan-out via
// the 7 ports; Block 3 wraps the per-language loop in a bounded pool
// so concurrent languages stay under MaxParallelism.
type GenerateVoiceoversUseCase struct {
	deps UseCaseDeps
}

// NewGenerateVoiceoversUseCase constructs the canonical use case.
// Mandatory deps are fail-fast (panic on nil) per AGENTS.md WireUp
// pattern; optional deps are nil-safe.
func NewGenerateVoiceoversUseCase(deps UseCaseDeps) *GenerateVoiceoversUseCase {
	if deps.TTSProvider == nil {
		panic("GenerateVoiceoversUseCase: TTSProvider is required (UseCaseDeps.TTSProvider)")
	}
	if deps.DestinationResolver == nil {
		panic("GenerateVoiceoversUseCase: DestinationResolver is required (UseCaseDeps.DestinationResolver)")
	}
	if deps.AssetLifecycle == nil {
		panic("GenerateVoiceoversUseCase: AssetLifecycle is required (UseCaseDeps.AssetLifecycle)")
	}
	if deps.VoiceoverRepository == nil {
		panic("GenerateVoiceoversUseCase: VoiceoverRepository is required (UseCaseDeps.VoiceoverRepository)")
	}
	if deps.TransactionalOutbox == nil {
		panic("GenerateVoiceoversUseCase: TransactionalOutbox is required (UseCaseDeps.TransactionalOutbox)")
	}
	if deps.DB == nil {
		panic("GenerateVoiceoversUseCase: DB is required (UseCaseDeps.DB) — the use case threads the atomic swap tx")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	if deps.DefaultParallelism <= 0 {
		deps.DefaultParallelism = 3
	}
	if deps.MaxParallelism <= 0 || deps.MaxParallelism > 8 {
		deps.MaxParallelism = 8
	}
	return &GenerateVoiceoversUseCase{deps: deps}
}

// Execute runs the canonical pipeline once per request.
//
// Block 2 shape (sequential): Step 1 validate, Step 2 resolve
// destination once, Step 3 fan out per language (TTS → optional
// post-process → Drive upload → atomic swap + outbox in single tx).
//
// Partial failure: returns (*Result, nil) with per-item Status ==
// StatusFailed so the caller can decide whether to surface 200 with
// `ok:false` body or 500. Cross-cutting failures (validation, no
// languages, destination resolve) return (*Result, error).
//
// Caller contract: command emitted to a worker via the voiceover
// job broker should set JobID after Execute returns; the dispatcher
// uses result.RequestID to thread audit back to the originating job.
func (u *GenerateVoiceoversUseCase) Execute(ctx context.Context, cmd *GenerateVoiceoversCommand) (*GenerateVoiceoversResult, error) {
	// Step 1: validate the Command envelope at the use case boundary.
	// Mirrors the path-traversal-rejection-before-field-access pattern
	// pinned by TestGenerateBatch_RejectsPathTraversalPayload.
	if err := cmd.Validate(); err != nil {
		return nil, fmt.Errorf("GenerateVoiceoversUseCase.Execute: validate: %w", err)
	}

	// Step 1b: normalize strategy at the boundary (mirrors
	// BatchRequest.normalizeBatchRequest at types.go:289). Unknown
	// inputs collapse to asset.StrategyVerify. Without this,
	// invalid strings like "" or "fast" pass through unchanged and
	// break downstream `req.Strategy == "replace"` comparisons in
	// process.go / stages.go.
	cmd.Strategy = asset.NormalizeStrategy(string(cmd.Strategy), false)

	// Step 1c: compute the per-batch request ID once (mirrors
	// buildRequestID at types.go:301). Threaded through the
	// per-language orchestrator so every row in this batch shares the
	// same request_id column value for cross-language audit.
	requestID := buildRequestID()

	result := &GenerateVoiceoversResult{
		OK:           true,
		RequestID:    requestID,
		TotalOutputs: len(cmd.Languages),
		PerLanguage:  make([]VoiceoverItemResult, 0, len(cmd.Languages)),
		StartedAt:    time.Now().UTC(),
	}

	// Step 2: resolve destination once. Cross-cutting failure path —
	// bubble up so the caller short-circuits (no per-item fan-out).
	var dest *ResolvedDestination
	if cmd.Destination != nil {
		d, err := u.deps.DestinationResolver.Resolve(ctx, cmd.Destination)
		if err != nil {
			result.OK = false
			result.Error = fmt.Sprintf("destination resolve: %v", err)
			result.CompletedAt = time.Now().UTC()
			return result, fmt.Errorf("GenerateVoiceoversUseCase.Execute: resolve destination: %w", err)
		}
		dest = d
	}

	// Step 2b: compute textHash once for the batch (same value goes
	// into every voiceover row's text_hash column + every filename
	// substitution `{hash}` token in the per-language filename).
	textHash := hashutil.SHA256String(cmd.Text)

	// Step 3: sequential fan-out per language (Block 3 wraps in pool).
	for _, lang := range cmd.Languages {
		item := u.processOneLanguage(ctx, cmd, requestID, lang, textHash, dest)
		switch item.Status {
		case StatusCompleted:
			result.SuccessCount++
		default: // StatusFailed or any unexpected value
			result.OK = false
			result.FailedCount++
		}
		result.PerLanguage = append(result.PerLanguage, item)
	}

	result.CompletedAt = time.Now().UTC()
	return result, nil
}

// processOneLanguage is the per-language orchestrator. Block 2 uses
// the sequential fan-out; Block 3 introduces the bounded pool around
// slice of these calls. Per-language ordering of PerLanguage[] matches
// the input Languages[] order so callers can correlate Language ↔
// index without re-processing.
func (u *GenerateVoiceoversUseCase) processOneLanguage(
	ctx context.Context,
	cmd *GenerateVoiceoversCommand,
	requestID string,
	language string,
	textHash string,
	dest *ResolvedDestination,
) VoiceoverItemResult {
	item := VoiceoverItemResult{
		Language: language,
		Status:   StatusFailed,
	}

	if dest == nil || dest.FolderID == "" {
		item.Error = "missing_folder_id: voiceover destination has no FolderID for upload"
		return item
	}

	id := buildVoiceoverID(textHash, language, dest.FolderID)
	filename := u.buildCommandFilename(cmd, language, textHash)
	item.Filename = filename
	item.ID = id

	// Step 1: TTSProvider.Synthesize
	voice := ""
	if cmd.VoiceOverrides != nil {
		voice = cmd.VoiceOverrides[language]
	}
	ttsOut, err := u.deps.TTSProvider.Synthesize(ctx, TTSInput{
		Text:          cmd.Text,
		Language:      language,
		Voice:         voice,
		Filename:      filename,
		OutputDir:     dest.FolderPath, // composition-root derived output dir
		RemoveSilence: cmd.RemoveSilence,
	})
	if err != nil {
		item.Error = fmt.Sprintf("tts_failed: %v", err)
		return item
	}
	item.LocalPath = ttsOut.LocalPath
	item.CleanedPath = ttsOut.CleanedPath
	item.Voice = ttsOut.Voice
	item.FileHash = ttsOut.FileHash

	// Step 2: optional AudioPostProcessor — nil-safe.
	if cmd.RemoveSilence && u.deps.AudioPostProcessor != nil && ttsOut.LocalPath != "" {
		postOut, err := u.deps.AudioPostProcessor.Process(ctx, AudioPostInput{
			LocalPath: ttsOut.LocalPath,
			OutputDir: dest.FolderPath,
			Filename:  filename,
		})
		if err != nil {
			item.Error = fmt.Sprintf("audio_post_process_failed: %v", err)
			return item
		}
		if postOut.CleanedPath != "" {
			item.CleanedPath = postOut.CleanedPath
		}
	}

	if item.LocalPath == "" && item.CleanedPath == "" {
		item.Error = "no_local_payload: TTSProvider + AudioPostProcessor produced no local path"
		return item
	}
	uploadPath := item.CleanedPath
	if uploadPath == "" {
		uploadPath = item.LocalPath
	}

	// Step 3: AssetLifecycle.Upload — populates Drive URLs.
	metaBuf := map[string]any{
		"text_hash":    textHash,
		"text_preview": textutil.Truncate(cmd.Text, 100),
		"language":     language,
		"voice":        item.Voice,
		"strategy":     string(cmd.Strategy),
		"cleaned_path": item.CleanedPath,
	}
	// Delegate the user-meta overlay to the canonical mergeUserMetadata
	// function so the process_metadata_test.go contract (collision-drop,
	// StyleGroup injection) is preserved by ONE implementation, not two.
	mergeUserMetadata(metaBuf, dest, cmd.Metadata, u.deps.Logger)
	metaJSON, _ := json.Marshal(metaBuf)

	uploadOut, err := u.deps.AssetLifecycle.Upload(ctx, AssetUploadInput{
		ID:         id,
		LocalPath:  uploadPath,
		Filename:   filename,
		FolderID:   dest.FolderID,
		FolderPath: dest.FolderPath,
		Metadata:   string(metaJSON),
		FileHash:   item.FileHash,
		Source:     "voiceover",
		Name:       textutil.Truncate(cmd.Text, 100),
	})
	if err != nil {
		item.Error = fmt.Sprintf("upload_failed: %v", err)
		return item
	}
	item.DriveLink = uploadOut.DriveLink
	item.DriveFileID = uploadOut.DriveFileID
	item.FileHash = uploadOut.FileHash

	// Step 4a: pre-read the OLD row to capture orphan paths for the
	// post-commit cleanup goroutine (lazy-deferred to Block 7). The
	// pre-read MUST run BEFORE the BeginTx so the orphan paths
	// captured here are committed-state, not stale-from-another-tx.
	// The result is intentionally not used here — the legacy
	// Service.GenerateBatch path's replace-mode cleanup lives in
	// Service.cleanupOrphanVoiceover (stages.go). The pre-read is
	// captured for the eventual Block 7 CONTRACT migration.
	_, _ = u.deps.VoiceoverRepository.PreReadByID(ctx, id)

	// Step 4b: atomic SQLite swap + outbox enqueue (single tx).
	// The PR-VO-A2 contract requires the OLD voiceover record is
	// never removed until the NEW one is durably persisted; we
	// thread DELETE+INSERT+outbox-ENQUEUE into one BeginTx/Commit
	// cycle so neither is observable alone.
	tx, err := u.deps.DB.BeginTx(ctx, nil)
	if err != nil {
		item.Error = fmt.Sprintf("tx_begin_failed: %v", err)
		return item
	}
	defer func() { _ = tx.Rollback() }() // safe after Commit

	if err := u.deps.VoiceoverRepository.DeleteByIDTx(ctx, tx, id); err != nil {
		item.Error = fmt.Sprintf("db_delete_failed: %v", err)
		return item
	}

	now := time.Now().UTC().Format(time.RFC3339)
	rec := &VoiceoverRecord{
		ID:           id,
		RequestID:    requestID,
		TextHash:     textHash,
		TextPreview:  textutil.Truncate(cmd.Text, 100),
		Language:     language,
		Voice:        item.Voice,
		Filename:     filename,
		LocalPath:    item.LocalPath,
		CleanedPath:  item.CleanedPath,
		FolderID:     dest.FolderID,
		FolderPath:   dest.FolderPath,
		DriveFileID:  item.DriveFileID,
		DriveLink:    item.DriveLink,
		DownloadLink: uploadOut.DownloadLink,
		FileHash:     item.FileHash,
		Status:       "generated",
		Strategy:     string(cmd.Strategy),
		Metadata:     string(metaJSON),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := u.deps.VoiceoverRepository.InsertTx(ctx, tx, rec); err != nil {
		item.Error = fmt.Sprintf("db_insert_failed: %v", err)
		return item
	}

	// PR-VO-A3 outbox enqueue inside the same tx.
	if item.FileHash != "" {
		if err := u.deps.TransactionalOutbox.EnqueueIndexEvent(ctx, tx, id, item.FileHash); err != nil {
			item.Error = fmt.Sprintf("outbox_enqueue_failed: %v", err)
			return item
		}
	}

	if err := tx.Commit(); err != nil {
		item.Error = fmt.Sprintf("tx_commit_failed: %v", err)
		return item
	}

	item.Status = StatusCompleted
	return item
}

// buildCommandFilename is the use case's filename builder. Block 2 inlines
// the template-substitution logic (the existing buildFilename helper is a
// method on Service, so the use case can't call it without a Service
// instance). The grammar mirrors buildFilename: {slug}, {lang}, {hash},
// {time}. Default template: "{slug}_{lang}.mp3".
func (u *GenerateVoiceoversUseCase) buildCommandFilename(cmd *GenerateVoiceoversCommand, language, textHash string) string {
	slug := textutil.SlugifyWithMax(cmd.Text, 30)
	template := cmd.FilenameTemplate
	if template == "" {
		template = "{slug}_{lang}.mp3"
	}
	filename := strings.ReplaceAll(template, "{slug}", slug)
	filename = strings.ReplaceAll(filename, "{lang}", language)
	hashPrefix := textHash
	if len(hashPrefix) > 8 {
		hashPrefix = hashPrefix[:8]
	}
	filename = strings.ReplaceAll(filename, "{hash}", hashPrefix)
	filename = strings.ReplaceAll(filename, "{time}", time.Now().Format("150405"))
	return filename
}
