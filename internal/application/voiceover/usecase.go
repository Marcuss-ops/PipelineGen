// Package voiceover — canonical voiceover generation use cases.
//
// AGENTS.md Pattern 0 (port abstraction layer, June 2026):
// use cases own their dependency wiring via UseCaseDeps{...}; concrete
// adapters are injected in the composition root (internal/app/
// build_bundles_voiceover.go) and never directly referenced from the
// service layer.
//
// A1 (June 2026): the legacy B-2 BACKFILL delegate
// (ServiceDeps + GenerateVoiceoverUseCase + NewGenerateVoiceoverUseCase
// + Execute) was a 1-a-1 pass-through to voiceover.VoiceoverGenerator
// without orchestration. It was wired in build_bundles_voiceover.go
// solely as scaffolding for an eventual CUTOVER (B-3) that never
// landed, and never had a production caller. Removed: callers depend
// directly on the typed port (books, scripts/jobs, workflow/promo).
package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// ────────────────────────────────────────────────────────────────────────
// PR-VOICEOVER-COMMAND-EXTRACT (Blocco 2, June 2026): canonical Command-
// driven use case. Depends ONLY on ports (Pattern 0, AGENTS.md).
// ────────────────────────────────────────────────────────────────────────

// UseCaseDeps wires dependencies for the canonical GenerateVoiceoversUseCase
// (Blocco 2). All 6 ports are mandatory; pass non-nil concretes at the
// composition root. Logger is the only optional dep.
//
// P1.6 (June 2026): removed DB *sql.DB — the use case now calls
// VoiceoverRepository.BeginTx() to open the atomic swap transaction
// instead of reaching through a raw *sql.DB handle. The Repository
// port already declares BeginTx per persistence/repository.go.
//
// AudioPostProcessor is nil-safe — the use case guards at the call site
// (only invoked when cmd.RemoveSilence == true). Composition roots can
// supply a no-op processor if audio cleanup is not desired.
//
// DefaultFolderResolver (PR 6 P0.2, June 2026) is OPTIONAL by design:
// nil-safe at the use case boundary. When cmd.Destination is nil AND the
// resolver returns a configured folder, Execute synthesises a
// ResolvedDestination and proceeds (mirrors the legacy
// *Service.processLanguage fallback at process.go:75-79). When
// DefaultFolderResolver is nil OR returns ok=false, Execute degrades to
// the canonical missing_folder_id short-circuit at processOneLanguage
// (line 283) — same behavior as the pre-P0.2 implementation. This keeps
// the "no fake availability" rule (godlike/07) intact for deployments
// without a configured voiceover_root_folder: those requests still fail
// loudly with missing_folder_id rather than silently writing to /tmp.
type UseCaseDeps struct {
	TTSProvider           TTSProvider
	DestinationResolver   DestinationResolver
	AudioPostProcessor    AudioPostProcessor
	AssetLifecycle        AssetLifecycle
	VoiceoverRepository   VoiceoverRepository
	TransactionalOutbox   TransactionalOutbox
	Logger                *zap.Logger
	DefaultFolderResolver VoiceoverDefaultFolderResolver

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
	deps     UseCaseDeps
	executor *Executor
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
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	if deps.DefaultParallelism <= 0 {
		deps.DefaultParallelism = 3
	}
	if deps.MaxParallelism <= 0 || deps.MaxParallelism > 8 {
		deps.MaxParallelism = 8
	}
	return &GenerateVoiceoversUseCase{
		deps:     deps,
		executor: NewExecutor(deps.Logger),
	}
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

	// Step 1c: per-batch requestID is computed once by Plan() so the
	// value is shared by every Task.RequestID AND by the top-level
	// result.RequestID below. Single source of truth — no two
	// independent buildRequestID() calls per batch (auditors correlate
	// request_id ↔ task IDs via the same value).
	//
	// Step 5 (P0.3 items-model recovery, June 2026): TotalOutputs and
	// PerLanguage capacity are now sourced from len(cmd.Items) (one
	// output per VoiceoverItem, NOT one output per language code).
	result := &GenerateVoiceoversResult{
		OK:           true,
		RequestID:    "",
		TotalOutputs: len(cmd.Items),
		PerLanguage:  make([]VoiceoverItemResult, 0, len(cmd.Items)),
		StartedAt:    time.Now().UTC(),
	}

	// Step 2: resolve destination once. Cross-cutting failure path —
	// bubble up so the caller short-circuits (no per-item fan-out).
	//
	// PR 6 P0.2 (June 2026) destination-fallback chain:
	//   1. cmd.Destination supplied → resolve via DestinationResolver
	//      (the canonical explicit/Kind-based routing per PR-VO-C1).
	//   2. cmd.Destination nil AND DefaultFolderResolver wired AND
	//      returns ("<folderID>", true) → synthesise ResolvedDestination
	//      with the configured folder (mirrors legacy
	//      *Service.processLanguage fallback at process.go:75-79) and
	//      log "destination fallback to voiceover default folder" so
	//      operators see the fallback in audit logs.
	//   3. cmd.Destination nil AND DefaultFolderResolver nil OR
	//      returns ("", false) → leave dest = nil; processOneLanguage
	//      short-circuits with `missing_folder_id` (pre-P0.2 behavior
	//      preserved per godlike/07 — no fake availability for
	//      deployments without voiceover_root_folder configured).
	var dest *ResolvedDestination
	switch {
	case cmd.Destination != nil:
		d, err := u.deps.DestinationResolver.Resolve(ctx, cmd.Destination)
		if err != nil {
			result.OK = false
			result.Error = fmt.Sprintf("destination resolve: %v", err)
			result.CompletedAt = time.Now().UTC()
			return result, fmt.Errorf("GenerateVoiceoversUseCase.Execute: resolve destination: %w", err)
		}
		dest = d
	case u.deps.DefaultFolderResolver != nil:
		// Top-down switch semantics: at this point cmd.Destination is
		// verifiably nil (case-1 was false). The redundant
		// `cmd.Destination == nil` half is dropped per idiomatic
		// Go-switch practice.
		driveFolderID, localOutputDir, ok := u.deps.DefaultFolderResolver.Resolve(ctx)
		if ok && driveFolderID != "" {
			dest = &ResolvedDestination{
				FolderID:   driveFolderID,
				FolderPath: localOutputDir,
			}
			if u.deps.Logger != nil {
				u.deps.Logger.Info("destination fallback to voiceover default folder",
					zap.String("folder_id", driveFolderID),
					zap.String("output_dir", localOutputDir))
			}
		}
	}

	// Step 2b: textHash is computed lazily by Plan() (one SHA256 per
	// batch, threaded into every Task.TextHash + every filename
	// substitution `{hash}` token). The Result ID lineage is owned by
	// the executor's per-task fn closure; this Execute layer stays
	// pure orchestrator (Pattern 0).

	// Step 3: bounded parallel fan-out per language (Block 3).
	// Plan materialises []Task (one per language) with all the
	// per-task side-data pre-computed (filename, ID, voice override,
	// requestID, textHash). EffectiveParallelism clamps the requested
	// cap against deps.MaxParallelism and len(tasks) so we never
	// spawn more workers than languages. The TaskFn closure binds
	// the executor to processOneTask (Task → TaskResult) so the
	// per-language fan-out body stays a single implementation in
	// processOneLanguage (the executor only orchestrates, doesn't
	// own business logic — Pattern 0).
	// Step 3 (cont): Plan() returns the per-batch requestID and
	// textHash it threaded into every Task. Use the SAME requestID
	// for result.RequestID so audit correlates result ↔ tasks.
	tasks, requestID, _ := u.Plan(cmd, dest)
	result.RequestID = requestID
	requested := cmd.Parallelism
	if requested <= 0 {
		// cmd.Parallelism zero/unset → fall back to the constructor's
		// clamped DefaultParallelism (production: 3 per AGENTS.md
		// utilities table / voiceover Master Plan).
		requested = u.deps.DefaultParallelism
	}
	concurrency := EffectiveParallelism(requested, u.deps.MaxParallelism, len(tasks))
	taskFn := func(ctx context.Context, t Task) TaskResult {
		return u.processOneTask(ctx, t)
	}
	results, runErr := u.executor.Run(ctx, tasks, concurrency, taskFn, nil)
	if runErr != nil {
		// Composition root did not bind the per-language worker OR
		// the executor hit a cross-cutting setup error. Surface loudly
		// so the missing wire-up is fixed before deploy (godlike/07
		// — no fake availability).
		result.OK = false
		result.Error = fmt.Sprintf("executor.Run: %v", runErr)
		result.CompletedAt = time.Now().UTC()
		return result, fmt.Errorf("GenerateVoiceoversUseCase.Execute: %w", runErr)
	}
	result.PerLanguage = results
	for _, item := range results {
		switch item.Status {
		case StatusCompleted:
			result.SuccessCount++
		default: // StatusFailed or any unexpected value
			result.OK = false
			result.FailedCount++
		}
	}

	result.CompletedAt = time.Now().UTC()
	return result, nil
}

