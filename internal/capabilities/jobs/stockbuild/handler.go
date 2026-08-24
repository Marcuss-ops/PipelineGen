// Package stockbuild — handler.go (P0-2 stock-pipeline refactor, July 2026).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// youtube.stock.build.v1 handler implementation. Every broker claim
// routes here via the kerneljob.Handler signature. The 8-phase
// iteration (SEARCH→SELECT→DOWNLOAD→EXTRACT→UPLOAD→PERSIST→INDEX→
// VERIFY) is the canonical workflow — a prior run that crashed
// mid-flight resumes from the first non-Completed phase via the
// steps.Store ledger (Stock Cutover §12-3 Design A — per-row
// canonical semantics).
//
// godlike/07 NO-FAKE-AVAILABILITY: typed-sentinel errors ONLY —
// the handler never silent-successes on:
//   - Subject resolution failure (subjects.ErrSubjectNotFound).
//   - Payload validation failure (ErrInvalidPayload).
//   - Steps.Store backend unavailable (steps.ErrStoreNotWired).
//   - Phase body failure (runtime error from the underlying primitive).
//
// All such failures route through Result envelope with
// `{where: <step_key>, err: <message>, counts: <so_far>}` so the
// broker can audit the exact failure mode.
package stockbuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/subjects"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ─── Phase Catalog ──────────────────────────────────────────────────────────

// PhaseName is the canonical 8-phase machine. Each name is also the
// stepKey used for `steps.StepKey.StepKey`. The lexically-sortable
// invariant from steps.store.go is preserved by the leading numeric
// prefix; a reordering in this list MUST keep the prefix ordering
// intact or resume semantics break (a re-run would resume in the
// wrong phase).
type PhaseName string

const (
	PhaseSearch   PhaseName = "01_search"
	PhaseSelect   PhaseName = "02_select"
	PhaseDownload PhaseName = "03_download"
	PhaseExtract  PhaseName = "04_extract"
	PhaseUpload   PhaseName = "05_upload"
	PhasePersist  PhaseName = "06_persist"
	PhaseIndex    PhaseName = "07_index"
	PhaseVerify   PhaseName = "08_verify"
)

// AllPhases returns the canonical 8-phase order. Iteration MUST use
// this slice (not a runtime-sorted one) so resume semantics stay
// stable across schema changes.
func AllPhases() []PhaseName {
	return []PhaseName{
		PhaseSearch, PhaseSelect, PhaseDownload, PhaseExtract,
		PhaseUpload, PhasePersist, PhaseIndex, PhaseVerify,
	}
}

// ─── Phase Fingerprint ───────────────────────────────────────────────────────

// PhaseFingerprint is the canonical per-phase input fingerprint stored
// on `steps.StepKey.InputFingerprint`. A retried phase with the same
// fingerprint reuses the same row (MarkStarted is idempotent); a
// retried phase with a DIFFERENT fingerprint INSERTs a new row
// (Design A version-history audit). For this orchestrator, the
// fingerprint binds phase-specific schema inputs:
//
//   - SEARCH fingerprint = sha256(slug || "search" || target.videos)
//   - SELECT fingerprint = sha256(slug || "select" || category-counts)
//   - DOWNLOAD fingerprint = sha256(slug || "download" || source-urls)
//   - …
//
// Each phase's fingerprint may evolve; the per-row "latest" lookup
// in steps.FirstNonCompleted selects the most recent attempt's row.
func PhaseFingerprint(runID string, phase PhaseName, phaseSpecificInput string) string {
	return fmt.Sprintf("%s|%s|%s", runID, phase, phaseSpecificInput)
}

// ─── Phase Body Contract ────────────────────────────────────────────────────

