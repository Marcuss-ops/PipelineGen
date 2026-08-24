// Package jobs — worker_execution_result.go (worker_execution.go
// split, July 2026).
//
// Result finalisation extracted from worker_execution.go. Owns:
//
//  1. func (w *Worker) finalizeJob — the per-job finalisation
//     pipeline that consumes (result, dispatchErr) from the
//     dispatcher and routes through the 4 terminal-state paths:
//     ScheduleRetry (retryable, RetryCount < MaxRetries),
//     Fail + DeadLetter (exhausted retries),
//     CompleteWithArtifacts (artifact-producing, gated by the
//     typed CompletionPort per PR-WORKER-RUNNER-INPROCESS-MIGRATION),
//     Complete (legacy non-artifact jobs). Lease-loss handling
//     and the typed-error contracts godlike/07 fail-closed
//     live here.
//
// July 2026 sub-section split (this file → orchestrator only):
//
//   - worker_finalize_paths.go owns the three terminal-state path
//     helpers called by the orchestrator: finalizeJobDispatchError
//     (retry / fail+dead-letter), finalizeJobArtifactPath (typed
//     CompletionPort with manifest extraction + fail-closed gates),
//     finalizeJobLegacyComplete (legacy non-artifact completion).
//
//   - worker_artifact_manifest.go owns extractStagedArtifacts +
//     destinationForArtifactKind — the handler-result → broker
//     StagedArtifacts JSON conversion with the FASE 1 ordering pin
//     (decode → nil-check → empty-envelope → validate → process).
//     The ordering is regression-locked by 4 tests:
//     TestExtractStagedArtifacts_EmptyArtifactsList,
//     TestExtractStagedArtifacts_DecodeFailure_TypedSentinel,
//     TestExtractStagedArtifacts_ValidateFailure_TypedSentinel,
//     TestExtractStagedArtifacts_RequiredMissingPath_ErrRequiredArtifactMissing.
//     Any reorder silently lets a malformed manifest reach
//     SUCCEEDED — DO NOT touch the if-cascade byte-for-byte.
//
// CRITICAL: this file's finalizeJob receives the AGENTS.md
// allowlisted `context.WithTimeout(context.Background(),
// finalizationTimeout)` ctx from worker_execution.go::runJob's
// envelope. DO NOT replace it with the wrapped jobCtx — that
// would lose outcome persistence when jobCtx is cancelled by
// timeout or worker Stop.
//
// Mechanical split from worker_execution.go. Zero behavior
// change. The pre-split PR7 invariant — finalizationCtx is
// detached from jobCtx so the DB final-state write survives
// jobCtx cancellation — is preserved by the runJob envelope
// passing the already-constructed finalizationCtx into
// finalizeJob as the ctx parameter; finalizeJob does NOT
// reconstruct it.
package jobs

import (
	"context"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// finalizeJob consolidates the 4 finalisation paths previously inlined
// at the bottom of worker_execution.go::runJob. It receives the
// AGENTS.md allowlisted finalizationCtx (the caller's
// `context.WithTimeout(context.Background(), finalizationTimeout)`) so
// the terminal writes — the DB flip AND the artifact-publication spine
// (CompleteWithArtifacts, which publishes every staged artifact to
// Drive) — can complete even when the worker jobCtx has been cancelled
// by timeout or worker Stop.
//
// Transition table:
//
//	dispatchErr != nil
//	  └─ j.RetryCount < j.MaxRetries  → ScheduleRetry (server-side backoff
//	                                    via available_at; no intermediate
//	                                    "failed" state to avoid false
//	                                    alerting)
//	  └─ j.RetryCount >= j.MaxRetries → Fail + DeadLetter (terminal)
//
//	dispatchErr == nil
//	  └─ ProducesArtifacts (reg.ProducesArtifacts) true
//	       └─ w.broker == nil             → Fail-closed (godlike/07 — flag
//	                                         the composition miss; NO silent
//	                                         fallback to legacy path)
//	       └─ extractStagedArtifacts err  → Fail + DeadLetter (FASE 1 c
//	                                         typed-error contract: malformed
//	                                         manifest MUST NOT silently reach
//	                                         SUCCEEDED)
//	       └─ broker.CompleteWithArtifacts err → Fail + DeadLetter
//	                                            (PR-COMPLETE-WORKER-BROAD-FIX
//	                                             closure: pre-PR code silently
//	                                             logged + returned, leaving
//	                                             the job RUNNING forever)
//	       └─ success                     → log "job completed with artifacts"
//	  └─ ProducesArtifacts false
//	       └─ w.repo.Complete err
//	            └─ job.ErrLeaseLost                 → log warn
//	            └─ domainremote.ErrCompleteJobPathViolation → Fail (FASE 0.1:
//	                                                          legacy Worker path
//	                                                          cannot complete
//	                                                          artifact production)
//	            └─ other                           → log error
//	       └─ success                             → log "job completed"
//
// All `errors.Is(err, job.ErrLeaseLost)` branches log warn rather than
// re-raise; a lease-loss during a finalisation SQL UPDATE means another
// worker has already CAS-won the row, so the next worker's transition
// is the authoritative one.
func (w *Worker) finalizeJob(ctx context.Context, j *job.Job, result map[string]any, dispatchErr error) []string {
	// Refresh revision from the DB so the final CAS write carries the
	// latest expected revision (a concurrent Update during execution
	// would invalidate the snapshot copied at ClaimNext).
	finalRevision := j.Revision
	if jFresh, err := w.repo.Get(ctx, j.ID); err == nil && jFresh != nil {
		// Artifact-producing handlers such as Stock already use the
		// transactional finalizer, which atomically writes SUCCEEDED.
		// Do not send a second lease-fenced completion after that commit;
		// the lease is intentionally cleared by the canonical finalizer.
		if jFresh.Status == job.StatusSucceeded {
			return nil
		}
		if jFresh.Revision > 0 {
			finalRevision = jFresh.Revision
		}
	}

	workerID := w.id
	leaseID := j.LeaseID

	// Terminal-state path dispatch (July 2026 sub-section split — the
	// three path bodies live in worker_finalize_paths.go). Order and
	// semantics identical to the pre-split flat body: dispatch error
	// first (retry / fail+dead-letter), then artifact-producing via
	// the typed CompletionPort, then the legacy non-artifact Complete.
	if dispatchErr != nil {
		w.finalizeJobDispatchError(ctx, j, workerID, leaseID, finalRevision, dispatchErr)
		return nil
	}

	// PR-WORKER-RUNNER-INPROCESS-MIGRATION (July 2026): artifact-
	// producing jobs MUST be completed via the typed CompletionPort
	// (broker.CompleteWithArtifacts) — NOT the legacy w.repo.Complete
	// path. The SQL-layer gate at
	// internal/platform/sqlite/jobs/repository_lifecycle.go:115
	// returns the typed sentinel domainremote.ErrCompleteJobPathViolation
	// for artifact-producing jobs that attempt the legacy path.
	// godlike/06 SSOT: ProducesArtifacts lookup lives ONLY on the typed
	// JobTypeRegistry (reg.ProducesArtifacts) at
	// internal/capabilities/jobs/queue/registry.go; nil reg = legacy behaviour,
	// preserving existing test fixtures that don't build a registry.
	producesArtifacts := w.reg != nil && w.reg.ProducesArtifacts(j.Type)
	if producesArtifacts {
		return w.finalizeJobArtifactPath(ctx, j, workerID, leaseID, finalRevision, result)
	}
	w.finalizeJobLegacyComplete(ctx, j, workerID, leaseID, finalRevision, result)
	return nil
}
