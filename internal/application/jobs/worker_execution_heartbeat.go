// Package jobs — worker_execution_heartbeat.go (worker_execution.go
// split, July 2026; previously worker_lease.go since PR7 split, June
// 2026; FASE 4(b) LeaseState integration + Cut 6.3 fix, July 2026).
//
// Heartbeat (ticker-driven loop that periodically calls lease renewal
// + propagates typed LeaseState in ctx cancellation). Owns:
//
//  1. time.NewTicker cadence (Cut 6.3: leaseTTL / 3, no floor — the
//     pre-Cut-6.3 5s floor silently broke the FASE 4(b) typed cancel
//     contract under leaseTTL <15s).
//
//  2. for-select over stop / ctx.Done / ticker.C; signals completion
//     by closing the done channel.
//
//  3. ctx-cancel propagation: on attemptLeaseRenewal returning
//     shouldExit=true with result.State == LeaseStateCancelRequested,
//     the renew-loop invokes opts.jobCancel so the handler's
//     ctx.Err() short-circuits. This is the typed replacement for the
//     pre-Fase-4 2-second IsCancelled-polling goroutine
//     (startCancelWatcher + cancelPollInterval — REMOVED in FASE 4(b));
//     the cancel signal now flows through the SAME SQL UPDATE that
//     extends the lease, eliminating the per-job DB poll.
//
// Lease INTERACTION (the typed RenewLease call + state classification)
// lives in worker_execution_lease.go::attemptLeaseRenewal. This
// file does NOT call w.repo.RenewLease directly — every single tick
// delegates to the lease helper, preserving the FASE 4(b)
// single-classification-site invariant (godlike/06 SSOT).
//
// ACQUIRE/RELEASE: lease acquire is the broker path (ClaimNext at the
// top of worker.go::Start's poll loop) and lease release is the
// finalisation hooks inside worker_execution_result.go::finalizeJob
// (the consolidated ScheduleRetry / Fail / DeadLetter /
// CompleteWithArtifacts paths — all accept leaseID + revision as
// parameters and atomically transition the row out of running
// state, implicitly releasing the lease as part of the SQL UPDATE).
// So the "acquire/renew/release" lifecycle spans worker.go (acquire
// via ClaimNext), worker_execution_heartbeat.go (ticker-driven loop
// that delegates renew to worker_execution_lease.go), and
// worker_execution_result.go (release implicitly in the finalisation
// hooks). NO new abstraction is added — the renew ticker is the
// ONLY piece that lives full-time while a job is executing.
//
// FASE 4(b) (July 2026) LeaseState integration (ticker half):
//   - LeaseStateContinue    → log nothing, continue.
//   - LeaseStateCancelRequested → invoke opts.jobCancel; loop exits
//     (the worker jobCtx MUST be cancelled
//     so in-flight handler calls short-circuit
//     via ctx.Err()).
//   - LeaseStateLeaseLost   → exit WITHOUT invoking opts.jobCancel
//     (worker is orphaned, not cancelled;
//     the finalizer's lease-loss probing on
//     the SQL UPDATE handles the orphan
//     state).
//
// Cut 6.3 (July 2026) refresh: production leaseTTL is config-driven
// via cfg.Jobs.LeaseTTL (always ≥30s in production); test mocks use
// leaseTTL=15ms / 60ms to verify the typed cancel signal propagates
// within a 4-second test budget. The renewed cadence = leaseTTL/3
// is invariant under all leaseTTL ranges (was previously floored at
// 5s which silently broke the FASE 4(b) contract tests under
// leaseTTL <15s).
//
// godlike/06 SSOT single-orchestrator-invariants: transitions
// discipline (lease → heartbeat → result) is preserved across the
// refactor. The orchestrator worker_execution.go::runJob calls
// `go w.renewLeaseLoopWith(...)` unchanged; the lease helper
// worker_execution_lease.go::attemptLeaseRenewal is invoked only
// from inside the ticker; finalizeJob in worker_execution_result.go
// runs after the loop closes. The state machine is byte-for-byte
// equivalent to the pre-extract version.
package jobs

import (
	"context"
	"time"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// renewLeaseLoopOpts carries the optional jobCancel callback (Fase 4(b))
// so the renew-loop can cancel the worker jobCtx on a
// LeaseStateCancelRequested return WITHOUT spawning a parallel
// IsCancelled-poll goroutine. The callback is nil-tolerant for
// legacy call sites that do not need the cancel integration
// (godlike/07 minimum-blast-radius: pre-Fase-4 callers with no
// jobCancel continue to compile).
type renewLeaseLoopOpts struct {
	// jobCancel, if non-nil, is invoked when the typed lease
	// state is LeaseStateCancelRequested. Idempotent (calling
	// jobCancel on an already-cancelled ctx is a no-op).
	jobCancel context.CancelFunc
}

// renewLeaseLoopWith drives the heartbeat ticker. The for-select
// loop structure / ctx-cancel / stop-channel / done-channel
// invariants are unchanged from the pre-extract version — the
// ONLY refactor is moving the typed LeaseState classification
// out of this loop into worker_execution_lease.go::attemptLeaseRenewal.
//
// FASE 4(b) propagation contract (this file's slice):
//   - When attemptLeaseRenewal returns shouldExit=true with
//     result.State == LeaseStateCancelRequested, the ticker loop
//     invokes opts.jobCancel() so the handler's ctx.Err() fires.
//   - When attemptLeaseRenewal returns shouldExit=true with
//     result.State == LeaseStateLeaseLost, the ticker loop EXITS
//     without invoking opts.jobCancel (worker is orphaned, not
//     cancelled).
//
// godlike/07 minimum-blast-radius: the loop ticks at
// leaseTTL/3 (Cut 6.3, NO floor); the production cadence is
// unchanged from the pre-extract version (production
// leaseTTL=30s → 10s heartbeat tick).
func (w *Worker) renewLeaseLoopWith(ctx context.Context, jobID string, stop <-chan struct{}, done chan<- struct{}, opts renewLeaseLoopOpts) {
	defer close(done)
	interval := w.leaseTTL / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, shouldExit := w.attemptLeaseRenewal(ctx, jobID)

			if shouldExit {
				// Fase 4(b): Propagate context cancellation if the
				// store affirmatively returned CancelRequested. We
				// do NOT cancel on LeaseStateLeaseLost — the worker
				// is orphaned, not cancelled.
				if result.State == jobs.LeaseStateCancelRequested && opts.jobCancel != nil {
					opts.jobCancel()
				}
				return
			}
		}
	}
}
