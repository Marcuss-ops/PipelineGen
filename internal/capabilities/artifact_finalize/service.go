// Package artifact_finalize — service.go (FASE 3 / Push 3.1d, July 2026).
//
// finalizerService is the canonical concrete for the Finalizer
// port (finalizer.go in this package). It performs the FASE 3
// (d) "verify all PUBLISHED → SUCCEEDED" step:
//
//  1. Read all artifact_stages rows for the given job_id via
//     Repository.ListByJob (the canonical scan helper, ordered
//     by created_at ASC, uses the idx_artifact_stages_job_state
//     composite index).
//
//  2. Apply a job-level readiness gate: every REQUIRED row MUST
//     be in PUBLISHED state. A REQUIRED row in STAGED /
//     FAILED_PERMANENT trips ErrArtifactRequiredMissing
//     (wrapped with job_id + the FIRST missing artifact id; the
//     rest of the missing ids join the wrap as a comma-
//     delimited list). FASE 3 (b) "richiesto mancante ⇒ errore,
//     mai warning".
//
//  3. Flip every PUBLISHED row (REQUIRED + OPTIONAL) to
//     SUCCEEDED via Repository.MarkSucceeded. The fenced CAS
//     rejects already-terminal rows with
//     ErrTerminalStateRejection; the finalizer swallows this
//     specific sentinel (idempotent re-flip case) and continues
//     with the remaining rows. Other infrastructural errors
//     abort and propagate.
//
// godlike/06 SSOT: this is the SINGLE canonical application-
// layer Service for FASE 3 finalization. The composition root
// (internal/app/build_bundles_artifact_finalize.go) instantiates
// exactly one instance and exposes it via the application.
// FinalizerBundle on ComposeRoot.
//
// godlike/07 fail-closed:
//   - Pre-flight Validate (godlike/07): empty JobID trips a
//     typed error BEFORE the Repository scan — caller-supplied
//     empty JobID is a programming error, not a recoverable
//     condition.
//   - Repository errors: ctx cancellation, DB timeout, and other
//     infrastructural failures propagate to the caller unmodified
//     (wrapped with the operation + id context for log-greppers).
//     ErrTerminalStateRejection is the ONLY sentinel that gets
//     swallowed (idempotent case; see above).
//   - Counter integrity: the finalizer's counter arithmetic is
//     self-consistent (Scanned == RequiredTotal + Optional*
//   - RequiredAlreadySucceeded — the latter is implicit, NOT
//     exposed as a counter to keep the public surface minimal).
package artifact_finalize

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// Compile-time assertion: *finalizerService satisfies the
// Finalizer port (finalizer.go in this package). The canonical
// Single-Writer-Of-The-Finalizer-Port anchor.
var _ Finalizer = (*finalizerService)(nil)

// finalizerService is the canonical concrete for Finalizer.
// The struct is private because NewFinalizerService is the
// SOLE public construction site (godlike/06): callers reach
// the typed port via the Finalizer interface field on the
// FinalizerBundle.
type finalizerService struct {
	// repo is the artifact.ArtifactStageRepository port (single-writer for
	// the artifact_stages table per Push 3.1a SSOT). The
	// finalizer consumes the 3 read paths (ListByJob, indirect
	// via the scan) + the MarkSucceeded fenced-CAS path. Later
	// pushes may add ListByState for batch invocations, but the
	// per-job API stays canonical.
	repo artifact.ArtifactStageRepository

	// log is *zap.Logger for the Info-level "finalize completed"
	// audit line + the Debug-level "no stages for job_id" trace.
	log *zap.Logger
}

// NewFinalizerService constructs the canonical FASE 3
// finalizer service. Caller MUST supply non-nil repo + non-nil
// log (godlike/07 fail-fast at construction; zero-value log
// produces no log lines, which defeats the audit-trail
// contract).
func NewFinalizerService(repo artifact.ArtifactStageRepository, log *zap.Logger) (*finalizerService, error) {
	if repo == nil {
		return nil, fmt.Errorf("artifact_finalize.NewFinalizerService: repo is required")
	}
	if log == nil {
		return nil, fmt.Errorf("artifact_finalize.NewFinalizerService: log is required (zero-value log defeats the audit-trail contract)")
	}
	return &finalizerService{repo: repo, log: log}, nil
}

