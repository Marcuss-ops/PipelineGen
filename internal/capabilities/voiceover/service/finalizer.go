// Package voiceover — finalizer.go (P0.4 Fase 3a, unified finalization, July 2026).
//
// voiceoverFinalizer is the SINGLE canonical implementation of the
// VoiceoverFinalizer port. It replaces the two divergent finalization
// paths:
//
//	Path A (child pipeline, process_voiceover_item.go Stage 4):
//	  BeginTx → DeleteByIDTx → InsertTx → outbox EnqueueIndexEvent → Commit
//	  (MISSING: dedupe gate, media_assets projection, cleanup event)
//
//	Path B (legacy batch, stages.go finalizeStage):
//	  BeginTx → dedupe gate → DeleteByIDTx → InsertTx →
//	  UpsertVoiceoverProjectionTx → outbox EnqueueIndexEvent →
//	  outbox EnqueueCleanupEvent → Commit
//
// Post-Fase-3a BOTH paths call the same Finalize(ctx, tx, cmd) method.
// The caller owns the transaction (BeginTx + Commit); the finalizer
// owns the 6 logical steps inside the tx.
//
// Step classification (Audit P0 #2, July 2026) — production path
// cannot degrade Required steps into the optional bucket.
//
//   - Step 1 (dedupe):             OPTIONAL — skipped when DriveFileID
//     is empty; dedupe-reuse short-circuits
//     Step 2..6 with Reused=true.
//   - Step 2 (DeleteByIDTx):       MANDATORY — no execution-state
//     variants; mandatory universe.
//   - Step 3 (InsertTx):           MANDATORY — same.
//   - Step 4 (media_assets proj.): REQUIRED — LifecycleService nil
//     is a fatal wiring error (not a
//     degrade). Execution-state marker
//     "media_assets_projection: executed"
//     is always appended on success.
//   - Step 5 (index outbox):       REQUIRED — Outbox nil is a fatal
//     wiring error. LegacyFileMD5=="" is a
//     guard-skip (data-state reason).
//   - Step 6 (cleanup outbox):     REQUIRED — Outbox nil is a fatal
//     wiring error. ShouldSwap==false OR
//     no prior artefacts is a guard-skip.
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

	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
)

// ─────────────────────────────────────────────────────────────────────
// FinalizeCommand — canonical DTO for both finalization paths
// ─────────────────────────────────────────────────────────────────────

