package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// postRenewFailClosedCheckTimeout bounds the post-renewCancel
// drain step in runLease. After renewCancel, the renewal
// goroutine has a 200ms window to deliver any in-flight error
// on renewErrs before the runLease select gives up and falls
// through to the canonical tools.Complete call. The 200ms is
// the historical value; it exceeds the renew interval by ~4x
// at the default cadence (DefaultRenewInterval = 30s) but is
// the safety net for a fast-cadence test (50ms minimum).
const postRenewFailClosedCheckTimeout = 200 * time.Millisecond

// ErrLeaseLostDuringRun surfaces a lease-lost condition detected
// in the post-handler drain step BEFORE the final tools.Complete
// call. Pre-P0 #5 the runner's runLease ignored the
// renewal-loop's reported error in the post-renewCancel drain
// step and proceeded to call tools.Complete anyway, producing a
// phantom Complete on a lease the broker had already
// reassigned to another worker. Post-P0 #5 the runner
// fail-closes with this typed error BEFORE calling
// tools.Complete, so the broker never sees a stale terminal
// report from a worker that no longer owns the lease.
//
// godlike/07 typed-error contract: errors.Is reachable via the
// %w chain in postRenewFailClosedCheck. The original renewErr
// (typically sqljobs.ErrLeaseLost) is ALSO reachable via the
// same error chain thanks to Go 1.20+ multi-%w. Callers can
// probe either sentinel with a single errors.Is call.
//
// godlike/06 SSOT: ErrLeaseLostDuringRun is the worker-package
// owner of the "lease lost during run but BEFORE Complete" fact.
// Placed next to ErrArtifactClientRequired in this file. The
// sqljobs.ErrLeaseLost sentinel (from
// internal/capabilities/jobs/queue/broker.go) is the broker's typed
// response to a stale Renew attempt — distinct concern
// (broker perspective vs runner orchestration perspective). The
// two sentinels are intentionally separate so each layer's
// contract is independently probeable.
var ErrLeaseLostDuringRun = errors.New("worker Runner: lease lost during run (renewal loop reported error before tools.Complete) — godlike/07 P0 #5 fail-closed: no phantom Complete on a reassigned lease")

