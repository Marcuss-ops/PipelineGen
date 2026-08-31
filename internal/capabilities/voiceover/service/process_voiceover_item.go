// Package voiceover — process_voiceover_item.go (BLOC5.3 commit-2-child-canonical, June 2026).
//
// Canonical per-item voiceover orchestrator (Pattern 0 — port abstraction
// layer, AGENTS.md). Replaces the legacy process_one.go::ProcessOneVoiceoverUseCase
// bridge (GenerateVoiceoverItemCommand → BatchRequest → Service.GenerateBatch)
// as the SINGLE canonical per-language pipeline, mirroring the per-language
// stage sequence of usecase.go::processOneLanguage (Batch 7-port use case).
//
// BLOC5.3 audit-pin invariants (P0.6 — pass-through, no recalc):
//   - item.TextHash is trusted (pre-computed by fanout via textHashSHA256).
//   - item.Voice is trusted (pre-resolved by fanout from VoiceOverrides[lang]).
//   - item.Filename is trusted (pre-computed by fanout via buildItemFilename).
//   - item.RequestID is trusted (pre-correlates parent → child audit lineage).
//   - NO BatchRequest construction — canonical port-driven pipeline only.
//
// Failure mode contract (godlike/07 — no fake availability): every stage
// returns a VoiceoverItemResult with typed Status + Error string. The
// handler maps the (result, error) tuple into the dispatcher contract:
//   - nil item / nil-validate failure → (*VoiceoverItemResult = nil, error)
//   - per-stage failure             → (*VoiceoverItemResult{failed}, nil)
//   - success                       → (*VoiceoverItemResult{completed}, nil)
//
// Lifecycle atomicity (PR-VO-A2): stage 4 (SQLite swap) is wrapped in a
// single BeginTx/Commit so the DELETE of the OLD row + INSERT of the new
// + outbox EnqueueIndexEvent all commit atomically. The swap tx is
// caller-owned; the use case holds the *sql.Tx across the 3 calls.
//
// PR-VO-USECASE-PROCESS-DRY (July 2026): the per-item body is now a
// THIN WRAPPER around the shared ProcessSegmentUseCase (usecase/process_segment.go).
// The same neutral struct is consumed by usecase.go::processOneLanguage
// (batch path) — godlike/06 SSOT "one canonical owner per fact" for the
// per-item pipeline. Pre-DRY the per-item body carried the full 5-stage
// inline code; post-DRY the per-item body just (1) validates, (2)
// resolves destination with DefaultFolderResolver fallback, (3) builds
// a ProcessSegmentCommand, and (4) delegates to ProcessSegmentUseCase.Execute.
// The shared runner owns typed stage classification; this wrapper propagates
// PipelineError without parsing human-readable error text.
package voiceover

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
)

type voiceoverFingerprintLookup interface {
	FindByFingerprint(context.Context, string) (*persistence.VoiceoverRecord, error)
}

// ProcessVoiceoverItemDeps wires dependencies for the canonical per-item
// pipeline (Pattern 0, AGENTS.md). All required ports are mandatory —
// the constructor panics on nil per fail-fast WireUp pattern.
//
// Azione #6 (July 2026): TransactionalOutbox and FilenameBuilder removed.
// TransactionalOutbox was a type-alias for TxOutboxEnqueuer, never used
// by Execute (the finalizer owns the outbox, PR-VO-B3). FilenameBuilder
// was nil-safe per Azione #5; Execute trusts item.Filename pre-computed
// by the fanout (BLOC4 P0.6 pass-through invariant).
//
// PR-NEST-FLAT-DEPS-VOICEOVER-PERITEM (July 2026): the previous flat
// shape had 9 mandatory ports (or 10 before Azione #6), tripping the
// `max_struct_deps=8` archcheck gate. The struct now nests the 9
// fields into 5 purpose-grouped sub-bundles (each ≤5 fields, all ≤8):
//
//   - Pipeline (5): TTSProvider, DestinationResolver, AudioPostProcessor,
//     Publisher, VoiceoverRepository — the per-item
//     pipeline's 5 typed ports.
//   - Recovery (2): DefaultFolderResolver (nil-safe), TxOutboxEnqueuer
//     (nil-safe) — pre-existing nil-tolerant recovery
//     ports for orphan cleanup + missing-destination
//     fallback.
//   - Finalize (1): Finalizer — the unified 6-step finalization port
//     (P0.4 Fase 3a, July 2026).
//   - Output   (1): OutputDir — the per-item output dir for TTS files.
//   - Logger   (1): *zap.Logger — nil-safe via zap.NewNop().
//
// ProcessVoiceoverItemDeps itself carries 5 sub-bundle fields → 5
// fields, well below the 8-field cap. The nesting follows the
// canonical godlike/06 SSOT pattern established by
// PR-NEST-FLAT-DEPS-ARLIST (internal/capabilities/assets/providers/artlist/
// service.go: ServicePorts + ServiceDependencies{Infra, Ports, Domain,
// Repos, Finalizer}).
//
// godlike/06 SSOT: this is the SINGLE canonical Deps surface for
// the per-item pipeline. New ports MUST land in one of the 5
// sub-bundles (or extend the count by adding a new purpose-grouped
// sub-bundle) so ProcessVoiceoverItemDeps stays ≤8 fields.
type ProcessVoiceoverItemDeps struct {
	Pipeline ProcessVoiceoverPipelineDeps
	Recovery ProcessVoiceoverRecoveryDeps
	Finalize ProcessVoiceoverFinalizeDeps
	Output   ProcessVoiceoverOutputDeps
	Logger   *zap.Logger // nil-safe via zap.NewNop()
}

