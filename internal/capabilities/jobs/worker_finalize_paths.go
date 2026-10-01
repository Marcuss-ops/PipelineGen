// Package jobs — worker terminal-state orchestration.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	jobscheduling "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/scheduling"
	domainremote "github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"go.uber.org/zap"
)

func (w *Worker) finalizeJobDispatchError(ctx context.Context, j *job.Job, workerID, leaseID string, finalRevision int, dispatchErr error) {
	// A DEFERRAL is checked FIRST, before any failure logging or retry
	// classification: the handler is not reporting a failure, it is reporting a
	// WAIT (job.DeferredAfter — a remote render still running, a provider
	// window not open). Classifying it as a failure is what turns a long
	// external wait into a terminal FAILED job, and what spends a retry on work
	// that never failed. The canonical finalize outcome keeps the row
	// non-terminal and leaves retry_count untouched; the row's deferred_until
	// carries when it may come back (honoured by the requeue sweep).
	if deferral, ok := job.AsDeferral(dispatchErr); ok {
		w.finalizeJobDeferral(ctx, j, workerID, leaseID, finalRevision, deferral)
		return
	}

	w.log.Error("job failed", zap.String("job_id", j.ID), zap.Error(dispatchErr))

	if retry.IsTransient(dispatchErr) && jobscheduling.DecideRetry(j) == jobscheduling.RetryScheduled {
		backoff := jobscheduling.RetryBackoff(j.RetryCount, jobscheduling.DefaultRetryPolicy)
		w.log.Info("scheduling job for retry", zap.String("job_id", j.ID), zap.Duration("backoff", backoff))
		if retryErr := w.repo.ScheduleRetry(ctx, j.ID, workerID, leaseID, finalRevision, dispatchErr.Error(), backoff); retryErr != nil {
			if errors.Is(retryErr, job.ErrLeaseLost) {
				w.log.Warn("lease lost during ScheduleRetry — another worker claimed this job", zap.String("job_id", j.ID))
			} else {
				w.log.Error("failed to schedule retry", zap.String("job_id", j.ID), zap.Error(retryErr))
			}
		}
		return
	}

	if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision, dispatchErr.Error()); failErr != nil {
		if errors.Is(failErr, job.ErrLeaseLost) {
			w.log.Warn("lease lost during fail (exhausted retries)", zap.String("job_id", j.ID))
		} else {
			w.log.Error("failed to mark job as failed", zap.String("job_id", j.ID), zap.Error(failErr))
		}
	}
	if dlqErr := w.repo.DeadLetter(ctx, j.ID, dispatchErr.Error()); dlqErr != nil {
		w.log.Warn("failed to dead-letter job", zap.String("job_id", j.ID), zap.Error(dlqErr))
	} else {
		w.log.Warn("job moved to dead letter queue", zap.String("job_id", j.ID), zap.Int("retry_count", j.RetryCount), zap.Error(dispatchErr))
	}
}

