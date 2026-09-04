// Package jobs — worker terminal-state orchestration.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	jobscheduling "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/scheduling"
	domainremote "github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"go.uber.org/zap"
)

func (w *Worker) finalizeJobDispatchError(ctx context.Context, j *job.Job, workerID, leaseID string, finalRevision int, dispatchErr error) {
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
	}
}