// Finalize performs the FASE 3 (d) "verify all PUBLISHED →
// SUCCEEDED" check for the given job_id. See Finalizer.Finalize
// (finalizer.go in this package) for the full behaviour contract.
//
// Implementation outline:
//  1. Validate JobID is non-empty (fail-fast at the boundary).
//  2. ListByJob → scan all artifact_stages rows for the job.
//     Empty result = no-op (zero counters, nil error).
//  3. Walk the rows once; partition into REQUIRED (must be
//     PUBLISHED) and OPTIONAL (telemetry-only). Track missing /
//     failed required rows.
//  4. Apply the readiness gate: any REQUIRED row NOT in
//     PUBLISHED trips ErrArtifactRequiredMissing.
//  5. Walk the eligible PUBLISHED rows; call MarkSucceeded.
//     Swallow ErrTerminalStateRejection (idempotent re-flip);
//     abort on any other error.
//  6. Emit the audit log line.
func (s *finalizerService) Finalize(ctx context.Context, jobID string) (*FinalizeResult, error) {
	// Step 1: pre-flight validate JobID.
	if strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("artifact_finalize.Finalize: jobID is required (caller must supply a non-empty canonical job_id)")
	}

	// Step 2: scan all artifact_stages rows for this job.
	// Allocate the FinalizeResult FIRST so infra-cancellation
	// errors (ctx canceled, DB timeout) return the populated
	// (zero-counter) envelope instead of nil — uniform caller
	// observability (godlike/07 fail-closed contract: every
	// error path returns a typed error AND a non-nil result
	// where the work has any observability value). The empty-
	// JobID branch above still returns nil (validation error;
	// no work attempted).
	result := &FinalizeResult{JobID: jobID}
	stages, err := s.repo.ListByJob(ctx, jobID)
	if err != nil {
		return result, fmt.Errorf("artifact_finalize.Finalize: ListByJob (job_id=%s): %w", jobID, err)
	}
	result.Scanned = len(stages)

	// Empty-job no-op: a job with zero stages has nothing to
	// finalize. The Debug log line makes the trace visible
	// without consuming a typed sentinel for a non-actionable
	// condition.
	if len(stages) == 0 {
		s.log.Debug("finalize: no stages found for job_id (no-op)",
			zap.String("job_id", jobID),
		)
		return result, nil
	}

	// Step 3: single-pass partition. allocate eligible slices
	// with len(stages) capacity to avoid re-alloc on
	// required-heavy + optional-empty jobs.
	//
	// Counter accounting (godlike/06 SSOT): each stage falls
	// into exactly ONE branch of the outer if (REQUIRED vs
	// OPTIONAL) AND exactly ONE case of the inner switch
	// (4 canonical states: STAGED/PUBLISHED/SUCCEEDED/
	// FAILED_PERMANENT). The exposed counters map 1:1 to the
	// non-trivial branches:
	//
	//   Scanned             = total rows
	//   RequiredTotal       = REQUIRED branches sum (incl.
	//                         REQUIRED + SUCCEEDED on re-entry)
	//   OptionalFailed      = OPTIONAL + FAILED_PERMANENT
	//   OptionalStillStaged = OPTIONAL + STAGED
	//   FlippedToSucceeded  = eligible.len (PUBLISHED rows that
	//                         actually transitioned THIS call)
	//
	// Sum invariant (verified by service_test.go):
	//   Scanned == RequiredTotal + OptionalFailed + OptionalStillStaged + eligible.len
	//
	// Hidden contributions to Scanned without a dedicated
	// counter (intentional — the FinalizeResult surface stays
	// minimal):
	//   - REQUIRED + SUCCEEDED on entry: counted in
	//     RequiredTotal (idempotent re-finalize); contributes 0
	//     to FlippedToSucceeded.
	//   - OPTIONAL + SUCCEEDED on entry: counted in Scanned
	//     ONLY (no other exposed counter).
	//
	// CRITICAL: `eligible` contains ONLY PUBLISHED rows
	// (REQUIRED+PUBLISHED + OPTIONAL+PUBLISHED). The
	// SUCCEEDED-on-entry branches (both REQUIRED + OPTIONAL)
	// skip silently WITHOUT appending to `eligible` — this is
	// what makes the sum invariant Scanned == ... + eligible.len
	// exactly equal (no eligible-already-succeeded term to
	// subtract).
	eligible := make([]artifact.ArtifactStage, 0, len(stages))
	var requiredMissing []string
	for _, st := range stages {
		// Gate on Requirement first — REQUIRED rows have a
		// blocking failure mode; OPTIONAL rows are pure
		// telemetry counters.
		if st.Requirement != artifact.RequirementRequired {
			switch st.State {
			case artifact.ArtifactStageStateStaged:
				result.OptionalStillStaged++
			case artifact.ArtifactStageStateFailedPermanent:
				result.OptionalFailed++
			case artifact.ArtifactStageStatePublished:
				eligible = append(eligible, st)
			case artifact.ArtifactStageStateSucceeded:
				// Already finalised on a previous Finalize
				// invocation. Skip silently (idempotent
				// re-finalize); no counter needed (Scanned
				// already reflects the row's presence).
			}
			continue
		}
		// REQUIRED path.
		result.RequiredTotal++
		switch st.State {
		case artifact.ArtifactStageStatePublished:
			eligible = append(eligible, st)
		case artifact.ArtifactStageStateSucceeded:
			// Already finalised on a previous Finalize
			// invocation. Skip silently; the row is counted
			// in RequiredTotal (telemetry reflects the input
			// shape, not the action taken).
		case artifact.ArtifactStageStateStaged, artifact.ArtifactStageStateFailedPermanent:
			requiredMissing = append(requiredMissing, st.ID)
		}
	}

	// Step 4: job-level readiness gate. FASE 3 (b) fail-closed:
	// any missing required artifact trips ErrArtifactRequiredMissing.
	if len(requiredMissing) > 0 {
		// Wrap with the FIRST missing id (canonical
		// errors.Is probe) + the comma-delimited full list
		// (operator-audit log line). The split keeps the
		// sentinel probe single-sourced and adds the
		// remaining ids as informational context.
		wrap := artifact.WrapArtifactRequiredMissing(jobID, "required", requiredMissing[0])
		if len(requiredMissing) > 1 {
			return result, fmt.Errorf("%w; additional missing required ids: [%s]",
				wrap, strings.Join(requiredMissing[1:], ","))
		}
		return result, wrap
	}

	// Step 5: MarkSucceeded for each eligible PUBLISHED row.
	for _, st := range eligible {
		if err := s.repo.MarkSucceeded(ctx, st.ID); err != nil {
			if errors.Is(err, artifact.ErrTerminalStateRejection) {
				// Concurrent re-finalize: another Finalize
				// invocation already flipped this row to
				// SUCCEEDED. The fenced CAS rejection is
				// the canonical idempotency signal — count
				// it as a no-op and continue with the
				// remaining rows.
				s.log.Debug("finalize: MarkSucceeded idempotent re-flip (ErrTerminalStateRejection swallowed)",
					zap.String("job_id", jobID),
					zap.String("stage_id", st.ID),
				)
				continue
			}
			// Infrastructural error (DB timeout, ctx
			// cancellation, etc.) — propagate to the caller.
			return result, fmt.Errorf("artifact_finalize.Finalize: MarkSucceeded (job_id=%s id=%s): %w", jobID, st.ID, err)
		}
		result.FlippedToSucceeded++
	}

	// Step 6: audit log line. Info level so a finished saga
	// shows up in the operator's logs; the structured fields
	// let dashboards aggregate per-job finalize outcomes.
	s.log.Info("finalize completed",
		zap.String("job_id", jobID),
		zap.Int("scanned", result.Scanned),
		zap.Int("required_total", result.RequiredTotal),
		zap.Int("flipped_to_succeeded", result.FlippedToSucceeded),
		zap.Int("optional_failed", result.OptionalFailed),
		zap.Int("optional_still_staged", result.OptionalStillStaged),
	)

	return result, nil
}
