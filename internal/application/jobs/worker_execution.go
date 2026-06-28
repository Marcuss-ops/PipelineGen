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
// Mechanical split, zero behavior change. ONLY relocated + import-redistributed.
package jobs

import (
	"context"
	"errors"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
	"go.uber.org/zap"
)

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
			if retryErr := w.repo.ScheduleRetry(finalizationCtx, j.ID, workerID, leaseID, revision, backoff); retryErr != nil {
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

		if failErr := w.repo.Fail(finalizationCtx, j.ID, workerID, leaseID, revision, dispatchErr.Error()); failErr != nil {
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

	if completeErr := w.repo.Complete(finalizationCtx, j.ID, workerID, leaseID, revision, mapToRawMessage(result)); completeErr != nil {
		if errors.Is(completeErr, sqljobs.ErrLeaseLost) {
			w.log.Warn("lease lost during complete — another worker claimed this job",
				zap.String("job_id", j.ID))
		} else {
			w.log.Error("failed to mark job as completed",
				zap.String("job_id", j.ID),
				zap.Error(completeErr))
		}
	} else {
		w.log.Info("job completed", zap.String("job_id", j.ID))
	}
}