// ProcessVoiceoverPipelineDeps groups the 5 canonical per-item
// pipeline ports (TTSProvider → DestinationResolver → AudioPostProcessor
// → Publisher → Finalize) plus the optional cross-run cache and async
// publish pool. Field count: 7.
type ProcessVoiceoverPipelineDeps struct {
	TTSProvider         TTSProvider
	DestinationResolver DestinationResolver
	AudioPostProcessor  AudioPostProcessor
	Publisher           VoiceoverPublisher
	VoiceoverRepository persistence.Repository
	// VoiceoverCache is the OPTIONAL cross-run cache lookup. When
	// wired, the per-item pipeline short-circuits TTS + upload +
	// finalize when a previous run already produced the same
	// voiceover for the same content fingerprint. Nil-safe.
	VoiceoverCache VoiceoverCacheLookup
	// AsyncPublish is the OPTIONAL bounded publish pool (P0.4: separate
	// TTS pool from publish pool). When wired, Stage 3 (Drive upload +
	// timing) and Stage 4 (SQLite finalize) run in a background goroutine
	// so the TTS slot is freed after synthesis. Nil means synchronous
	// execution (backward compat).
	AsyncPublish AsyncPublishPool
}

// ProcessVoiceoverRecoveryDeps groups the 2 nil-tolerant recovery
// ports (orphan-cleanup + missing-destination fallback). Field count: 2.
type ProcessVoiceoverRecoveryDeps struct {
	// DefaultFolderResolver is OPTIONAL (nil-safe). When item.Destination
	// is nil, Execute calls DefaultFolderResolver.Resolve(ctx) to obtain
	// the configured default Voiceover folder. When nil OR the resolver
	// returns ok=false, Execute surfaces a permanent missing-destination
	// error (P0.2 nil-destination fallback, July 2026).
	DefaultFolderResolver VoiceoverDefaultFolderResolver
	// TxOutboxEnqueuer is the OPTIONAL FASE 4 orphan-cleanup port (July 2026).
	// When Stage 3 (Drive upload) succeeds and Stage 4 (Finalize) fails,
	// the per-item path opens a SEPARATE tx and enqueues a
	// voiceover.cleanup.requested outbox event for the orphaned Drive
	// file. When nil (pre-FASE-4 callers, or composition root not yet
	// wired), the orphan-cleanup path is silently skipped — the
	// background orphan sweeper will eventually recover the Drive file.
	//
	// godlike/07 NO-FAKE-AVAILABILITY: this field is OPTIONAL (nil-safe)
	// at the use case boundary because pre-FASE-4 production code did
	// not wire the outbox for orphan cleanup; the failure mode
	// (orphan sweeper recovery) is the safety net. A future
	// composition-root audit can promote it to mandatory when
	// FASE 4 wiring becomes the canonical production posture.
	TxOutboxEnqueuer TxOutboxEnqueuer
}

// ProcessVoiceoverFinalizeDeps wraps the unified finalization port
// (P0.4 Fase 3a, July 2026). MANDATORY — the per-item pipeline
// delegates all 6 finalization steps (dedupe, delete, insert,
// media_assets projection, index outbox, cleanup outbox) to the
// finalizer inside a caller-owned tx. Field count: 1.
type ProcessVoiceoverFinalizeDeps struct {
	Finalizer VoiceoverFinalizer
}

