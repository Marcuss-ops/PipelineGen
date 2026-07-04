// Package jobs — worker_execution.go (PR7 split, June 2026).
//
// Job execution + finalisation extracted from worker.go. Owns:
//
//  1. func (w *Worker) runJob — the per-job dispatcher pipeline:
//     parent ctx → correlation-id enriched ctx → timeout-bounded
//     jobCtx (per Worker.jobTimeoutFor) → Dispatcher.Dispatch →
//     finalisation (ScheduleRetry / Fail / DeadLetter / Complete
//     with retry-backoff math + lease-id + revision snapshot).
//
// CRITICAL INVARIANT: the finalizationCtx MUST stay
// `context.WithTimeout(context.Background(), 30*time.Second)` —
// this is one of the AGENTS.md context-util-table explicitly
// allowlisted `context.Background()` sites. The purpose is to
// survive jobCtx cancellation so the DB write that flips the
// job row to failed/completed/dead-lettered state can complete
// even when the worker is mid-shutdown. Detaching from jobCtx
// (rather than from ctx / worker lifecycle) prevents losing
// outcome persistence when jobCtx is cancelled by either
// timeout or by the outer worker Stop. This invariant MUST be
// preserved byte-for-byte across PR7.
//
// Issue 6 (June 2026, P1): added `startCancelWatcher` helper +
// integration in runJob so user-initiated cancellation via the
// broker (Cancel route -> Job.Status = CANCELLED) propagates
// into jobCtx — handlers that poll ctx.Err() at phase boundaries
// can short-circuit Ollama / voiceover / image generation calls
// instead of continuing for the full job-timeout. The 2-second
// poll interval balances latency-to-cancel against IsCancelled's
// DB hit; the watcher exits when jobCtx becomes Done (which
// happens naturally via `defer jobCancel()` regardless of whether
// the cancel was driven by watcher or timeout).
//
// Mechanical split, zero behavior change for the finalizationCtx.
// ONLY relocated + import-redistributed.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
	"go.uber.org/zap"
)

// cancelPollInterval is the polling cadence for the cancel-watcher
// goroutine. IsCancelled hits the database (w.repo.Get(jobCtx, ...))
// so the interval must balance responsiveness against DB load —
// 2 seconds matches the canonical lease-renewal cadence
// (RunnerConfig.LeaseTTL / 5) and stays well below the canonical
// 60-minute script.generate timeout so handlers observe the cancel
// signal long before the timeout fires.
//
// Issue 6 (June 2026, P1): hard-coded here rather than exposed as
// a WorkerConfig knob; the interval is operational-tunable via a
// follow-up PR if real-world telemetry shows the chosen cadence
// is wrong, but a single shared constant across all job types is
// the simpler principled default.
const cancelPollInterval = 2 * time.Second

// startCancelWatcher spawns a goroutine that polls isCancelled and
// calls jobCancel when the check returns true. The watcher exits
// when jobCtx becomes Done — which the caller covers via
// `defer jobCancel()`, so the goroutine always has a clean exit
// path. Nil-tolerant isCancelled (test fixtures) is a no-op spawn.
//
// Issue 6 (June 2026, P1): extracted into a helper so the cancel
// wiring can be unit-tested without spinning up the full Worker
// machinery. Spawning the goroutine directly inside runJob would
// make the test depend on the broker-claim loop and timing
// (flaky); this helper lets TestStartCancelWatcher pin the
// polling semantics in isolation before the end-to-end test
// (TestWorker_CancelsRunningJobOnCancelSignal) covers the
// envelope through Worker.runJob.
func startCancelWatcher(jobCtx context.Context, jobCancel context.CancelFunc, isCancelled func() bool) {
	if isCancelled == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(cancelPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				if isCancelled() {
					jobCancel()
					return
				}
			}
		}
	}()
}

