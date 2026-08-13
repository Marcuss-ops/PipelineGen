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
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
//
// Finalizer (PR-VO-USECASE-PROCESS-DRY, July 2026) is MANDATORY —
// the use case now delegates the per-item finalization (Steps 4-6
// including the dedupe gate + media_assets projection + cleanup
// outbox) to the unified VoiceoverFinalizer port (P0.4 Fase 3a,
// July 2026). Pre-DRY the batch path did manual TX orchestration
// (BeginTx → DeleteByIDTx → InsertTx → outbox → Commit) which
// MISSED the dedupe gate, media_assets projection, and cleanup
// outbox — godlike/07 "no fake availability" gap closed by the
// post-DRY migration. The composition root wires the SAME
// finalizer instance used by the per-item path so both call
// paths share a single canonical finalization port.
//
// godlike/07 minimal-blast-radius: TransactionalOutbox is REMOVED
// (Azione #4, July 2026). The Finalizer owns the outbox path now;
// this field was RETAINED-but-unused since the DRY migration.
type UseCaseDeps struct {
	TTSProvider           TTSProvider
	DestinationResolver   DestinationResolver
	AudioPostProcessor    AudioPostProcessor
	Publisher             VoiceoverPublisher
	VoiceoverRepository   persistence.Repository
	Logger                *zap.Logger
	DefaultFolderResolver VoiceoverDefaultFolderResolver
	// Finalizer is the unified finalization port (P0.4 Fase 3a).
	// MANDATORY post-DRY. Composition root injects the same
	// *voiceoverFinalizer shared by the per-item use case.
	Finalizer VoiceoverFinalizer

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
//
// PR-VO-USECASE-PROCESS-DRY (July 2026): the per-item body
// (processOneLanguage) now delegates to the SHARED
// ProcessSegmentUseCase (usecase/process_segment.go) — the same neutral
// struct consumed by ProcessVoiceoverItemUseCase (per-item path).
// Pre-DRY the batch path did manual TX orchestration; post-DRY
// the batch path uses the finalizer via the ProcessSegmentUseCase,
// gaining the dedupe gate + media_assets projection + cleanup
// outbox that it was missing.
type GenerateVoiceoversUseCase struct {
	deps       UseCaseDeps
	executor   *Executor              // bounded parallel fan-out (worker pool)
	processSeg *ProcessSegmentUseCase // SINGLE canonical per-item pipeline runner (PR-VO-USECASE-PROCESS-DRY)
}

// NewGenerateVoiceoversUseCase constructs the canonical use case.
// Mandatory deps are fail-fast (panic on nil) per AGENTS.md WireUp
// pattern; optional deps are nil-safe.
//
// PR-VO-USECASE-PROCESS-DRY: Finalizer is now a mandatory dep
// (panics on nil). The constructor builds the ProcessSegmentUseCase
// from the same deps so the per-item body is shared with the
// per-item use case.
func NewGenerateVoiceoversUseCase(deps UseCaseDeps) *GenerateVoiceoversUseCase {
	if deps.TTSProvider == nil {
		panic("GenerateVoiceoversUseCase: TTSProvider is required (UseCaseDeps.TTSProvider)")
	}
	if deps.DestinationResolver == nil {
		panic("GenerateVoiceoversUseCase: DestinationResolver is required (UseCaseDeps.DestinationResolver)")
	}
	if deps.Publisher == nil {
		panic("GenerateVoiceoversUseCase: Publisher is required (UseCaseDeps.Publisher)")
	}
	if deps.VoiceoverRepository == nil {
		panic("GenerateVoiceoversUseCase: VoiceoverRepository is required (UseCaseDeps.VoiceoverRepository)")
	}
	// PR-VO-USECASE-PROCESS-DRY: TransactionalOutbox is REMOVED
	// (Azione #4, July 2026). The Finalizer owns the outbox path now;
	// this field was RETAINED-but-unused since the DRY migration.
	if deps.Finalizer == nil {
		panic("GenerateVoiceoversUseCase: Finalizer is required (PR-VO-USECASE-PROCESS-DRY — post-DRY the per-item body delegates to the finalizer)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	resolved := config.DefaultVoiceoverDefaults()
	if deps.DefaultParallelism <= 0 {
		deps.DefaultParallelism = resolved.DefaultParallelism
	}
	if deps.MaxParallelism <= 0 {
		deps.MaxParallelism = resolved.MaxParallelism
	}
	return &GenerateVoiceoversUseCase{
		deps:     deps,
		executor: NewExecutor(deps.Logger),
		processSeg: NewProcessSegmentUseCase(ProcessSegmentDeps{
			TTSProvider:         deps.TTSProvider,
			AudioPostProcessor:  deps.AudioPostProcessor,
			Publisher:           deps.Publisher,
			VoiceoverRepository: deps.VoiceoverRepository,
			Finalizer:           deps.Finalizer,
			Logger:              deps.Logger,
		}),
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
	// PR-VO-DRY-PAIR (July 2026): delegate to the shared
	// ResolveDestinationWithFallback free function (destination_helpers.go).
	// Pre-DRY this was a ~25-line switch block duplicated in
	// process_voiceover_item.go::Execute. Post-DRY both callers
	// route through the same single function.
	dest, err := ResolveDestinationWithFallback(ctx, cmd.Destination,
		u.deps.DestinationResolver, u.deps.DefaultFolderResolver, u.deps.Logger)
	if err != nil {
		result.OK = false
		result.Error = fmt.Sprintf("destination resolve: %v", err)
		if errors.Is(err, ErrVoiceoverDestinationUnavailable) {
			result.ErrorCode = VoiceoverDestinationUnavailableCode
		}
		result.CompletedAt = time.Now().UTC()
		return result, fmt.Errorf("GenerateVoiceoversUseCase.Execute: resolve destination: %w", err)
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
	// PR-VO-TYPED-PRIMITIVES (July 2026): the per-task textHash is
	// threaded from Task.TextHash (typed envelope) verbatim. The
	// underlying string representation is byte-equivalent with the
	// pre-refactor value at every wire boundary.
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
//
// PR-VO-USECASE-PROCESS-DRY (July 2026): the per-item body is
// now a THIN WRAPPER around the shared ProcessSegmentUseCase
// (usecase/process_segment.go). Pre-DRY the body did manual TX
// orchestration (BeginTx → DeleteByIDTx → InsertTx → outbox →
// Commit) which MISSED the dedupe gate + media_assets projection
// + cleanup outbox that the per-item path already had. Post-DRY
// both paths share the SAME canonical per-item pipeline
// (delegated to the VoiceoverFinalizer).
//
// godlike/07 minimal-blast-radius: the caller-side concerns stay
// in processOneLanguage (buildVoiceoverID + BuildVoiceoverFilename
// are pre-computed here, not in the ProcessSegmentUseCase, because the
// per-item path also needs them and the ProcessSegmentUseCase trusts
// the inputs verbatim per the BLOC4 P0.6 pass-through invariant).
// The cmd.RemoveSilence *bool dereference stays here (per-item
// shape is bool; the typed-wire surface for the use case is *bool).
func (u *GenerateVoiceoversUseCase) processOneLanguage(
	ctx context.Context,
	cmd *GenerateVoiceoversCommand,
	itemSpec VoiceoverItem,
	requestID string,
	// PR-VO-TYPED-PRIMITIVES (July 2026): textHash is the typed
	// 16-char per-item TextHash envelope (Task.TextHash from
	// planner.go::Plan).
	textHash TextHash,
	dest *ResolvedDestination,
) VoiceoverItemResult {
	// Pre-flight: destination check. The ProcessSegmentUseCase would also
	// surface this, but we check here so the per-item path can
	// return a zero-value result with a clean error message WITHOUT
	// invoking the TTSProvider (mirrors the pre-DRY short-circuit
	// that processOneLanguage had at the top of the body).
	if dest == nil || dest.FolderID == "" {
		return VoiceoverItemResult{
			Language: itemSpec.Language,
			Status:   StatusFailed,
			Error:    "missing_folder_id: voiceover destination has no FolderID for upload",
		}
	}

	// PR-VO-TYPED-PRIMITIVES (July 2026): textHash is the typed
	// TextHash envelope. buildVoiceoverID first param is raw string
	// (the filename-generation + ID-generation paths consume a
	// polymorphic fingerprint); explicit string() conversion at the
	// seam.
	id := buildVoiceoverID(string(textHash), itemSpec.Language, dest.FolderID)
	// E4: buildCommandFilenameForItem → canonical BuildVoiceoverFilename.
	// Inputs are pre-validated by itemSpec via the higher-layer
	// GenerateVoiceoversCommand.Validate / GenerateVoiceoverItemCommand.Validate
	// gates, so the error path is unreachable in production; panic
	// surfaces regressions loud-fast in tests.
	//
	// PR-VO-TYPED-PRIMITIVES (July 2026): same string() conversion
	// for the FilenameSpec.TextHash field.
	filename, err := BuildVoiceoverFilename(FilenameSpec{
		Text:     itemSpec.Text,
		Language: itemSpec.Language,
		TextHash: string(textHash),
		Template: itemSpec.Filename,
	})
	if err != nil {
		panic(fmt.Sprintf("voiceover.BuildVoiceoverFilename (processOneLanguage): %v (item=%+v)", err, itemSpec))
	}

	// Derive RemoveSilence (cmd carries bool; ProcessSegmentCommand
	// carries plain bool). The TTS provider (Stage 1 of the
	// ProcessSegmentUseCase) NEVER receives RemoveSilence=true (P0.2
	// Fase 2c); AudioPostProcessor (Stage 2) is the sole owner of
	// silence removal.
	removeSilence := cmd.RemoveSilence

	// Build the neutral DTO consumed by the shared ProcessSegmentUseCase.
	in := &ProcessSegmentCommand{
		ID:            id,
		RequestID:     requestID,
		TextHash:      textHash,
		Text:          itemSpec.Text,
		Language:      itemSpec.Language,
		Voice:         itemSpec.Voice,
		Filename:      filename,
		Strategy:      string(cmd.Strategy),
		Metadata:      cmd.Metadata,
		RemoveSilence: removeSilence,
		Timing:        cmd.Timing,
		Dest:          dest,
		Project:       cmd.Project,
		// ShouldSwap stays false: the batch path does not capture
		// old-row swap context today (the pre-DRY code had a
		// PreReadByID call but explicitly discarded the result —
		// the legacy Service.GenerateBatch path's replace-mode
		// cleanup lives in Service.cleanupOrphanVoiceover, not
		// here). A future CUTOVER wave can wire the swap context.
	}

	out, runErr := u.processSeg.Execute(ctx, in)
	if out == nil {
		// Defensive: ProcessSegmentUseCase.Execute returns nil out
		// only for nil input (already checked above) or other
		// catastrophic failures. Surface a typed failed result
		// so the bounded executor's PerLanguage[] slice stays
		// length-preserved.
		return VoiceoverItemResult{
			Language: itemSpec.Language,
			Voice:    itemSpec.Voice,
			Filename: filename,
			ID:       id,
			Status:   StatusFailed,
			Error:    fmt.Sprintf("pipeline_run_failed: %v", runErr),
		}
	}
	return *out
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