// PhaseBody is the contract every phase's Run body must implement.
// The 8 concrete bodies live in phases_*.go siblings (P1 follow-up)
// — this PR ships the canonical Handler scaffolding + the typed
// RunNotImplemented body so the broker integration is testable
// without leaking binary half-implementations.
//
// godlike/07 NO-FAKE-AVAILABILITY: a PhaseBody that returns nil for
// a phase MUST be replaced by a real call to the underlying
// primitive (planner, stager, cutter, renderer, publisher,
// finalizer). A nil-returning stub is the canonical silent-success
// anti-pattern and is forbidden — return RunNotImplemented instead.
type PhaseBody interface {
	// Run executes the phase body and returns either:
	//   - nil error on success.
	//   - A typed error on failure (godlike/07 typed-error contract).
	Run(ctx context.Context, input PhaseInput) error
} // PhaseInput is what each phase body receives. The handler builds this
// from the resolved Subject + Payload + the previous phase's
// checkpoint result_json.
type PhaseInput struct {
	RunID   string
	Subject *asset.Subject // canonical kernel/asset.Subject (godlike/06 SSOT — sole type owner)
	Payload Payload
	// PrevResult is the JSON-decoded prior-phase checkpoint for
	// downstream consumption. Empty for phase 01_search.
	PrevResult json.RawMessage
}

// ─── Canonical Result Envelope ──────────────────────────────────────────────

// ResultEnvelope is the canonical Result returned by the handler.
// Today `job.Result` is a typed alias of `map[string]any` (kernel back-
// compat — godlike/06 SSOT), so the envelope is constructed as a
// map[string]any at the call site and decoded into ResultEnvelope
// for tests + humans.
//
// The envelope shape is:
//   - success: {"run_id":"...", "subject_id":"<uuid>",
//     "status":"COMPLETE", "counts": {...}}
//   - failure: {"run_id":"...", "subject_id":"<uuid>",
//     "where":"<step_key>", "status":"FAILED",
//     "err":"<message>", "counts": {...}}
type ResultEnvelope struct {
	RunID     string         `json:"run_id"`
	SubjectID string         `json:"subject_id"`
	Status    string         `json:"status"`
	Where     string         `json:"where,omitempty"`
	Err       string         `json:"err,omitempty"`
	Counts    map[string]int `json:"counts"`
}

// ─── Handler ─────────────────────────────────────────────────────────────────

// Handler is the canonical youtube.stock.build.v1 JobHandler.
//
// Required deps (composition-root fail-closed contract — godlike/06
// SSOT):
//   - subjectsResolver: the canonical subjects.Resolver. nil ⇒
//     ErrResolverNotWired at Handle time (composition failed
//     silently).
//   - stepsStore: the canonical steps.Store. nil ⇒
//     ErrStepsStoreNotWired at Handle time.
//   - phases: 8 phase bodies, in canonical PhaseName order.
//     Mismatch length / order ⇒ ErrPhasesMalformed at Handle time.
//
// fail-closed at construction: NewHandler returns (nil, err) if any
// of subjectsResolver / stepsStore is nil or phases is malformed.
// The composition-root wiring site in `internal/app/wiring/`
// must reject any such partial construction.
type Handler struct {
	log              *zap.Logger
	subjectsResolver subjects.Resolver
	stepsStore       steps.Store
	phases           map[PhaseName]PhaseBody
}

// NewHandler constructs the canonical Handler. Returns (nil, err) on
// any fail-closed condition (composition bug).
func NewHandler(
	log *zap.Logger,
	subjectsResolver subjects.Resolver,
	stepsStore steps.Store,
	phases map[PhaseName]PhaseBody,
) (*Handler, error) {
	if subjectsResolver == nil {
		return nil, ErrResolverNotWired
	}
	if stepsStore == nil {
		return nil, ErrStepsStoreNotWired
	}
	if len(phases) != len(AllPhases()) {
		return nil, fmt.Errorf("%w: supplied %d phases, expected %d",
			ErrPhasesMalformed, len(phases), len(AllPhases()))
	}
	for _, expected := range AllPhases() {
		if _, ok := phases[expected]; !ok {
			return nil, fmt.Errorf("%w: phase %q missing",
				ErrPhasesMalformed, expected)
		}
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		log:              log,
		subjectsResolver: subjectsResolver,
		stepsStore:       stepsStore,
		phases:           phases,
	}, nil
}

