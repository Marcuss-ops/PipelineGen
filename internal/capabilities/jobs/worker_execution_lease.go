// Package jobs — worker_execution_lease.go (worker_execution.go
// split, July 2026).
//
// Lease verification + renewal + classification (the typed
// RenewLeaseResult envelope decision layer). Owns the SOLE place
// where the worker reaches into the configured store to actively
// extend a job's lease: this is the lease INTERACTION half of the
// renew-loop. The other half (the ticker structure + context
// propagation) lives in worker_execution_heartbeat.go.
//
// godlike/06 SSOT (single lease-renewal surface): the worker has
// exactly ONE function that calls w.repo.RenewLease —
// attemptLeaseRenewal. Adding a second RenewLease call site is
// forbidden (it would duplicate the typed LeaseState classification
// logic and break the FASE 4(b) test pinners at cancellation_test.go).
//
// godlike/10 SSOT helpers-as-receivers: this file holds ONLY the
// typed renewal + result classification — no business branching,
// no ticker / cadence state, no context-cancel propagation (those
// live in worker_execution_heartbeat.go). The helper returns BOTH
// the typed result (so the caller can dispatch on State) AND a
// shouldExit boolean (so the caller does NOT need to re-evaluate
// the LeaseState logic). The single source of truth for the
// LeaseState → shouldExit mapping lives here; the heartbeat loop
// is a pure plumbing layer.
//
// FASE 4(b) (July 2026) — LeaseState typed-result contract:
//
//	LeaseStateContinue    → shouldExit=false. Loop continues.
//	                          Caller MUST NOT cancel jobCtx.
//	LeaseStateCancelRequested → shouldExit=true. Caller MUST invoke
//	                          opts.jobCancel so the handler
//	                          short-circuits via ctx.Err().
//	LeaseStateLeaseLost   → shouldExit=true. Caller MUST NOT cancel
//	                          jobCtx (worker is orphaned, not cancelled).
//
// Cut 6.3 (July 2026) — renew cadence invariant: leaseTTL cadence is
// owned by worker_execution_heartbeat.go (ticker setup). The helper
// itself is per-tick; no internal cadence / ticker state lives here.
//
// godlike/07 fail-closed posture: the typed LeaseState enum
// (`job.LeaseState*`) is the SOLE termination signal. The
// `error` return from RenewLease is logged but never alone causes
// loop termination — a non-state error is treated as a transient
// hiccup and the loop retries on the next tick via the heartbeat's
// ticker-driven cadence.
//
// godlike/10 non-duplication policy (relationship to fencing): the
// pre-CAS fencing checks (errors.Is(err, job.ErrLeaseLost)) live in
// worker_execution_result.go::finalizeJob, NOT here. Splitting the
// lease-renewal ticker from the lease-loss-on-commit fencing avoids
// duplicating the typed ErrLeaseLost probing between the two
// surfaces; each helper owns its own error-classification domain.
package jobs

import (
	"context"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// attemptLeaseRenewal reaches into the data store to persist lease
// extension for a single tick and returns the typed LeaseState
// classification to the orchestration loop.
//
// FASE 4(b) typed-signal contract (canonical, regression-locked by
// cancellation_test.go):
//
//   - LeaseStateCancelRequested → emit Info log + shouldExit=true.
//     The heartbeat loop will invoke opts.jobCancel on the worker
//     jobCtx so handlers short-circuit via ctx.Err(). This is the
//     typed replacement for the pre-Fase-4 2-second IsCancelled-polling
//     goroutine.
//
//   - LeaseStateLeaseLost → emit Warn log + shouldExit=true. The
//     heartbeat loop will NOT cancel jobCtx (worker is orphaned,
//     not cancelled); the finalizer in worker_execution_result.go
//     handles the orphan state via errors.Is(err, job.ErrLeaseLost)
//     probing on the SQL UPDATE.
//
//   - LeaseStateContinue → log non-state errors only (typed
//     result.State is the authoritative success signal);
//     shouldExit=false; loop continues.
//
//   - LeaseState empty / unknown → treat as transient failure;
//     log non-state errors only; shouldExit=false. The typed
//     LeaseState is the canonical signal; an empty undocumented
//     state is NOT legitimate renewal.
//
// godlike/07 minimum-blast-radius: this helper signature is the SOLE
// place that calls w.repo.RenewLease. Test mocks in
// cancellation_test.go::renewLoopMockJobBroker.RenewLease pin the
// typed LeaseState envelope; the helper's return semantics MUST
// stay (result, shouldExit) to keep `TestRenewLeaseLoopWith_CancelRequested_TriggersJobCancel`
// + `TestRenewLeaseLoopWith_LeaseLost_AbortsLoop` +
// `TestRenewLeaseLoopWith_Continue_NoOp` passing byte-for-byte.
func (w *Worker) attemptLeaseRenewal(ctx context.Context, jobID string) (job.RenewLeaseResult, bool) {
	result, err := w.repo.RenewLease(ctx, jobID, w.id, w.leaseTTL)

	if result.State == job.LeaseStateCancelRequested {
		w.log.Info("worker: lease renewal observed cancel_requested (Fase 4(b) typed signal); cancelling jobCtx",
			zap.String("job_id", jobID))
		return result, true
	}
	if result.State == job.LeaseStateLeaseLost {
		// Cancel() is a terminal operator transition: it clears the
		// worker/lease fence before the running handler can observe the
		// next renewal. The SQL adapter therefore reports LeaseLost for
		// both a genuinely stolen lease and an explicitly cancelled job.
		// Preserve the orphan rule for the former, but stop the handler
		// for the latter so cancellation cannot leave provider subprocesses
		// running after the job is already terminal.
		if cancelled, getErr := w.repo.Get(ctx, jobID); getErr == nil && cancelled != nil && cancelled.Status == job.StatusCancelled {
			w.log.Info("worker: lease renewal observed terminal cancellation; cancelling jobCtx",
				zap.String("job_id", jobID))
			return job.RenewLeaseResult{State: job.LeaseStateCancelRequested}, true
		}
		w.log.Warn("worker: lease lost during renewal (Fase 4(b) typed signal); aborting",
			zap.String("job_id", jobID), zap.Error(err))
		return result, true
	}

	// LeaseStateContinue (or empty state, defensively):
	// log non-state errors only; the typed result.State is the
	// authoritative success signal. godlike/07 fail-closed: a
	// non-state error is treated as a transient hiccup; loop
	// retries on the next ticker tick.
	if err != nil && result.State != job.LeaseStateContinue {
		w.log.Warn("failed to renew lease",
			zap.String("job_id", jobID), zap.Error(err))
	}

	return result, false
}