// Run is the main claim-and-execute loop. Blocks until ctx is
// cancelled, claiming jobs from the broker and executing them
// via runLease.
//
// P0 #6 (July 2026): the claim retry path now routes through
// pkg/retry.Do (the canonical godlike/06 SSOT surface for bounded
// retry loops) instead of an inlined `time.Sleep(2 * time.Second)`
// + infinite outer-loop retry. The migration settles two P0 #6
// integration-test blockers:
//
//  1. **ctx-aware sleep**: pre-P0 #6 `time.Sleep(d)` ignored ctx
//     cancellation during the 2s settle wait, so a shutdown request
//     during the settle would block until d elapsed. retry.Do's
//     per-retry sleep selects on `ctx.Done()` so cancellation aborts
//     the wait immediately (godlike/07 fail-closed seam for
//     shutdown paths).
//
//  2. **Deterministic test timing** (the gating P0 #6 assertion):
//     integration tests inject `Options{Clock: myFakeClock}` so
//     per-retry sleeps are advanced on demand — no 2s wall-clock
//     flake on slow CI (the original CI flake in runner_test.go:383
//     was the symptom that led to P0 #6 ticket creation).
//
// retry.Do configuration (tight fast-retry budget):
//   - MaxAttempts:    5 — bounded inner retry budget so a sustained
//     broker outage exits the inner loop within ~5 seconds; the
//     OUTER for-loop continues claim attempts forever (matches
//     pre-P0 #6 "try forever" intent).
//   - InitialBackoff: 200ms — fast first retry so broker-flap
//     scenarios don't stall the worker for seconds (vs the pre-P0 #6
//     fixed 2s settle wait, which absorbed transient broker hiccups
//     but stalled for the same 2s on persistent outages).
//   - MaxBackoff:     2s — exponential saturation cap; 5 attempts
//     at 200ms/400ms/800ms/1.6s/2s = ~5s worst-case inner budget
//     before the outer for-loop takes over.
//   - BackoffFactor:  2.0 — canonical exponential doubling.
//   - JitterFraction: 0 — deterministic timing for the integration
//     test (no thundering-herd randomness in test assertions).
//   - IsRetryable:    retry.IsTransient — the canonical pkg/retry
//     pure-typed-probe predicate. Post-FASE-6-Cut-6.1.D returns
//     true ONLY for: (a) errors with a RetryableError interface
//     implementer reporting IsRetryable() == true, OR (b) errors
//     wrapping a *TransientInfrastructureError carrier. Non-typed
//     broker errors fail-closed at first attempt. The post-Do
//     `errors.Is(err, ErrNoWorkerCapabilities)` check below
//     explicitly handles the W1 Phase 5 startup-misconfig surface
//     so the typed-retryable path and the typed-startup-misconfig
//     path are independently probeable.
//
// godlike/07 NO-FAKE-AVAILABILITY: ErrNoWorkerCapabilities is a
// TYPED NON-TRANSIENT sentinel (no RetryableError implementer);
// retry.IsTransient returns false at first attempt → retry.Do
// returns the typed sentinel → outer code's errors.Is match
// surfaces ErrNoWorkerCapabilities for the W1 Phase 5
// loud-error-and-return contract. NO retry on a startup misconfig.
func (r *Runner) Run(ctx context.Context) error {
	if r.registry == nil {
		r.registry = NewRegistry()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var lease *jobs.Lease
		err := retry.Do(ctx, func() error {
			var claimErr error
			lease, claimErr = r.broker.Claim(ctx, jobs.ClaimCommand{
				WorkerID:        r.workerID,
				WorkerSessionID: r.sessionID,
				Capabilities:    r.caps,
				WaitSeconds:     20,
			})
			return claimErr
		}, retry.Options{
			MaxAttempts:    5,
			InitialBackoff: 200 * time.Millisecond,
			MaxBackoff:     2 * time.Second,
			BackoffFactor:  2.0,
			JitterFraction: 0,
			DisableJitter:  true,
			IsRetryable:    retry.IsTransient,
		})
		if err != nil {
			// pre-P0 #6 contract preserved: ErrNoWorkerCapabilities
			// (IsRetryable returned false) surfaces here as non-nil
			// immediately on first attempt.
			if errors.Is(err, jobs.ErrNoWorkerCapabilities) {
				r.log.Error("worker has no advertised capabilities — refusing to retry",
					zap.String("reason", "registered types did not survive parse+dedup; check VELOX_WORKER_CAPABILITIES and cmd/worker startup"))
				return err
			}
			r.log.Warn("claim failed (retry budget exhausted)", zap.Error(err))
			continue
		}
		if lease == nil || lease.Job == nil {
			continue
		}
		// Claim-time KPI snapshot: the instant broker.Claim returns the job is
		// claimed and NO unit has executed yet — the pristine readiness
		// photograph (required/ready/running/missing + prepared_at_claim_ratio)
		// can only be captured here, before runLease starts mutating units.
		// Best-effort: a snapshot failure never delays or fails the claim.
		r.captureClaimSnapshot(ctx, lease)
		if err := r.runLease(ctx, lease); err != nil {
			r.log.Warn("job failed", zap.String("job_id", lease.Job.ID), zap.Error(err))
		}
	}
}

// captureClaimSnapshot records the prepared_at_claim_ratio KPI for a just-
// claimed lease via the durable preparation store snapshotter, when wired.
// It runs synchronously BEFORE runLease so the readiness counts reflect the
// exact claim instant (immediately after, RUNNING → READY / MISS → READY
// transitions destroy the pristine state). Errors are logged, never returned:
// snapshotting is a control-plane side effect and must not delay claim work.
func (r *Runner) captureClaimSnapshot(ctx context.Context, lease *jobs.Lease) {
	if r == nil || r.claimSnapshot == nil || lease == nil || lease.Job == nil {
		return
	}
	jobRef := lease.Job
	attemptID := fmt.Sprintf("%s:%d", jobRef.ID, jobRef.Revision)
	if _, err := r.claimSnapshot.SnapshotPreparationClaim(ctx, job.PreparationClaimInput{
		JobID:       jobRef.ID,
		AttemptID:   attemptID,
		JobRevision: int64(jobRef.Revision),
		ClaimedAt:   time.Now().UTC(),
	}); err != nil {
		r.log.Warn("claim-time preparation snapshot failed",
			zap.String("job_id", jobRef.ID),
			zap.String("attempt_id", attemptID),
			zap.Error(err))
	}
}