// finalizeJobDeferral persists a WAIT as the canonical deferred outcome: the
// row returns to RETRY_WAIT with retry_count UNCHANGED and deferred_until set,
// so the requeue sweep re-dispatches it when the wait is over. It is a single
// place by construction — every deferral the worker ever persists goes through
// here, so the outcome, the delay and the audit event cannot drift apart.
//
// Failure to persist is LOUD and terminal-by-omission-free: the job keeps its
// lease until it expires, the reaper requeues it, and the next attempt re-runs
// the handler (for a settle that re-reads the same durable render handle, that
// is a harmless re-poll). It is never silently reported as completed.
func (w *Worker) finalizeJobDeferral(ctx context.Context, j *job.Job, workerID, leaseID string, finalRevision int, deferral *job.Deferral) {
	// The row MUST carry the instant it may come back (the store rejects a
	// deferral without one), so the deployment default lives here where the
	// scheduling policy already lives — a handler states the FACT (I am
	// waiting), never the cadence.
	delay := jobscheduling.DefaultDeferralDelay
	reason := "handler deferred its attempt"
	if deferral != nil {
		if deferral.Delay > 0 {
			delay = deferral.Delay
		}
		if strings.TrimSpace(deferral.Reason) != "" {
			reason = deferral.Reason
		}
	}
	w.log.Info("job deferred — handing the attempt back without spending a retry",
		zap.String("job_id", j.ID), zap.String("job_type", j.Type),
		zap.Duration("delay", delay), zap.String("reason", reason))

	result, err := w.repo.FinalizeAttempt(ctx, job.FinalizeAttemptCommand{
		JobID:            j.ID,
		Outcome:          job.OutcomeDeferred,
		WorkerID:         workerID,
		LeaseID:          leaseID,
		ExpectedRevision: finalRevision,
		ErrorMessage:     reason,
		Backoff:          delay,
		EventType:        "job_deferred",
		EventData: map[string]any{
			"kind":        j.Type,
			"sub_kind":    "job.defer",
			"reason":      reason,
			"delay_ms":    delay.Milliseconds(),
			"retry_count": j.RetryCount,
		},
	})
	if err != nil {
		if errors.Is(err, job.ErrLeaseLost) || errors.Is(err, job.ErrTransitionConflict) {
			w.log.Warn("lease lost while deferring — another worker owns this job",
				zap.String("job_id", j.ID), zap.Error(err))
			return
		}
		w.log.Error("failed to persist job deferral — the job stays leased until the reaper requeues it",
			zap.String("job_id", j.ID), zap.Error(err))
		return
	}
	// A deferred job is still RUNNING from the operator's point of view (it is
	// non-terminal and the Master keeps working on the remote side), so the
	// calendar card keeps its RUNNING status and only the phase changes. The
	// deferral is deliberately NOT reported as a failure: a wait that looks like
	// an incident is how real incidents get ignored.
	w.reportCalendar(j.ID, j.Type, "RUNNING", "deferred", nil, nil)
	observability.WorkerJobDeferredTotal.WithLabelValues(j.Type, string(result.FinalStatus)).Inc()
}