// ProcessVoiceoverOutputDeps groups the per-item output dir.
// Field count: 1.
type ProcessVoiceoverOutputDeps struct {
	// OutputDir is the base local filesystem directory for TTS output.
	// When the resolved destination's FolderPath is empty, Execute falls
	// back to OutputDir. Mirrors the batch path's Service.outputDir (set
	// from cfg.Storage.VoiceoversPath() at composition time). When empty,
	// the per-item path will fail at the TTS/AudioPost stage with
	// "empty OutputDir" — the caller MUST wire this field.
	// PR-VO-PERITEM-OUTPUTDIR (July 2026): added to close the empty
	// FolderPath gap on the per-item path.
	OutputDir string
}

// ProcessVoiceoverItemUseCase is the canonical per-item voiceover
// orchestrator. The 7-port typed dep surface keeps the use case free
// of any internal/platform/* import (Pattern 0, June 2026) —
// the composition root satisfies each port by structural conformance
// (Go's implicit-interface rules). The compile-time assertion at the
// bottom of this file pins the narrow VoiceoverItemExecutor interface
// conformance so legacy consumers (promo bridge, future call-site
// migrations) can depend on the interface rather than the concrete.
//
// PR-VO-USECASE-PROCESS-DRY (July 2026): the per-item body delegates
// to the SHARED ProcessSegmentUseCase (usecase/process_segment.go). The
// constructor builds a ProcessSegmentUseCase from the same deps so the
// per-item body is shared with the batch use case.
type ProcessVoiceoverItemUseCase struct {
	deps       ProcessVoiceoverItemDeps
	processSeg *ProcessSegmentUseCase // SINGLE canonical per-item pipeline runner (PR-VO-USECASE-PROCESS-DRY)
}