func (w *Worker) runJob(parent context.Context, j *job.Job) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	if j.CorrelationID != "" {
		ctx = corid.WithCorrelationID(ctx, j.CorrelationID)
	}

	w.log.Info("running job",
		zap.String("job_id", j.ID),
		zap.String("type", j.Type),
		zap.String("correlation_id", j.CorrelationID),
		zap.String("lease_id", j.LeaseID),
		zap.Int("revision", j.Revision),
	)

	// HC-1 (June 2026): per-job-type timeout resolves through the
	// typed Registry attached via WithRegistry(). Replaces the
	// pre-HC-1 `context.WithTimeout(ctx, jobTimeout(j.Type))` call
	// which read from a package-level `var jobTimeoutRegistry` map.
	jobCtx, jobCancel := context.WithTimeout(ctx, w.jobTimeoutFor(j.Type))
	defer jobCancel()

	// Lease renewal.
	stopLease := make(chan struct{})
	leaseDone := make(chan struct{})
	go w.renewLeaseLoop(jobCtx, j.ID, stopLease, leaseDone)

	// Snapshot lease tokens for finalisation.
	workerID := w.id
	leaseID := j.LeaseID
	revision := j.Revision

	tools := &JobTools{
		Progress: func(progress int, message string) {
			if err := w.repo.SetProgress(jobCtx, j.ID, progress, message); err != nil {
				w.log.Warn("failed to report progress",
					zap.String("job_id", j.ID),
					zap.Int("progress", progress),
					zap.Error(err))
			}
		},
		Event: func(eventType string, message string, data map[string]any) {
			if err := w.repo.AddEvent(jobCtx, j.ID, eventType, message, data); err != nil {
				w.log.Warn("failed to record event",
					zap.String("job_id", j.ID),
					zap.String("event_type", eventType),
					zap.Error(err))
			}
		},
		IsCancelled: func() bool {
			domJob, err := w.repo.Get(jobCtx, j.ID)
			if err != nil {
				return false
			}
			return domJob != nil && domJob.Status == job.StatusCancelled
		},
	}

	// Issue 6 (June 2026, P1): hook the cancel-watcher BEFORE
	// Dispatcher.Dispatch so any handler entry that observes
	// ctx.Err() can short-circuit the pipeline (Ollama / voiceover
	// / image generation calls). Watcher exits when jobCtx becomes
	// Done — covered by `defer jobCancel()` so goroutine has a
	// clean exit regardless of whether the cancel was triggered by
	// the watcher or by the timeout. Nil isCancelled (test
	// fixtures that bypass the registry) is a no-op.
	startCancelWatcher(jobCtx, jobCancel, tools.IsCancelled)

	result, dispatchErr := w.dispatcher.Dispatch(jobCtx, j, tools)

	close(stopLease)
	<-leaseDone

	// ── finalizationCtx ───────────────────────────────────────────────
	// AGENTS.md §context-util-table explicitly allowlists this
	// context.Background() site. The purpose is to survive jobCtx
	// cancellation so the DB write that flips the job row to
	// failed / completed / dead-lettered state can complete even
	// when the worker is mid-shutdown. Detaching from jobCtx
	// (rather than from ctx / worker lifecycle) prevents losing
	// outcome persistence when jobCtx is cancelled by either
	// timeout or by the outer worker Stop. 30s upper bound keeps
	// a stuck DB write from blocking shutdown indefinitely.
	finalizationCtx, finalCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer finalCancel()
	finalRevision := revision
	if jFresh, err := w.repo.Get(finalizationCtx, j.ID); err == nil && jFresh != nil && jFresh.Revision > 0 {
		finalRevision = jFresh.Revision
	}

	if dispatchErr != nil {
		w.log.Error("job failed",
			zap.String("job_id", j.ID),
			zap.Error(dispatchErr))

		if j.RetryCount < j.MaxRetries {
			backoff := time.Duration(1<<j.RetryCount) * 2 * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			w.log.Info("scheduling job for retry",
				zap.String("job_id", j.ID),
				zap.Duration("backoff", backoff))

			// ScheduleRetry does running→queued atomically with
			// server-side backoff via available_at. No intermediate
			// "failed" state — avoids false alerting.
			if retryErr := w.repo.ScheduleRetry(finalizationCtx, j.ID, workerID, leaseID, finalRevision, backoff); retryErr != nil {
				if errors.Is(retryErr, sqljobs.ErrLeaseLost) {
					w.log.Warn("lease lost during ScheduleRetry — another worker claimed this job",
						zap.String("job_id", j.ID))
				} else {
					w.log.Error("failed to schedule retry",
						zap.String("job_id", j.ID),
						zap.Error(retryErr))
				}
			}
			return
		}

		if failErr := w.repo.Fail(finalizationCtx, j.ID, workerID, leaseID, finalRevision, dispatchErr.Error()); failErr != nil {
			if errors.Is(failErr, sqljobs.ErrLeaseLost) {
				w.log.Warn("lease lost during fail (exhausted retries)",
					zap.String("job_id", j.ID))
			} else {
				w.log.Error("failed to mark job as failed",
					zap.String("job_id", j.ID),
					zap.Error(failErr))
			}
		}
		if dlqErr := w.repo.DeadLetter(finalizationCtx, j.ID, dispatchErr.Error()); dlqErr != nil {
			w.log.Warn("failed to dead-letter job", zap.String("job_id", j.ID), zap.Error(dlqErr))
		} else {
			w.log.Warn("job moved to dead letter queue",
				zap.String("job_id", j.ID),
				zap.Int("retry_count", j.RetryCount),
				zap.Error(dispatchErr))
		}
		return
	}

	if completeErr := w.repo.Complete(finalizationCtx, j.ID, workerID, leaseID, finalRevision, mapToRawMessage(result)); completeErr != nil {
		if errors.Is(completeErr, sqljobs.ErrLeaseLost) {
			w.log.Warn("lease lost during complete — another worker claimed this job",
				zap.String("job_id", j.ID))
		} else if errors.Is(completeErr, sqljobs.ErrArtifactJobRequiresCompleteWithArtifacts) {
			// P0 (July 2026): the legacy Worker path cannot call
			// CompleteWithArtifacts — the job.Store interface has
			// no such method. Fail the job so it reaches a terminal
			// state instead of staying RUNNING forever (godlike/07
			// no-fake-availability).
			w.log.Error("artifact-producing job cannot complete via legacy Worker path — failing job",
				zap.String("job_id", j.ID),
				zap.String("job_type", j.Type),
				zap.Error(completeErr))
			if failErr := w.repo.Fail(finalizationCtx, j.ID, workerID, leaseID, finalRevision,
				fmt.Sprintf("legacy Worker cannot complete artifact-producing job %q: %v", j.Type, completeErr)); failErr != nil {
				if errors.Is(failErr, sqljobs.ErrLeaseLost) {
					w.log.Warn("lease lost during fail-after-artifact-gate",
						zap.String("job_id", j.ID))
				} else {
					w.log.Error("failed to mark artifact-producing job as failed",
						zap.String("job_id", j.ID),
						zap.Error(failErr))
				}
			}
		} else {
			w.log.Error("failed to mark job as completed",
				zap.String("job_id", j.ID),
				zap.Error(completeErr))
		}
	} else {
		w.log.Info("job completed", zap.String("job_id", j.ID))
	}
}