func (w *Worker) finalizeJobArtifactPath(ctx context.Context, j *job.Job, workerID, leaseID string, finalRevision int, result map[string]any) []string {
	if w.broker == nil {
		w.log.Error("artifact-producing job encountered without CompletionPort wired — failing job",
			zap.String("job_id", j.ID), zap.String("job_type", j.Type),
			zap.Error(fmt.Errorf("worker.CompletionPort unset (call WithBroker(cp) at composition time)")))
		if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision,
			fmt.Sprintf("worker.CompletionPort not wired for artifact-producing job %q; call WithBroker(cp) on the Worker constructor", j.Type)); failErr != nil {
			if errors.Is(failErr, job.ErrLeaseLost) {
				w.log.Warn("lease lost during fail-after-missing-broker", zap.String("job_id", j.ID))
			} else {
				w.log.Error("failed to mark artifact-producing job as failed (after missing-broker gate)", zap.String("job_id", j.ID), zap.Error(failErr))
			}
		}
		return nil
	}

	stagedArtifacts, extractErr := extractStagedArtifacts(result, j.Type)
	if extractErr != nil {
		manifestErr := fmt.Sprintf("artifact manifest extract failed for artifact-producing job %q: %v", j.Type, extractErr)
		w.log.Error("worker: artifact manifest extract failed — failing job (FASE 1 c typed-error contract)",
			zap.String("job_id", j.ID), zap.String("job_type", j.Type), zap.Error(extractErr))
		if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision, manifestErr); failErr != nil {
			if errors.Is(failErr, job.ErrLeaseLost) {
				w.log.Warn("lease lost during fail-after-manifest-extract-error", zap.String("job_id", j.ID))
			} else {
				w.log.Error("failed to mark artifact-producing job as failed (after manifest extract error)", zap.String("job_id", j.ID), zap.Error(failErr))
			}
		}
		if dlqErr := w.repo.DeadLetter(ctx, j.ID, manifestErr); dlqErr != nil {
			w.log.Warn("failed to dead-letter job after manifest extract error", zap.String("job_id", j.ID), zap.Error(dlqErr))
		}
		return nil
	}

	cmd := CompleteWithArtifactsCommand{
		WorkerID:         w.id,
		WorkerSessionID:  "",
		JobID:            j.ID,
		LeaseID:          leaseID,
		ExpectedRevision: finalRevision,
		CorrelationID:    j.CorrelationID,
		ResultData:       mapToRawMessage(result),
		StagedArtifacts:  stagedArtifacts,
		OutboxEvents:     nil,
	}

	// Storage-specific contention is classified before crossing the CompletionPort
	// boundary. The worker retries only the canonical typed transient contract.
	var canonicalAssetIDs []string
	completionErr := retry.Do(ctx, func() error {
		ids, err := w.broker.CompleteWithArtifacts(ctx, cmd)
		if err == nil {
			canonicalAssetIDs = ids
			return nil
		}
		if retry.IsTransient(err) {
			observability.WorkerFinalizationDBLockedTotal.WithLabelValues(j.Type, "retried").Inc()
		}
		return err
	}, retry.Options{
		MaxAttempts:    5,
		InitialBackoff: 200 * time.Millisecond,
		MaxBackoff:     2 * time.Second,
		BackoffFactor:  2.0,
		DisableJitter:  true,
		IsRetryable:    retry.IsTransient,
		OnRetry: func(attempt int, err error) {
			w.log.Warn("finalization: transient storage contention — retrying CompleteWithArtifacts",
				zap.String("job_id", j.ID), zap.String("job_type", j.Type),
				zap.Int("retry_attempt", attempt+1), zap.Error(err))
		},
	})
	if completionErr != nil {
		if retry.IsTransient(completionErr) {
			observability.WorkerFinalizationDBLockedTotal.WithLabelValues(j.Type, "terminal").Inc()
		}
		diagnostic := fmt.Sprintf("CompletionPort.CompleteWithArtifacts failed for artifact-producing job %q: %v", j.Type, completionErr)
		w.log.Error("failed to mark artifact-producing job as completed via CompletionPort — failing job",
			zap.String("job_id", j.ID), zap.String("job_type", j.Type), zap.Error(completionErr))
		if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision, diagnostic); failErr != nil {
			if errors.Is(failErr, job.ErrLeaseLost) {
				w.log.Warn("lease lost during fail-after-completion-error", zap.String("job_id", j.ID))
			} else {
				w.log.Error("failed to mark artifact-producing job as failed (after CompletionPort error)", zap.String("job_id", j.ID), zap.Error(failErr))
			}
		}
		if dlqErr := w.repo.DeadLetter(ctx, j.ID, diagnostic); dlqErr != nil {
			w.log.Warn("failed to dead-letter job after CompletionPort error", zap.String("job_id", j.ID), zap.Error(dlqErr))
		}
	} else {
		w.log.Info("job completed with artifacts", zap.String("job_id", j.ID), zap.String("job_type", j.Type))
		// The child is durable and terminal — the only moment its parent can
		// become eligible. Finalise it now instead of waiting for the sweep.
		w.notifyParentCompletion(ctx, j)
	}
	return canonicalAssetIDs
}

func (w *Worker) finalizeJobLegacyComplete(ctx context.Context, j *job.Job, workerID, leaseID string, finalRevision int, result map[string]any) {
	if completeErr := w.repo.Complete(ctx, j.ID, workerID, leaseID, finalRevision, mapToRawMessage(result)); completeErr != nil {
		if errors.Is(completeErr, job.ErrLeaseLost) {
			w.log.Warn("lease lost during complete — another worker claimed this job", zap.String("job_id", j.ID))
		} else if errors.Is(completeErr, domainremote.ErrCompleteJobPathViolation) {
			w.log.Error("artifact-producing job cannot complete via legacy Worker path — failing job",
				zap.String("job_id", j.ID), zap.String("job_type", j.Type), zap.Error(completeErr))
			if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision,
				fmt.Sprintf("legacy Worker cannot complete artifact-producing job %q: %v", j.Type, completeErr)); failErr != nil {
				if errors.Is(failErr, job.ErrLeaseLost) {
					w.log.Warn("lease lost during fail-after-artifact-gate", zap.String("job_id", j.ID))
				} else {
					w.log.Error("failed to mark artifact-producing job as failed", zap.String("job_id", j.ID), zap.Error(failErr))
				}
			}
		} else {
			w.log.Error("failed to mark job as completed", zap.String("job_id", j.ID), zap.Error(completeErr))
		}
	} else {
		w.log.Info("job completed", zap.String("job_id", j.ID))
		// The child is durable and terminal — the only moment its parent can
		// become eligible. Finalise it now instead of waiting for the sweep.
		w.notifyParentCompletion(ctx, j)
	}
}