// Handle is the canonical JobHandler implementation per kernel/job.Handler
// signature. The 8-phase iteration is the canonical owner of the
// SEARCH→SELECT→DOWNLOAD→…→VERIFY workflow; a prior run that crashed
// mid-flight resumes from the first non-Completed phase via
// steps.FirstNonCompleted semantics.
//
// Fail-closed contract: any error propagates as Result envelope
// {where,err,counts} so the broker + audit trail can locate the
// failure mode. The session NEVER returns nil error with a partial
// Result envelope (godlike/07).
func (h *Handler) Handle(ctx context.Context, j *job.Job, tools *job.JobExecutionTools) (job.Result, error) {
	// ── 1. Decode payload ───────────────────────────────────────────
	var payload Payload
	if err := decodePayload(j.Payload, &payload); err != nil {
		return h.failResult(ctx, j, "00_decode", err, nil)
	}
	if err := payload.Validate(); err != nil {
		return h.failResult(ctx, j, "00_validate", err, nil)
	}

	// ── 2. Resolve subject (godlike/06 SSOT — sole canonical owner) ──
	subject, err := h.subjectsResolver.LookupOrCreate(ctx, payload.Subject.DisplayName)
	if err != nil {
		return h.failResult(ctx, j, "00_resolve", err, nil)
	}

	// ── 3. Derive Run ID (deterministic, per-subject+target) ────────
	runID := DeriveRunID(subject.Slug, payload)
	h.log.Info("stockbuild: starting run",
		zap.String("run_id", runID),
		zap.String("subject_id", subject.UUID),
		zap.String("display_name", subject.DisplayName),
	)
	if tools != nil && tools.Event != nil {
		tools.Event("stockbuild.started", runID, map[string]any{
			"subject_id":   subject.UUID,
			"subject_slug": subject.Slug,
		})
	}

	// ── 4. Iterate 8 phases via steps.Store ──────────────────────────
	counts := make(map[string]int, 8)
	var prevResult json.RawMessage
	for _, phaseName := range AllPhases() {
		select {
		case <-ctx.Done():
			return h.failAtPhase(ctx, j, runID, subject.UUID, phaseName,
				fmt.Errorf("stockbuild: ctx cancelled before %s: %w", phaseName, ctx.Err()),
				counts)
		default:
		}

		body := h.phases[phaseName]
		fp := PhaseFingerprint(runID, phaseName, phaseSpecificInput(phaseName, payload))

		stepKey := steps.StepKey{
			JobID:            runID,
			StepKey:          string(phaseName),
			InputFingerprint: fp,
		}

		// MarkStarted: idempotent re-call bumps attempt if prior
		// non-Completed; ErrStepAlreadyCompleted if prior Completed
		// → skip the phase, restore the prior row's result.
		startErr := h.stepsStore.MarkStarted(ctx, stepKey)
		if errors.Is(startErr, steps.ErrStepAlreadyCompleted) {
			h.log.Info("stockbuild: skip already-Completed phase (resume)",
				zap.String("run_id", runID), zap.String("phase", string(phaseName)))
			prevResult = h.loadPrevResult(ctx, stepKey)
			continue
		}
		if startErr != nil {
			return h.failAtPhase(ctx, j, runID, subject.UUID, phaseName, startErr, counts)
		}

		// Run the phase body via the PhaseBody contract.
		phaseInput := PhaseInput{
			RunID:      runID,
			Subject:    subject,
			Payload:    payload,
			PrevResult: prevResult,
		}
		phaseStart := nowFn()
		runErr := body.Run(ctx, phaseInput)
		counts[string(phaseName)]++

		if runErr != nil {
			// MarkFailed is best-effort: godlike/07 propagates the
			// typed error to the caller regardless.
			if mfErr := h.stepsStore.MarkFailed(ctx, stepKey, runErr.Error()); mfErr != nil {
				h.log.Warn("stockbuild: MarkFailed lost",
					zap.String("run_id", runID),
					zap.String("phase", string(phaseName)),
					zap.Error(mfErr))
			}
			return h.failAtPhase(ctx, j, runID, subject.UUID, phaseName, runErr, counts)
		}

		// MarkCompleted: pass the per-phase counts as result_json so
		// resume can rehydrate the accumulator if a later step fails.
		resultJSON, marshalErr := json.Marshal(map[string]any{
			"counts":           counts,
			"phase_elapsed_ms": sinceMs(phaseStart),
		})
		if marshalErr != nil {
			return h.failAtPhase(ctx, j, runID, subject.UUID, phaseName,
				fmt.Errorf("stockbuild: checkpoint marshal: %w", marshalErr), counts)
		}
		if mcErr := h.stepsStore.MarkCompleted(ctx, stepKey, resultJSON, nil); mcErr != nil {
			return h.failAtPhase(ctx, j, runID, subject.UUID, phaseName, mcErr, counts)
		}
		prevResult = resultJSON
	}

	// ── 5. Success envelope ──────────────────────────────────────────
	successEnv := ResultEnvelope{
		RunID:     runID,
		SubjectID: subject.UUID,
		Status:    StatusComplete,
		Counts:    counts,
	}
	if tools != nil && tools.Event != nil {
		tools.Event("stockbuild.completed", runID, map[string]any{"counts": counts})
	}
	h.log.Info("stockbuild: COMPLETE",
		zap.String("run_id", runID), zap.Any("counts", counts))
	return job.Result(successEnv.AsMap()), nil
}

