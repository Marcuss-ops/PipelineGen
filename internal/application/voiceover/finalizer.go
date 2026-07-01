// Package voiceover — finalizer.go (P0.4 Fase 3a, unified finalization, July 2026).
//
// voiceoverFinalizer is the SINGLE canonical implementation of the
// VoiceoverFinalizer port. It replaces the two divergent finalization
// paths:
//
//   Path A (child pipeline, process_voiceover_item.go Stage 4):
//     BeginTx → DeleteByIDTx → InsertTx → outbox EnqueueIndexEvent → Commit
//     (MISSING: dedupe gate, media_assets projection, cleanup event)
//
//   Path B (legacy batch, stages.go finalizeStage):
//     BeginTx → dedupe gate → DeleteByIDTx → InsertTx →
//     UpsertVoiceoverProjectionTx → outbox EnqueueIndexEvent →
//     outbox EnqueueCleanupEvent → Commit
//
// Post-Fase-3a BOTH paths call the same Finalize(ctx, tx, cmd) method.
// The caller owns the transaction (BeginTx + Commit); the finalizer
// owns the 6 logical steps inside the tx.
//
// Step classification (Audit P0 #2, July 2026) — production path
// cannot degrade Required steps into the optional bucket.
//
//   - Step 1 (dedupe):             OPTIONAL — skipped when DriveFileID
//                                   is empty; dedupe-reuse short-circuits
//                                   Step 2..6 with Reused=true.
//   - Step 2 (DeleteByIDTx):       MANDATORY — no execution-state
//                                   variants; mandatory universe.
//   - Step 3 (InsertTx):           MANDATORY — same.
//   - Step 4 (media_assets proj.): REQUIRED — LifecycleService nil
//                                   is a fatal wiring error (not a
//                                   degrade). Execution-state marker
//                                   "media_assets_projection: executed"
//                                   is always appended on success.
//   - Step 5 (index outbox):       REQUIRED — Outbox nil is a fatal
//                                   wiring error. FileHash=="" is a
//                                   guard-skip (data-state reason).
//   - Step 6 (cleanup outbox):     REQUIRED — Outbox nil is a fatal
//                                   wiring error. ShouldSwap==false OR
//                                   no prior artefacts is a guard-skip.
//
// Required-dep failures (deps.LifecycleService == nil || deps.Outbox ==
// nil) result in a fail-fast (nil, fmt.Errorf(...)) — NEVER degraded
// to RequiredSteps with an execution-state marker. godlike/07 ZERO
// LEGACY: wiring is a precondition; we cannot lie the operator by
// recording a step we never executed.
package voiceover

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────────
// FinalizeCommand — canonical DTO for both finalization paths
// ─────────────────────────────────────────────────────────────────────

// FinalizeCommand carries all data needed for the 6-step finalization
// inside a caller-owned transaction. Every field is populated by the
// caller; the finalizer reads them but never mutates the command.
type FinalizeCommand struct {
	// Identity & Content
	ID        string
	RequestID string
	TextHash  string
	Text      string // Full text; truncated to 100 chars for preview column
	Language  string
	Voice     string
	Filename  string
	Strategy  string
	MetaJSON  []byte // Canonical JSON metadata map

	// Audio Asset State
	LocalPath   string
	CleanedPath string
	FileHash    string // Index outbox skipped when empty

	// Destination & Drive State
	FolderID     string
	FolderPath   string
	DriveFileID  string // Dedupe gate skipped when empty
	DriveLink    string
	DownloadLink string

	// Replace-Mode Cleanup Context
	// When ShouldSwap is true AND outbox is wired, the finalizer
	// enqueues a voiceover.cleanup.requested event for the old
	// Drive file + local paths. Nil-safe when false.
	ShouldSwap     bool
	OldDriveFileID string
	OldLocalPath   string
	OldCleanedPath string
}

// ─────────────────────────────────────────────────────────────────────
// FinalizeResult — dedupe outcome
// ─────────────────────────────────────────────────────────────────────