// NewProcessVoiceoverItemUseCase constructs the canonical use case.
// All required deps are mandatory (panic on nil — fail-fast per
// AGENTS.md WireUp pattern). Logger is nil-safe via zap.NewNop().
//
// PR-VO-USECASE-PROCESS-DRY: the constructor now builds a
// ProcessSegmentUseCase from the same deps (TTSProvider, AudioPostProcessor,
// Publisher, VoiceoverRepository, Finalizer, Logger). The
// ProcessSegmentUseCase is the SINGLE canonical per-item pipeline
// runner; the use case just builds the DTO and delegates.
func NewProcessVoiceoverItemUseCase(deps ProcessVoiceoverItemDeps) *ProcessVoiceoverItemUseCase {
	if deps.Pipeline.TTSProvider == nil {
		panic("voiceover.NewProcessVoiceoverItemUseCase: Pipeline.TTSProvider is required (ProcessVoiceoverItemDeps.Pipeline.TTSProvider)")
	}
	if deps.Pipeline.DestinationResolver == nil {
		panic("voiceover.NewProcessVoiceoverItemUseCase: Pipeline.DestinationResolver is required")
	}
	if deps.Pipeline.Publisher == nil {
		panic("voiceover.NewProcessVoiceoverItemUseCase: Pipeline.Publisher is required (E1 cutover: drive-only upload)")
	}
	if deps.Pipeline.VoiceoverRepository == nil {
		panic("voiceover.NewProcessVoiceoverItemUseCase: Pipeline.VoiceoverRepository is required")
	}
	if deps.Finalize.Finalizer == nil {
		panic("voiceover.NewProcessVoiceoverItemUseCase: Finalize.Finalizer is required (P0.4 Fase 3a — unified finalization port)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &ProcessVoiceoverItemUseCase{
		deps: deps,
		processSeg: NewProcessSegmentUseCase(ProcessSegmentDeps{
			TTSProvider:         deps.Pipeline.TTSProvider,
			AudioPostProcessor:  deps.Pipeline.AudioPostProcessor,
			Publisher:           deps.Pipeline.Publisher,
			VoiceoverRepository: deps.Pipeline.VoiceoverRepository,
			Finalizer:           deps.Finalize.Finalizer,
			TxOutboxEnqueuer:    deps.Recovery.TxOutboxEnqueuer,
			Cache: ProcessSegmentCacheDeps{
				VoiceoverCache: deps.Pipeline.VoiceoverCache,
				AsyncPublish:   deps.Pipeline.AsyncPublish,
			},
			Logger: deps.Logger,
		}),
	}
}

// Execute runs the canonical per-item voiceover pipeline via the
// shared ProcessSegmentUseCase (PR-VO-USECASE-PROCESS-DRY, July 2026).
//
// Pre-DRY the body carried the full 5-stage inline code (TTS →
// AudioPost → Publish → BeginTx → Finalize → Commit). Post-DRY the
// body is a thin wrapper that:
//
//  1. Pre-flight: nil-safe + validate gate.
//  2. Stage 0b: destination resolution with DefaultFolderResolver
//     fallback (caller-side concern; the per-item path resolves
//     per-item, the batch path resolves once).
//  3. ID derivation (caller-side; the BLOC4 P0.6 pass-through
//     invariant pins this at the caller layer).
//  4. Builds a ProcessSegmentCommand neutral DTO and calls
//     u.processSeg.Execute(ctx, cmd).
//  5. Propagates the typed PipelineError produced by the shared runner.
//
// The pre-flight, destination resolution, and ID derivation are
// caller-side concerns that BOTH the batch and per-item paths share
// (with different inputs); the ProcessSegmentUseCase handles the 4-stage
// pipeline (TTS → AudioPost → Publish → BeginTx + Finalize + Commit)
// for both.
//
// Per the BLOC4 IN-VOICEOVER PASS-THROUGH invariant (P0.6):
//   - item.TextHash is used verbatim (no re-derivation)
//   - item.Voice is used verbatim (no VoiceOverrides re-resolution)
//   - item.Filename is used verbatim (no template re-substitution)
//
// The handler dispatches (result, error) per the godlike/07 contract:
// Stage 0 failures (nil item, validate, destination resolve) return
// (nil or *VoiceoverItemResult, *PipelineError). Stage 1-4 failures
// (delegated to ProcessSegmentUseCase) return (out, *PipelineError) with
// structured stage and retryability fields.
func (u *ProcessVoiceoverItemUseCase) Execute(ctx context.Context, item *GenerateVoiceoverItemCommand) (*VoiceoverItemResult, error) {
	// Pre-flight: nil-safe + validate gate.
	if item == nil {
		return nil, fmt.Errorf("ProcessVoiceoverItemUseCase.Execute: nil item (callers must pass a non-nil *GenerateVoiceoverItemCommand)")
	}
	if err := item.Validate(); err != nil {
		return nil, fmt.Errorf("ProcessVoiceoverItemUseCase.Execute: validate (lang=%s, request_id=%s): %w", string(item.Language), item.RequestID, err)
	}

	// Stage 0b: destination resolution with DefaultFolderResolver
	// fallback. Delegates to the shared ResolveDestinationWithFallback
	// free function (destination_helpers.go, PR-VO-DRY-PAIR July 2026).
	// Pre-DRY this was a ~30-line if-else cascade duplicated in
	// usecase.go::Execute. Post-DRY both callers route through the
	// same single function.
	dest, err := ResolveDestinationWithFallback(ctx, item.Destination,
		u.deps.Pipeline.DestinationResolver, u.deps.Recovery.DefaultFolderResolver, u.deps.Logger)
	if err != nil {
		out := &VoiceoverItemResult{
			Language:  item.Language,
			Status:    StatusFailed,
			ErrorCode: string(FailureDestinationUnavailable),
			Error:     fmt.Sprintf("destination_resolve_failed: %v", err),
		}
		if errors.Is(err, ErrVoiceoverDestinationUnavailable) {
			out.ErrorCode = VoiceoverDestinationUnavailableCode
			out.Error = fmt.Sprintf("%s: %v", VoiceoverDestinationUnavailableCode, err)
		}
		setFinalStageProgress(out, string(item.Language), item.ParentJobID)
		return out, newPipelineError(StageDestinationResolve, false, err)
	}
	if dest == nil || dest.FolderID == "" {
		out := &VoiceoverItemResult{
			Language:  item.Language,
			Status:    StatusFailed,
			ErrorCode: string(FailureMissingFolder),
			Error:     "missing_folder_id: voiceover destination has no FolderID for upload",
		}
		setFinalStageProgress(out, string(item.Language), item.ParentJobID)
		return out, newPipelineErrorCode(StageDestinationResolve, false, FailureMissingFolder, fmt.Errorf("missing_folder_id"))
	}

	// PR-VO-PERITEM-OUTPUTDIR (July 2026): the resolved destination
	// carries FolderID (Drive target) but NOT FolderPath (local
	// filesystem directory for TTS output). The batch path in
	// process.go:processLanguage explicitly overrides FolderPath
	// via Service.outputDir + ensureOutputDir; the per-item path
	// resolves FolderPath from the OutputDir dep. Without this,
	// ProcessSegmentUseCase.Execute receives an empty FolderPath
	// and fails at the TTS Synthesize call (OutputDir="" →
	// AudioPostProcessor returns "empty OutputDir/Filename").
	if dest.FolderPath == "" && u.deps.Output.OutputDir != "" {
		dest.FolderPath = u.deps.Output.OutputDir
	}

	// Trust item.TextHash from fanout (P0.6 invariant — no re-derive).
	// PR-VO-TYPED-PRIMITIVES (July 2026): item.TextHash is the typed
	// TextHash envelope; buildVoiceoverID first param is raw string,
	// so convert at the seam.
	itemHash := item.TextHash

	// Audit the cross-run cache before doing TTS. Reuse is deliberately not
	// claimed here yet when timing artifacts are required: the row currently
	// stores the audio artifact but not a verifiable timing bundle. This
	// makes the run report honest (candidate/hit/miss) without risking an
	// unsynchronised subtitle timeline.
	fingerprint := BuildVoiceoverContentFingerprint(itemHash, item.Language, item.Voice, dest.FolderID, item.Timing, item.RemoveSilence)
	cacheStatus := "miss"
	cacheReason := "not_found"
	if lookup, ok := u.deps.Pipeline.VoiceoverRepository.(voiceoverFingerprintLookup); ok {
		cached, lookupErr := lookup.FindByFingerprint(ctx, fingerprint)
		if lookupErr != nil {
			cacheStatus = "lookup_error"
			cacheReason = lookupErr.Error()
		} else if cached != nil {
			cacheStatus = "candidate"
			cacheReason = "timing_artifact_not_hydrated"
		}
	} else {
		cacheStatus = "unavailable"
		cacheReason = "repository_does_not_expose_fingerprint_lookup"
	}
	u.deps.Logger.Info("voiceover cross-run cache audit",
		zap.String("language", string(item.Language)),
		zap.String("fingerprint", fingerprint),
		zap.String("status", cacheStatus),
		zap.String("reason", cacheReason))

	// ID is derived deterministically from (textHash, language, folderID).
	id := buildVoiceoverID(string(itemHash), item.Language, dest.FolderID)

	// Build the neutral DTO consumed by the shared ProcessSegmentUseCase.
	// The StyleGroup injection (metaBuf["style_group"] if !empty) is
	// now inside the ProcessSegmentUseCase; the per-item path's prior
	// meta-merge logic is preserved by passing item.Metadata
	// through (mergeUserMetadata in the ProcessSegmentUseCase handles
	// the user-meta overlay + StyleGroup injection in one place).
	cmd := &ProcessSegmentCommand{
		ID:            id,
		RequestID:     item.RequestID,
		TextHash:      itemHash,
		Text:          item.Text,
		Language:      item.Language,
		Voice:         item.Voice,
		Filename:      item.Filename,
		Strategy:      string(item.Strategy),
		Metadata:      item.Metadata,
		RemoveSilence: item.RemoveSilence,
		Timing:        item.Timing,
		Moments:       item.Moments,
		// PR-P12-VOICEOVER-SEMANTIC-FIELDS (July 2026): forward the
		// canonical semantic project identifier from the per-item command
		// (API request or internal caller) so the adapter (Stage 3
		// Publisher) builds the canonical {project}/{language}/ subpath
		// via VoiceoverPath. Empty Project is OK: the adapter falls back
		// to FolderID (legacy) or voiceover ID (graceful degradation).
		Project: item.Project,
		Dest:    dest,
		// Per-item path (child pipeline): no old-row swap context.
		// ShouldSwap stays false (no cleanup event).
	}

	out, err := u.processSeg.Execute(ctx, cmd)
	if err != nil {
		// ProcessSegmentUseCase owns the stage classification. Propagate
		// its typed PipelineError unchanged; no caller parses Error() text.
		var pipelineErr *PipelineError
		if errors.As(err, &pipelineErr) {
			u.deps.Logger.Warn("voiceover.processItem: pipeline run failed",
				zap.String("language", string(item.Language)),
				zap.String("request_id", item.RequestID),
				zap.String("stage", string(pipelineErr.Stage)),
				zap.Bool("retryable", pipelineErr.Retryable),
				zap.String("error_code", string(pipelineErr.FailureCode())),
				zap.String("error", out.Error),
				zap.Error(err))
			return out, err
		}
		// Every canonical runner failure must be typed. Keep this
		// defensive branch fail-closed if a future implementation drifts.
		return out, newPipelineError(StageTxCommit, true, err)
	}

	u.deps.Logger.Info("voiceover.processItem: success",
		zap.String("language", string(item.Language)),
		zap.String("request_id", item.RequestID),
		zap.String("id", out.ID),
		zap.String("drive_link", out.DriveLink))
	return out, nil
}

// Compile-time assertion (AGENTS.md Pattern 0): the production concrete
// *ProcessVoiceoverItemUseCase must structurally satisfy the narrow
// VoiceoverItemExecutor port. Drift between Execute's signature and the
// port contract triggers a compile error at this line.
var _ VoiceoverItemExecutor = (*ProcessVoiceoverItemUseCase)(nil)