// ─── Failure helpers ────────────────────────────────────────────────────────

// failResult emits a "00_*" prefix-status failure (payload decode,
// validation, subject resolution). The run_id may not yet exist;
// surface the typed error verbatim so the caller can dispatch to
// the broker's audit trail.
func (h *Handler) failResult(_ context.Context, j *job.Job, where string, err error, _ []int) (job.Result, error) {
	h.log.Error("stockbuild: pre-run failure",
		zap.String("where", where), zap.Error(err))
	env := ResultEnvelope{
		Status: StatusFailed,
		Where:  where,
		Err:    err.Error(),
	}
	if j != nil {
		env.RunID = j.ID
	}
	return job.Result(env.AsMap()), err
}

// failAtPhase emits a per-phase failure envelope with the so-far
// counts so the broker + operator can audit exact progress.
func (h *Handler) failAtPhase(_ context.Context, j *job.Job, runID, subjectID string, phase PhaseName, err error, counts map[string]int) (job.Result, error) {
	h.log.Error("stockbuild: phase failure",
		zap.String("run_id", runID),
		zap.String("phase", string(phase)),
		zap.Any("counts", counts),
		zap.Error(err))
	env := ResultEnvelope{
		RunID:     runID,
		SubjectID: subjectID,
		Status:    StatusFailed,
		Where:     string(phase),
		Err:       err.Error(),
		Counts:    counts,
	}
	if j != nil {
		env.RunID = runID // runID wins over broker job.ID for clarity at this point
	}
	return job.Result(env.AsMap()), err
}

// loadPrevResult rehydrates the result_json from the latest Completed
// row in the steps.Store. Used by the resume path to feed downstream
// phases with prior phase state.
func (h *Handler) loadPrevResult(ctx context.Context, key steps.StepKey) json.RawMessage {
	history, err := h.stepsStore.ListByJob(ctx, key.JobID)
	if err != nil {
		return nil
	}
	for _, row := range history {
		if row.StepKey != key.StepKey || row.Status != steps.StatusCompleted {
			continue
		}
		return row.Result
	}
	return nil
}

// ─── Map-Codec (godlike/06 typed-alias back-compat) ────────────────────────

// AsMap renders the ResultEnvelope as a map[string]any — the
// canvas shape produced by job.Result (godlike/06 typed-alias).
func (e ResultEnvelope) AsMap() map[string]any {
	out := map[string]any{
		"run_id":     e.RunID,
		"subject_id": e.SubjectID,
		"status":     e.Status,
		"counts":     e.Counts,
	}
	if e.Where != "" {
		out["where"] = e.Where
	}
	if e.Err != "" {
		out["err"] = e.Err
	}
	return out
}
