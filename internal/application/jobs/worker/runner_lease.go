package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
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
// internal/application/jobs/broker.go) is the broker's typed
// response to a stale Renew attempt — distinct concern
// (broker perspective vs runner orchestration perspective). The
// two sentinels are intentionally separate so each layer's
// contract is independently probeable.
var ErrLeaseLostDuringRun = errors.New("worker Runner: lease lost during run (renewal loop reported error before tools.Complete) — godlike/07 P0 #5 fail-closed: no phantom Complete on a reassigned lease")

// Run is the main claim-and-execute loop. Blocks until ctx is
// cancelled, claiming jobs from the broker and executing them
// via runLease.
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
		lease, err := r.broker.Claim(ctx, appjobs.ClaimCommand{
			WorkerID:        r.workerID,
			WorkerSessionID: r.sessionID,
			Capabilities:    r.caps,
			WaitSeconds:     20,
		})
		if err != nil {
			// W1 Phase 5: ErrNoWorkerCapabilities is a STARTUP misconfiguration,
			// not a transient broker failure. Retrying in a 2s loop would spam
			// logs forever; instead surface a single loud error and exit so the
			// process supervisor restarts with fresh registered caps.
			if errors.Is(err, appjobs.ErrNoWorkerCapabilities) {
				r.log.Error("worker has no advertised capabilities — refusing to retry",
					zap.String("reason", "registered types did not survive parse+dedup; check VELOX_WORKER_CAPABILITIES and cmd/worker startup"))
				return err
			}
			r.log.Warn("claim failed", zap.Error(err))
			time.Sleep(2 * time.Second)
			continue
		}
		if lease == nil || lease.Job == nil {
			continue
		}
		if err := r.runLease(ctx, lease); err != nil {
			r.log.Warn("job failed", zap.String("job_id", lease.Job.ID), zap.Error(err))
		}
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
func (r *Runner) renewLoop(ctx context.Context, tools *Tools, jobID string, errs chan<- error) {
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
func (r *Runner) fail(ctx context.Context, lease *appjobs.Lease, err error) error {
	return r.broker.Fail(ctx, appjobs.FailCommand{
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