// renewLoop ticks every r.effectiveRenewInterval() and calls
// tools.Renew with the canonical DefaultLeaseTTL. On any error
// (ErrLeaseLost in particular — the broker has reassigned the job
// to another worker — or a transient broker round-trip failure),
// the error is sent once on errs and the goroutine returns.
//
// Lifecycle: the goroutine exits when ANY of these happens first:
//   - renewCtx is cancelled (handler returned; parent ctx done)
//   - the ticker fires and tools.Renew returns an error
//
// Channel semantics: errs has capacity 1 so the goroutine never
// blocks on send. The runner's checkRenew helper drains it between
// phases. The cap of 1 is sufficient because the goroutine returns
// immediately after the first send.
func (r *Runner) renewLoop(ctx context.Context, tools *Tools, jobID string, errs chan<- error, jobCancel context.CancelFunc) {
	ticker := time.NewTicker(r.effectiveRenewInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := tools.Renew(ctx, DefaultLeaseTTL); err != nil {
				r.log.Warn("lease renew failed",
					zap.String("job_id", jobID),
					zap.Error(err))
				errs <- err
				// A renewal failure means this worker can no longer prove
				// ownership of the job. Publish the error first so the
				// fail-closed terminal path cannot miss it, then cancel the
				// handler context so in-flight provider calls and
				// subprocesses stop instead of continuing after the lease
				// has been lost or cancelled.
				if jobCancel != nil {
					jobCancel()
				}
				return
			}
			r.log.Debug("lease renewed",
				zap.String("job_id", jobID),
				zap.Duration("ttl", DefaultLeaseTTL))
		}
	}
}

// fail is the pre-tools fail path used BEFORE tools is constructed.
// DEPRECATED since Phase 7 — only valid in the unsupported-job-type
// branch (runLease → !r.registry.Has check), which runs before
// `tools := NewTools(...)`. After Tools is constructed in runLease,
// all fail paths MUST use tools.Fail (which carries the
// post-renewal revision via the Tools atomic revision). Do NOT
// reach for r.fail from any other fail path; doing so silently
// regresses the revision-drift fix.
func (r *Runner) fail(ctx context.Context, lease *jobs.Lease, err error) error {
	return r.broker.Fail(ctx, jobs.FailCommand{
		WorkerID:         r.workerID,
		WorkerSessionID:  r.sessionID,
		JobID:            lease.Job.ID,
		LeaseID:          lease.LeaseID,
		ExpectedRevision: lease.Job.Revision,
		Error:            err.Error(),
	})
}

// postRenewFailClosedCheck is the P0 #5 fail-closed seam. It
// returns renewErr wrapped as ErrLeaseLostDuringRun if the
// renewal loop reported an error, else returns nil so the
// caller proceeds to the terminal action (e.g. tools.Complete).
//
// godlike/07 typed-error contract: errors.Is(err, ErrLeaseLostDuringRun)
// AND errors.Is(err, original renewErr) both probe correctly
// via Go 1.20+ multi-%w wrapping.
//
// Why a helper (thinker-Option-D-extracted): the runLease
// terminal sequence is order-sensitive (renewCancel → drain
// renewErrs → Complete). Extracting the drain into a single
// named function makes the seam unit-testable in isolation
// (the TestPostRenewFailClosedCheck_* cases pin the typed-error
// contract without the runLease boilerplate) AND keeps the
// runLease call site short and obvious. The helper is
// package-private; production callers always go through
// runLease.
func postRenewFailClosedCheck(renewErrs <-chan error) error {
	select {
	case renewErr := <-renewErrs:
		if renewErr != nil {
			return fmt.Errorf("worker Runner: pre-Complete lease renewal failed: %w: %w", ErrLeaseLostDuringRun, renewErr)
		}
		return nil
	case <-time.After(postRenewFailClosedCheckTimeout):
		return nil
	}
}