// processOneLanguage is the per-item orchestrator. Block 2 uses
// the sequential fan-out; Block 3 introduces the bounded pool around
// slice of these calls. Per-item ordering of PerLanguage[] matches
// the input Items[] order so callers can correlate item ↔ index
// without re-processing.
//
// Step 5 (P0.3 items-model recovery, June 2026): the function takes
// a VoiceoverItem directly (not (cmd, language)) so each invocation
// uses the item's own text/language/voice/filename. The linked
// *GenerateVoiceoversCommand still carries the batch-level
// configuration (Strategy, RemoveSilence, Metadata, Destination)
// — those fields are shared across the whole batch.
func (u *GenerateVoiceoversUseCase) processOneLanguage(
	ctx context.Context,
	cmd *GenerateVoiceoversCommand,
	itemSpec VoiceoverItem,
	requestID string,
	textHash string,
	dest *ResolvedDestination,
) VoiceoverItemResult {
	item := VoiceoverItemResult{
		Language: itemSpec.Language,
		Status:   StatusFailed,
	}

	if dest == nil || dest.FolderID == "" {
		item.Error = "missing_folder_id: voiceover destination has no FolderID for upload"
		return item
	}

	id := buildVoiceoverID(textHash, itemSpec.Language, dest.FolderID)
	// E4: buildCommandFilenameForItem → canonical BuildVoiceoverFilename.
	// Inputs are pre-validated by itemSpec via the higher-layer
	// GenerateVoiceoversCommand.Validate / GenerateVoiceoverItemCommand.Validate
	// gates, so the error path is unreachable in production; panic
	// surfaces regressions loud-fast in tests.
	filename, err := BuildVoiceoverFilename(FilenameSpec{
		Text:     itemSpec.Text,
		Language: itemSpec.Language,
		TextHash: textHash,
		Template: itemSpec.Filename,
	})
	if err != nil {
		panic(fmt.Sprintf("voiceover.BuildVoiceoverFilename (processOneLanguage): %v (item=%+v)", err, itemSpec))
	}
	item.Filename = filename
	item.ID = id

	// Step 1: TTSProvider.Synthesize — uses the item's text/voice.
	ttsOut, err := u.deps.TTSProvider.Synthesize(ctx, TTSInput{
		Text:          itemSpec.Text,
		Language:      itemSpec.Language,
		Voice:         itemSpec.Voice,
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
		"text_preview": textutil.Truncate(itemSpec.Text, 100),
		"language":     itemSpec.Language,
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
		Name:       textutil.Truncate(itemSpec.Text, 100),
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
	tx, err := u.deps.VoiceoverRepository.BeginTx(ctx)
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
		TextPreview:  textutil.Truncate(itemSpec.Text, 100),
		Language:     itemSpec.Language,
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
		// PR-VO-AUDIT-P01: cast typed Status to plain string for the
		// SQLite voiceovers.status column. The persistence layer keeps
		// its DB wire shape unchanged; the in-process state machine is
		// typed so the aggregate check is exhaustive at compile time.
		Status:       string(StatusGenerated),
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

// processOneTask is the Task-based adapter for the bounded executor
// (Block 3). It maps the immutable Task fields onto the existing
// processOneLanguage signature so the per-item fan-out body
// stays a single implementation — future BACKFILL stages (idempotency
// cache, post-write save context detachment) layer here without
// touching processOneLanguage.
//
// Step 5 (P0.3 items-model recovery, June 2026): the adapter sources
// the VoiceoverItem from t.Command.Items[t.Index] (per-item fan-out
// — each task carries its slice index back to the original command's
// Items array). The pre-Step-5 implementation extracted text/lang/
// voice/filename from the now-removed cmd.Languages/cmd.VoiceOverrides/
// cmd.FilenameTemplate flat shape; the new shape reads directly from
// the same underlying item the fanout produced.
//
// The adapter pattern keeps the executor's TaskFn pure (no *Service
// dependency the executor doesn't otherwise need) while preserving
// the canonical per-item stage sequencing pinned by
// service_test.go's path-traversal contract. Defensive bounds-check
// on t.Index surfaces a stale Task (out-of-range index after a
// concurrent re-plan) as StatusFailed rather than a runtime panic.
func (u *GenerateVoiceoversUseCase) processOneTask(ctx context.Context, t Task) VoiceoverItemResult {
	if t.Command == nil {
		// Defensive: Plan always populates Command, so a nil here means
		// a stale executor task. Surface the failure with the task's
		// recorded Language (Plan-derived) for log readability.
		return VoiceoverItemResult{
			Language: t.Language,
			Status:   StatusFailed,
			Error:    "task.Command is nil (plan produced an orphan task)",
		}
	}
	if t.Index < 0 || t.Index >= len(t.Command.Items) {
		// Defensive bounds-check: source the displayed Language from
		// Task.Language (Plan-derived from itemSpec.Language) so the
		// error path's item↔index mapping is consistent with the happy
		// path's display.
		return VoiceoverItemResult{
			Language: t.Language,
			Status:   StatusFailed,
			Error:    fmt.Sprintf("task item index %d out of bounds (len(Items)=%d)", t.Index, len(t.Command.Items)),
		}
	}
	// Step 5 invariant: pull text/lang/voice/filename from THIS item,
	// not from the now-removed cmd.Languages/cmd.VoiceOverrides/cmd.
	// FilenameTemplate flat fields. processOneLanguage takes the item
	// directly so the per-item payload is honoured end-to-end.
	item := t.Command.Items[t.Index]
	return u.processOneLanguage(ctx, t.Command, item, t.RequestID, t.TextHash, t.Destination)
}

// buildCommandFilenameForItem — REMOVED in E4 (June 2026). The
// per-item filename grammar now lives in BuildVoiceoverFilename at
// filename.go (one canonical implementation across the three call
// sites: process.go processLanguage, planner.go Plan, usecase.go
// processOneLanguage). The migration is one line of BuildVoiceoverFilename
// per callsite; no Surface below this line is touched.