// FinalizeResult is returned by VoiceoverFinalizer.Finalize so the
// caller can observe whether the dedupe gate matched an existing row,
// update the canonical ID, and inspect which optional and required
// steps were tracked.
//
// Audit P0 #2 (July 2026): the pre-P0 #2 surface exposed only
// SkippedSteps []string which conflates two semantically-distinct
// failure modes:
//
//   (a) Optional step that was guard-skipped because of a data-state
//       reason (e.g. dedupe gate not triggered because DriveFileID is
//       empty) — recordable, OPERATOR-ACTIONABLE.
//   (b) Required production-path step that was unwired at composition
//       time (e.g. LifecycleService nil) — fatal wiring error that
//       MUST propagate as a Go error.
//
// Mixing (a) and (b) in SkippedSteps made the silent-failure mode
// indistinguishable from the legitimate guard-skip mode. P0 #2
// splits the surface into OptionalSteps (recordable skip) and
// RequiredSteps (execution-state marker for production-required
// steps that DID execute or were guard-skipped for data reasons —
// NEVER for wiring reasons; wiring failures fatal at fail-fast).
type FinalizeResult struct {
	// ID is the canonical voiceover ID after finalization.
	// Equal to cmd.ID except when Reused is true (then it's the
	// matched existing row ID from the dedupe gate).
	ID string

	// Reused is true when the dedupe gate matched an existing row
	// and the finalizer short-circuited persistence (no INSERT,
	// no projection, no outbox). The caller should use the
	// returned ID as the canonical identifier.
	Reused bool

	// OptionalSteps lists which OPTIONAL finalization steps were
	// guard-skipped for data-state reasons. The list is only set
	// when a step has a data-state guard; production deps unwired
	// at composition time DO NOT appear here — they fatal-surface
	// as a non-nil Go error from Finalize().
	//
	// Possible values:
	//
	//   "dedupe: empty DriveFileID"   — Step 1 not run (data guard)
	//   "dedupe: reuse existing row"   — Steps 2-6 not run (early
	//                                    return on Step 1 dedupe
	//                                    reuse hit; Reused=true)
	OptionalSteps []string `json:"optional_steps,omitempty"`

	// RequiredSteps tracks the execution state of every
	// production-required step (Steps 4, 5, 6). Each entry has the
	// shape "<step_name>: <state>" where state is one of:
	//
	//   StateExecuted     — "media_assets_projection: executed"
	//                       "index_outbox: executed"
	//                       "cleanup_outbox: executed"
	//   StateGuarded      — "index_outbox: guarded (empty FileHash)"
	//                       "cleanup_outbox: guarded (ShouldSwap=false)"
	//                       "cleanup_outbox: guarded (no prior artefacts)"
	//
	// State values are constants defined at the bottom of this file
	// (StateExecuted, StateGuarded). They are NOT surfaced on the
	// wire and used only by callers parsing internal log lines;
	// wire-format consumers should treat them as opaque strings.
	//
	// RequiredSteps is NEVER written under "unwired" — production
	// wiring failures fatal at fail-fast (godlike/07 ZERO LEGACY).
	RequiredSteps []string `json:"required_steps,omitempty"`

	// CompletionState records the post-commit SQL verification
	// outcome (audit P0.5, July 2026). Surface on the canonical
	// result struct so callers can react without parsing log lines.
	// Only the legacy batch finalizeStage populates this field
	// (the child pipeline ProcessVoiceoverItemUseCase does not run
	// the verifier today; a future BLOC5.4 expansion can wire the
	// canonical verifier-port into the per-item use case).
	//
	// Mapping contract (single source of truth in finalizeStage):
	//
	//   verifier returns nil                                       → StateCompleted
	//   verifier returns nil + finalizeStage post-commit guard nil → StateCompleted
	//   verifier returns err wrapping ErrReconciliationRequired    → StateReconciliationRequired
	//   verifier returns any other non-nil err                     → StateCompletedUnverified
	//   verifier unwired (Service.postCommitVerifier == nil)       → "" (omitempty hides the wire)
	//
	// omitempty on JSON because the pre-P0.5 wire shape was
	// Reused/SkippedSteps/ID only; legacy consumers continue to
	// observe byte-equivalent wire output.
	CompletionState CompletionState `json:"completion_state,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────
// voiceoverFinalizer — concrete implementation
// ─────────────────────────────────────────────────────────────────────

// voiceoverFinalizerDeps holds the external dependencies for the
// concrete finalizer. All ports are optional (nil-safe) except
// voiceoverRepo which is mandatory (INSERT/DELETE are always needed).
type voiceoverFinalizerDeps struct {
	VoiceoverRepo    VoiceoverRepository // mandatory
	Outbox           TxOutboxEnqueuer    // nil-safe (skip index + cleanup)
	LifecycleService LifecycleProjectionUpserter // nil-safe (skip media_assets)
	Logger           *zap.Logger         // nil-safe via zap.NewNop()
}

// LifecycleProjectionUpserter is the narrow port for writing the
// media_assets projection row. Extracted so the finalizer doesn't
// depend on the full lifecycle.Service surface. The production
// concrete is *lifecycle.Service.UpsertVoiceoverProjectionTx.
type LifecycleProjectionUpserter interface {
	UpsertVoiceoverProjectionTx(ctx context.Context, tx *sql.Tx, input *VoiceoverProjectionInput) error
}

// VoiceoverProjectionInput is the canonical input shape for the
// media_assets projection UPSERT.
type VoiceoverProjectionInput struct {
	ID           string
	Source       string
	Name         string
	Filename     string
	FolderID     string
	FolderPath   string
	MediaType    string
	LocalPath    string
	DriveFileID  string
	DriveLink    string
	DownloadLink string
	FileHash     string
	Language     string
	Status       string
	Metadata     string
}

// voiceoverFinalizer is the concrete implementation of VoiceoverFinalizer.
type voiceoverFinalizer struct {
	deps voiceoverFinalizerDeps
}

// newVoiceoverFinalizer constructs the finalizer. Only voiceoverRepo is
// mandatory (panic on nil — fail-fast per AGENTS.md WireUp pattern).
func newVoiceoverFinalizer(deps voiceoverFinalizerDeps) *voiceoverFinalizer {
	if deps.VoiceoverRepo == nil {
		panic("voiceover.newVoiceoverFinalizer: VoiceoverRepo is required")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &voiceoverFinalizer{deps: deps}
}

// NewVoiceoverFinalizer is the exported constructor for the unified
// VoiceoverFinalizer (P0.4 Fase 3a, July 2026). The composition root
// (internal/app/build_bundles_voiceover.go) calls this to construct a
// SINGLE finalizer instance shared by both the child pipeline
// (ProcessVoiceoverItemUseCase) and the legacy batch pipeline (Service).
//
// Parameters:
//   - voRepo: persistence.Repository (BeginTx, InsertTx, DeleteByIDTx,
//     CountByDriveFileIDTx) — mandatory, panics on nil.
//   - outbox: TxOutboxEnqueuer (EnqueueIndexEvent, EnqueueCleanupEvent)
//     — nil-safe (skip outbox steps when nil).
//   - lifecycleSvc: LifecycleProjectionUpserter (UpsertVoiceoverProjectionTx)
//     — nil-safe (skip media_assets projection when nil).
//   - log: *zap.Logger — nil-safe (zap.NewNop() fallback).
//
// Returns VoiceoverFinalizer so the composition root can inject an
// interface — test doubles swap the concrete without churn.
func NewVoiceoverFinalizer(
	voRepo VoiceoverRepository,
	outbox TxOutboxEnqueuer,
	lifecycleSvc LifecycleProjectionUpserter,
	log *zap.Logger,
) VoiceoverFinalizer {
	return newVoiceoverFinalizer(voiceoverFinalizerDeps{
		VoiceoverRepo:    voRepo,
		Outbox:           outbox,
		LifecycleService: lifecycleSvc,
		Logger:           log,
	})
}

// Compile-time assertion (AGENTS.md Pattern 0).
var _ VoiceoverFinalizer = (*voiceoverFinalizer)(nil)

// Failure-prefix constants for required-dep-not-wired fatal errors.
// Surfaced in the error message so log scanners can grep for the
// failure class without parsing the human-readable suffix.
const (
	// errRequiredStepNotWired — fail-fast error prefix when a
	// production-required dep is nil at Finalize() entry. Per
	// audit P0 #2, this MUST NEVER be reported as a guard-skipped
	// step on FinalizeResult.RequiredSteps; it surfaces as a Go
	// error and propagates up the call chain so the caller can
	// fail-closed at the per-language boundary (Service.finalizeStage
	// maps the error to BatchItem.Status=StatusFailed).
	errRequiredStepNotWired = "voiceoverFinalizer: required step %q not wired"

	// Required-step names — wire-stable identifiers for the
	// required-deps the finalizer gates on. Surfaced in the error
	// message + as execution-state prefixes on RequiredSteps.
	//
	// Audit P0 #2 (July 2026): the strings are byte-equivalent with
	// the pre-P0 #2 SkippedSteps values ("media_assets_projection",
	// "index_outbox", "cleanup_outbox") so log-grep anchors and
	// operator alerting rules keyed on these substrings continue to
	// fire. The SEMANTIC improvement is the OptionalSteps /
	// RequiredSteps SPLIT (separate fields); the step-name prefixes
	// are wire-stable per godlike/06 (one canonical owner per fact).
	requiredStepMediaAssetsProjection = "media_assets_projection"
	requiredStepIndexOutbox           = "index_outbox"
	requiredStepCleanupOutbox         = "cleanup_outbox"

	// Execution-state markers appended to RequiredSteps on a
	// successful Finalize. "executed" means the deps were wired AND
	// the step ran. "guarded (...)" means the deps were wired BUT
	// a data-state guard prevented execution (e.g. empty FileHash,
	// ShouldSwap=false). State markers are NOT surfaced on
	// wire-format (JSON) consumers; callers parsing internal log
	// lines or programmatic step-state assertions should treat
	// them as the <=256-byte canonical strings below. Renaming is
	// HOW a downstream audit pins unannounced drift.
	requiredStateExecuted = "executed"
	requiredStateGuarded  = "guarded"
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

	rec := &VoiceoverRecord{
		ID:           cmd.ID,
		RequestID:    cmd.RequestID,
		TextHash:     cmd.TextHash,
		TextPreview:  textPreview,
		Language:     cmd.Language,
		Voice:        cmd.Voice,
		Filename:     cmd.Filename,
		LocalPath:    cmd.LocalPath,
		CleanedPath:  cmd.CleanedPath,
		FolderID:     cmd.FolderID,
		FolderPath:   cmd.FolderPath,
		DriveFileID:  cmd.DriveFileID,
		DriveLink:    cmd.DriveLink,
		DownloadLink: cmd.DownloadLink,
		FileHash:     cmd.FileHash,
		Status:       string(StatusGenerated),
		Strategy:     cmd.Strategy,
		Metadata:     string(cmd.MetaJSON),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := f.deps.VoiceoverRepo.InsertTx(ctx, tx, rec); err != nil {
		return nil, fmt.Errorf("voiceoverFinalizer: InsertTx: %w", err)
	}

	// ── Step 4: media_assets projection UPSERT (REQUIRED) ──
	// P0.4 Fase 3a (July 2026): this step was MISSING in the child
	// pipeline (Path A). The unified finalizer now writes the
	// media_assets row for BOTH paths. Audit P0 #2: LifecycleService
	// nil is a fatal wiring error (fail-fast above), so a non-nil
	// LifecycleService here is always present.
	if err := f.deps.LifecycleService.UpsertVoiceoverProjectionTx(ctx, tx, &VoiceoverProjectionInput{
		ID:           cmd.ID,
		Source:       "voiceover",
		Name:         textPreview,
		Filename:     cmd.Filename,
		FolderID:     cmd.FolderID,
		FolderPath:   cmd.FolderPath,
		MediaType:    "audio",
		LocalPath:    cmd.LocalPath,
		DriveFileID:  cmd.DriveFileID,
		DriveLink:    cmd.DriveLink,
		DownloadLink: cmd.DownloadLink,
		FileHash:     cmd.FileHash,
		Language:     cmd.Language,
		Status:       string(StatusGenerated),
		Metadata:     string(cmd.MetaJSON),
	}); err != nil {
		return nil, fmt.Errorf("voiceoverFinalizer: UpsertVoiceoverProjectionTx (media_assets): %w", err)
	}
	required = append(required, formatRequiredState(requiredStepMediaAssetsProjection, requiredStateExecuted))

	// ── Step 5: Outbox — asset.index.requested (REQUIRED) ──
	// Audit P0 #2: Outbox nil is fatal (fail-fast above). FileHash
	// empty is a data-state guard-skip with execution marker.
	if cmd.FileHash != "" {
		if err := f.deps.Outbox.EnqueueIndexEvent(ctx, tx, cmd.ID, cmd.FileHash); err != nil {
			return nil, fmt.Errorf("voiceoverFinalizer: EnqueueIndexEvent: %w", err)
		}
		required = append(required, formatRequiredState(requiredStepIndexOutbox, requiredStateExecuted))
	} else {
		required = append(required, formatRequiredState(requiredStepIndexOutbox, requiredStateGuarded, "empty FileHash"))
	}

	// ── Step 6: Outbox — voiceover.cleanup.requested (REQUIRED) ──
	// Audit P0 #2: Outbox nil is fatal (fail-fast above).
	// ShouldSwap=false OR no prior artefacts are data-state
	// guard-skips with execution markers.
	cleanupExecuted := false
	if cmd.ShouldSwap {
		var oldLocalPaths []string
		if cmd.OldLocalPath != "" {
			oldLocalPaths = append(oldLocalPaths, cmd.OldLocalPath)
		}
		if cmd.OldCleanedPath != "" && cmd.OldCleanedPath != cmd.OldLocalPath {
			oldLocalPaths = append(oldLocalPaths, cmd.OldCleanedPath)
		}
		if cmd.OldDriveFileID != "" || len(oldLocalPaths) > 0 {
			if err := f.deps.Outbox.EnqueueCleanupEvent(ctx, tx,
				cmd.ID,
				cmd.OldDriveFileID,
				cmd.DriveFileID,
				oldLocalPaths,
			); err != nil {
				return nil, fmt.Errorf("voiceoverFinalizer: EnqueueCleanupEvent: %w", err)
			}
			cleanupExecuted = true
		}
	}
	switch {
	case cleanupExecuted:
		required = append(required, formatRequiredState(requiredStepCleanupOutbox, requiredStateExecuted))
	case !cmd.ShouldSwap:
		required = append(required, formatRequiredState(requiredStepCleanupOutbox, requiredStateGuarded, "ShouldSwap=false"))
	default:
		required = append(required, formatRequiredState(requiredStepCleanupOutbox, requiredStateGuarded, "no prior artefacts"))
	}

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
