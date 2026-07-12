// Package jobs — worker_lease.go (PR7 split, June 2026; FASE 4(b)
// LeaseState integration, July 2026; Cut 6.3 fix, July 2026).
//
// Lease renewal ticker extracted from worker.go. Owns:
//
//  1. func (w *Worker) renewLeaseLoop — ticker-driven loop that
//     periodically calls w.repo.RenewLease(ctx, jobID, w.id,
//     w.leaseTTL). Inspects the typed kerneljob.RenewLeaseResult
//     (Fase 4(b)) so concurrent cancellation propagates through
//     the SAME SQL UPDATE that extends the lease — no parallel
//     2-second IsCancelled-poll goroutine is required. Returns
//     early on stop-channel close or ctx cancellation; signals
//     completion by closing the done channel.
//
// ACQUIRE/RELEASE: lease acquire is the broker path (ClaimNext at the
// top of worker.go::Start's poll loop) and lease release is the
// finalisation hooks inside worker_execution.go::runJob (the
// consolidated FinalizeAttempt / ScheduleRetry / Fail / DeadLetter /
// Complete / CompleteWithArtifacts paths — all accept leaseID + revision
// as parameters and atomically transition the row out of running state,
// implicitly releasing the lease as part of the SQL UPDATE). So the
// "acquire/renew/release" lifecycle spans worker.go (acquire via
// ClaimNext), worker_lease.go (renew here, with the typed LeaseState
// cancel-flag integration), and worker_execution.go (release implicitly
// in the finalisation hooks). NO new abstraction is added — the renew
// ticker is the ONLY piece that lives full-time while a job is executing.
//
// FASE 4(b) (July 2026) LeaseState integration:
//   - LeaseStateContinue     → log nothing, continue.
//   - LeaseStateCancelRequested → invoke jobCancel (the worker_ctx
//     must be cancelled so in-flight handler calls short-circuit
//     via ctx.Err()). This is the typed replacement for the
//     pre-Fase-4 startCancelWatcher goroutine that polled
//     broker.IsCancelled every 2 seconds; the cancel signal
//     now flows through the SAME SQL UPDATE that extends the
//     lease, eliminating the per-job DB poll.
//   - LeaseStateLeaseLost    → log+return; the worker's
//     finalisation path will surface the typed ErrLeaseLost
//     to the finalizer (or to a non-finalize abort path in
//     future PRs).
//
// Cut 6.3 (July 2026) — leaseTTL cadence policy:
// The renew-cadence is `w.leaseTTL / 3` (three renewals per lease
// period). PREVIOUSLY floored at 5*time.Second to defend against a
// hypothetical leaseTTL=1s configuration hammering the SQLite writer
// pool; Cut 6.3 REMOVES the floor so the typed LeaseState path is
// invariant under all leaseTTL ranges (production leaseTTL is
// config-driven via cfg.Jobs.LeaseTTL, never <30s in production;
// the floor was a defensive-only knob that BROKE the canonical
// FASE 4(b) contract tests because they use leaseTTL=15ms /
// leaseTTL=60ms to verify the typed CancelRequested / LeaseLost
// signals propagate within a 4-second test budget).
// godlike/07 minimum-blast-radius: the floor removal is a positive
// test-prod parity change — the production Worker with
// leaseTTL=30s ticks at 10s (unchanged from before Cut 6.3). The
// defense against pathological leaseTTL values belongs in cfg layer
// validation, not in the runtime renew-ticker.
//
// Mechanical split + Fase 4(b) integration + Cut 6.3 fix.
// Net behavior change: the typed LeaseState signal now propagates
// under all leaseTTL ranges (was broken under leaseTTL < 15s).
package jobs

import (
	"context"
	"time"

	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// renewLeaseLoopOpts carries the optional jobCancel callback (Fase 4(b))
// so the renew-loop can cancel the worker jobCtx on a LeaseStateCancelRequested
// return WITHOUT spawning a parallel IsCancelled-poll goroutine. The
// callback is nil-tolerant for legacy call sites that do not need
// the cancel integration (godlike/07 minimum-blast-radius: the
// pre-Fase-4 worker.go::runJob callers that have no jobCancel func
// continue to compile).
type renewLeaseLoopOpts struct {
	// jobCancel, if non-nil, is invoked when the typed lease
	// state is LeaseStateCancelRequested. Idempotent (calling
	// jobCancel on an already-cancelled ctx is a no-op).
	jobCancel context.CancelFunc
}

func (w *Worker) renewLeaseLoop(ctx context.Context, jobID string, stop <-chan struct{}, done chan<- struct{}) {
	w.renewLeaseLoopWith(ctx, jobID, stop, done, renewLeaseLoopOpts{})
}

func (w *Worker) renewLeaseLoopWith(ctx context.Context, jobID string, stop <-chan struct{}, done chan<- struct{}, opts renewLeaseLoopOpts) {
	defer close(done)
	// Cut 6.3 (July 2026): renew cadence = leaseTTL / 3 directly, with
	// NO floor. Previously floored at 5*time.Second to guard against a
	// hypothetical leaseTTL=1s configuration; that floor silently
	// broke the FASE 4(b) typed-cancel contract under leaseTTL <15s
	// because the renew-ticker never fired within the test budget and
	// renewHits=0. Production leaseTTL is config-driven via
	// cfg.Jobs.LeaseTTL (always ≥30s in production deployments),
	// so an unbounded leaseTTL/3 is safe — see package doc for the
	// rationale + the godlike/07 fail-closed cfg layer ownership.
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
			result, err := w.repo.RenewLease(ctx, jobID, w.id, w.leaseTTL)
			// Fase 4(b): inspect the typed result.State first.
			// The state is the canonical signal; the error return
			// is reserved for non-lease-state failures (network,
			// SQL). LeaseStateLeaseLost carries a companion
			// ErrLeaseLost so callers can errors.Is probe the
			// pre-Fase-4 sentinel symmetrically.
			if result.State == kerneljob.LeaseStateCancelRequested {
				w.log.Info("worker: lease renewal observed cancel_requested (Fase 4(b) typed signal); cancelling jobCtx",
					zap.String("job_id", jobID))
				if opts.jobCancel != nil {
					opts.jobCancel()
				}
				// Stop the renew loop — the handler's ctx is
				// cancelled, the finalizer will abort the job.
				return
			}
			if result.State == kerneljob.LeaseStateLeaseLost {
				w.log.Warn("worker: lease lost during renewal (Fase 4(b) typed signal); aborting",
					zap.String("job_id", jobID), zap.Error(err))
				return
			}
			// LeaseStateContinue (or an empty state, defensively)
			// — log non-state errors only; the typed result.State
			// is the authoritative success signal.
			if err != nil && result.State != kerneljob.LeaseStateContinue {
				w.log.Warn("failed to renew lease",
					zap.String("job_id", jobID), zap.Error(err))
			}
		}
	}
}