// FinalizeCommand carries all data needed for the 6-step finalization
// inside a caller-owned transaction. Every field is populated by the
// caller; the finalizer reads them but never mutates the command.
type FinalizeCommand struct {
	// Fingerprint is the cross-run content/policy cache key. It excludes
	// JobID and is persisted separately from IdempotencyKey.
	Fingerprint string
	// IdempotencyKey is the deterministic retry-safe deduplication key
	// computed by BuildVoiceoverIdempotencyKey(jobID, language, textHash, policyFingerprint).
	// Step 0 of Finalize reads this field BEFORE the dedupe gate (Step 1)
	// to short-circuit the entire 6-step sequence when a prior attempt
	// already persisted the same logical row. Empty IdempotencyKey means
	// "skip the idempotency gate" (backward-compat with pre-FASE-3 callers).
	//
	// godlike/06 SSOT: the canonical key derivation lives ONLY in
	// process_segment.go::BuildVoiceoverIdempotencyKey; the finalizer
	// trusts the caller-supplied key verbatim (no re-derivation).
	IdempotencyKey string

	// JobID is the canonical job identifier that produced this voiceover
	// item. Stored in the voiceovers row for audit-trail correlation
	// (operator query: "which job run produced this Drive audio file?").
	// Empty JobID is OK — the column default is '' and pre-FASE-3 rows
	// will carry the empty sentinel.
	JobID string

	// Identity & Content
	ID        string
	RequestID string
	// TextHash is the canonical 64-char SHA-256 fingerprint
	// (PR-VO-TEXTHASH-64, August 2026). Both the per-batch and
	// per-item paths now supply the same 64-char hash.
	TextHash string
	Text     string // Full text; truncated to 100 chars for preview column
	// Language is the typed BCP-47 envelope (voiceover.Language)
	// per PR-VO-TYPED-PRIMITIVES — wire byte-equivalent.
	Language Language
	Voice    string
	Filename string
	Strategy string
	MetaJSON []byte // Canonical JSON metadata map

	// Audio Asset State
	LocalPath       string
	CleanedPath     string
	LegacyFileMD5   string // Index outbox skipped when empty
	DurationSeconds float64

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
//	(a) Optional step that was guard-skipped because of a data-state
//	    reason (e.g. dedupe gate not triggered because DriveFileID is
//	    empty) — recordable, OPERATOR-ACTIONABLE.
//	(b) Required production-path step that was unwired at composition
//	    time (e.g. LifecycleService nil) — fatal wiring error that
//	    MUST propagate as a Go error.
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
	//   StateGuarded      — "index_outbox: guarded (empty LegacyFileMD5)"
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
//
// PR-ASSET-COMMITTER-COMMITASSET (July 2026): Committer is the
// CANONICAL producer of the media_assets projection + asset.index.requested
// outbox event. When wired, the finalizer routes Step 4 + Step 5
// through Committer.CommitTx in a single atomic write inside the
// caller's tx. The legacy LifecycleService + Outbox deps remain
// for backward compat (pre-Cutover callers) — Step 4 + Step 5
// prefer Committer when present and fall back to the legacy ports
// otherwise (see finalizer_execute.go).
type voiceoverFinalizerDeps struct {
	VoiceoverRepo    persistence.Repository           // mandatory
	Outbox           TxOutboxEnqueuer                 // nil-safe (skip index + cleanup)
	LifecycleService LifecycleProjectionUpserter      // nil-safe (skip media_assets, pre-Cutover fallback)
	Committer        assetspersistence.AssetCommitter // PR-ASSET-COMMITTER: nil-safe; when wired, replaces LifecycleService+Outbox for the media_assets + outbox path
	Logger           *zap.Logger                      // nil-safe via zap.NewNop()
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
	ID            string
	Source        string
	Name          string
	Filename      string
	FolderID      string
	FolderPath    string
	MediaType     string
	LocalPath     string
	DriveFileID   string
	DriveLink     string
	DownloadLink  string
	LegacyFileMD5 string
	// Language is the typed BCP-47 envelope (voiceover.Language)
	// per PR-VO-TYPED-PRIMITIVES. The cross-package seam at
	// internal/app/adapters_voiceover_use_case.go converts to
	// the raw string when forwarding to lifecycle.VoiceoverProjectionInput.
	Language Language
	Status   string
	Metadata string
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
//     — nil-safe (skip outbox steps when nil). Pre-Cutover fallback
//     for Step 5 (index outbox) when Committer is unwired.
//   - lifecycleSvc: LifecycleProjectionUpserter (UpsertVoiceoverProjectionTx)
//     — nil-safe (skip media_assets projection when nil). Pre-Cutover
//     fallback for Step 4 when Committer is unwired.
//   - committer: persistence.AssetCommitter — nil-safe. When wired,
//     Step 4 (media_assets projection) + Step 5 (asset.index.requested
//     outbox) are produced by Committer.CommitTx in a single atomic
//     write inside the caller's tx. PR-ASSET-COMMITTER-COMMITASSET.
//   - log: *zap.Logger — nil-safe (zap.NewNop() fallback).
//
// Returns VoiceoverFinalizer so the composition root can inject an
// interface — test doubles swap the concrete without churn.
func NewVoiceoverFinalizer(
	voRepo persistence.Repository,
	outbox TxOutboxEnqueuer,
	lifecycleSvc LifecycleProjectionUpserter,
	committer assetspersistence.AssetCommitter,
	log *zap.Logger,
) VoiceoverFinalizer {
	return newVoiceoverFinalizer(voiceoverFinalizerDeps{
		VoiceoverRepo:    voRepo,
		Outbox:           outbox,
		LifecycleService: lifecycleSvc,
		Committer:        committer,
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
	//
	// PR-VO-FINALIZER-STEP6-EXTRACT (P0 #3 in VO-DECOMPOSITION-2026-07-04,
	// deadline 2026-08-01): requiredStepCleanupOutbox moved to
	// finalizer_cleanup_outbox.go so the cleanup outbox step is
	// owned by the file that implements it (godlike/06 SSOT).
	requiredStepMediaAssetsProjection = "media_assets_projection"
	requiredStepIndexOutbox           = "index_outbox"
	// requiredStepCleanupOutbox REMOVED — moved to finalizer_cleanup_outbox.go

	// Execution-state markers appended to RequiredSteps on a
	// successful Finalize. "executed" means the deps were wired AND
	// the step ran. "guarded (...)" means the deps were wired BUT
	// a data-state guard prevented execution (e.g. empty LegacyFileMD5,
	// ShouldSwap=false). State markers are NOT surfaced on
	// wire-format (JSON) consumers; callers parsing internal log
	// lines or programmatic step-state assertions should treat
	// them as the <=256-byte canonical strings below. Renaming is
	// HOW a downstream audit pins unannounced drift.
	requiredStateExecuted = "executed"
	requiredStateGuarded  = "guarded"
)

// ── Companion files ──
//
//   - finalizer_execute.go        Finalize() 6-step method + formatRequiredState
//   - finalizer_cleanup_outbox.go  executeCleanupOutboxStep (Step 6, P0 #3)
