// Package jobs — worker_finalize_paths.go (worker_execution_result.go
// sub-section split, July 2026).
//
// Owns the three terminal-state path helpers called by
// worker_execution_result.go::finalizeJob:
//
//  1. finalizeJobDispatchError — the dispatchErr != nil path:
//     ScheduleRetry (retryable, RetryCount < MaxRetries) or
//     Fail + DeadLetter (exhausted retries).
//
//  2. finalizeJobArtifactPath — the ProducesArtifacts=true path:
//     fail-closed on nil broker, FASE 1 typed manifest contract
//     (Fail + DeadLetter on extract error), and the typed
//     CompletionPort CompleteWithArtifacts call with the
//     PR-COMPLETE-WORKER-BROAD-FIX fail-closed branch.
//
//  3. finalizeJobLegacyComplete — the ProducesArtifacts=false path:
//     legacy w.repo.Complete with lease-lost / path-violation /
//     generic-error handling.
//
// Mechanical split from worker_execution_result.go. Zero behavior
// change: every log line, error string, CAS call and lease-lost guard
// is byte-identical to the pre-split flat body.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"

	domainremote "github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"go.uber.org/zap"
)

// finalizeJobDispatchError handles the dispatchErr != nil terminal path:
// retryable jobs (RetryCount < MaxRetries) are re-scheduled with
// server-side backoff via available_at; exhausted retries are failed +
// dead-lettered (terminal). Lease-loss during any finalisation SQL
// UPDATE logs warn rather than re-raising — another worker already
// CAS-won the row.
func (w *Worker) finalizeJobDispatchError(ctx context.Context, j *job.Job, workerID, leaseID string, finalRevision int, dispatchErr error) {
	w.log.Error("job failed",
		zap.String("job_id", j.ID),
		zap.Error(dispatchErr))

	// A dispatch error is retryable only when its typed error contract
	// explicitly identifies it as transient. Deterministic request/source
	// failures (for example a missing clip in clip_only mode) must become
	// terminal immediately; otherwise the broker exposes RETRY_WAIT for a
	// permanent failure and can later re-run the same invalid request.
	if retry.IsTransient(dispatchErr) && j.RetryCount < j.MaxRetries {
		// Backoff math now routes through pkg/retry.BackoffFor
		// (the canonical owner of "compute exponential backoff" —
		// godlike/06 SSOT, see pkg/retry/options.go godlike/06
		// block). 2s × 2^RetryCount capped at 30s — byte-equivalent
		// with the pre-migration bitwise math, but the cap now
		// lives at `MaxBackoff` in the canonical Options literal
		// instead of an inlined `if backoff > 30*time.Second`
		// post-clamp. JitterFraction defaults to 0 in a struct
		// literal so determinism for the server-side `available_at`
		// schedule is preserved (the SQL stored timestamp is the
		// persisted retry target, NEVER a random pre-sleep).
		backoff := retry.BackoffFor(j.RetryCount, retry.Options{
			InitialBackoff: 2 * time.Second,
			BackoffFactor:  2.0,
			MaxBackoff:     30 * time.Second,
		})
		w.log.Info("scheduling job for retry",
			zap.String("job_id", j.ID),
			zap.Duration("backoff", backoff))

		// ScheduleRetry does running→queued atomically with
		// server-side backoff via available_at. No intermediate
		// "failed" state — avoids false alerting.
		if retryErr := w.repo.ScheduleRetry(ctx, j.ID, workerID, leaseID, finalRevision, dispatchErr.Error(), backoff); retryErr != nil {
			if errors.Is(retryErr, job.ErrLeaseLost) {
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

	if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision, dispatchErr.Error()); failErr != nil {
		if errors.Is(failErr, job.ErrLeaseLost) {
			w.log.Warn("lease lost during fail (exhausted retries)",
				zap.String("job_id", j.ID))
		} else {
			w.log.Error("failed to mark job as failed",
				zap.String("job_id", j.ID),
				zap.Error(failErr))
		}
	}
	if dlqErr := w.repo.DeadLetter(ctx, j.ID, dispatchErr.Error()); dlqErr != nil {
		w.log.Warn("failed to dead-letter job", zap.String("job_id", j.ID), zap.Error(dlqErr))
	} else {
		w.log.Warn("job moved to dead letter queue",
			zap.String("job_id", j.ID),
			zap.Int("retry_count", j.RetryCount),
			zap.Error(dispatchErr))
	}
}

// finalizeJobArtifactPath handles the ProducesArtifacts=true terminal
// path. The typed CompletionPort (broker.CompleteWithArtifacts) is the
// ONLY way an artifact-producing job reaches SUCCEEDED:
//
//   - nil broker → fail-closed (godlike/07 — the composition miss is
//     surfaced in the audit timeline, never silently downgraded to the
//     legacy path).
//   - extractStagedArtifacts error → Fail + DeadLetter (FASE 1 c typed
//     manifest contract: a malformed manifest MUST NOT reach SUCCEEDED).
//   - CompleteWithArtifacts error → Fail + DeadLetter
//     (PR-COMPLETE-WORKER-BROAD-FIX: pre-PR code silently logged and
//     returned, leaving the job RUNNING forever).
func (w *Worker) finalizeJobArtifactPath(ctx context.Context, j *job.Job, workerID, leaseID string, finalRevision int, result map[string]any) []string {
	if w.broker == nil {
		w.log.Error("artifact-producing job encountered without CompletionPort wired — failing job",
			zap.String("job_id", j.ID),
			zap.String("job_type", j.Type),
			zap.Error(fmt.Errorf("worker.CompletionPort unset (call WithBroker(cp) at composition time)")))
		if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision,
			fmt.Sprintf("worker.CompletionPort not wired for artifact-producing job %q; call WithBroker(cp) on the Worker constructor", j.Type)); failErr != nil {
			if errors.Is(failErr, job.ErrLeaseLost) {
				w.log.Warn("lease lost during fail-after-missing-broker",
					zap.String("job_id", j.ID))
			} else {
				w.log.Error("failed to mark artifact-producing job as failed (after missing-broker gate)",
					zap.String("job_id", j.ID),
					zap.Error(failErr))
			}
		}
		return nil
	}

	// Extract the artifact manifest from the handler result. Handlers
	// that produce files (script.generate, image.generate.google,
	// etc.) inject a __artifact_manifest key into the result map.
	// The worker extracts it here and passes it as StagedArtifacts
	// so the broker's CompleteWithArtifacts can persist the artifact
	// metadata atomically with the job SUCCEEDED transition.
	//
	// FASE 1 (c) — typed-error contract: a manifest decode/marshal
	// failure surfaces a typed job.ErrArtifactManifestInvalid. The
	// decode-error / marshal-error path FAILS the job (audit 2026-07-03
	// P0 #4 criterion "il manifest non è decodificabile") — a
	// malformed manifest MUST NOT silently reach SUCCEEDED.
	//
	// Empty-but-valid manifests (returned as json.RawMessage("[]"))
	// still ride the normal CompleteWithArtifacts path.
	stagedArtifacts, extractErr := extractStagedArtifacts(result, j.Type)
	if extractErr != nil {
		// FASE 1 (c): the typed manifest error is a hard handler
		// fault (decode/marshal failure). Mirror the CompletionPort
		// error branch: fail the job + dead-letter, so a malformed
		// manifest is observable in the audit timeline and the broker
		// never marks SUCCEEDED.
		manifestErr := fmt.Sprintf("artifact manifest extract failed for artifact-producing job %q: %v", j.Type, extractErr)
		w.log.Error("worker: artifact manifest extract failed — failing job (FASE 1 c typed-error contract)",
			zap.String("job_id", j.ID),
			zap.String("job_type", j.Type),
			zap.Error(extractErr))
		if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision, manifestErr); failErr != nil {
			if errors.Is(failErr, job.ErrLeaseLost) {
				w.log.Warn("lease lost during fail-after-manifest-extract-error",
					zap.String("job_id", j.ID))
			} else {
				w.log.Error("failed to mark artifact-producing job as failed (after manifest extract error)",
					zap.String("job_id", j.ID),
					zap.Error(failErr))
			}
		}
		if dlqErr := w.repo.DeadLetter(ctx, j.ID, manifestErr); dlqErr != nil {
			w.log.Warn("failed to dead-letter job after manifest extract error",
				zap.String("job_id", j.ID), zap.Error(dlqErr))
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

	// The finalization transaction (broker.CompleteWithArtifacts →
	// JobFinalizer) runs under SQLite WAL single-writer semantics. A
	// concurrent writer (Drive publication, outbox pool, maintenance WAL
	// checkpoint) can make the finalizer's media_assets upsert fail with
	// SQLITE_BUSY/SQLITE_LOCKED ("database is locked") — a transient
	// contention that resolves once the other writer commits. Retry a
	// bounded number of times instead of treating it as terminal: the
	// pre-fix behaviour failed BOTH CompleteWithArtifacts AND the Fail
	// fallback with the same lock, leaving the job orphaned in RUNNING
	// until the 5-minute lease scanner requeued it (~9 min wall).
	var canonicalAssetIDs []string
	var completionErr error
	completionErr = retry.Do(ctx, func() error {
		ids, err := w.broker.CompleteWithArtifacts(ctx, cmd)
		if err == nil {
			canonicalAssetIDs = ids
			return nil
		}
		if isSQLiteBusy(err) {
			observability.WorkerFinalizationDBLockedTotal.WithLabelValues(j.Type, "retried").Inc()
		}
		return err
	}, retry.Options{
		MaxAttempts:    5,
		InitialBackoff: 200 * time.Millisecond,
		MaxBackoff:     2 * time.Second,
		BackoffFactor:  2.0,
		DisableJitter:  true,
		IsRetryable:    isSQLiteBusy,
		OnRetry: func(attempt int, err error) {
			w.log.Warn("finalization: database is locked — retrying CompleteWithArtifacts",
				zap.String("job_id", j.ID),
				zap.String("job_type", j.Type),
				zap.Int("retry_attempt", attempt+1),
				zap.Error(err))
		},
	})
	if completionErr != nil {
		if isSQLiteBusy(completionErr) {
			observability.WorkerFinalizationDBLockedTotal.WithLabelValues(j.Type, "terminal").Inc()
		}
		diagnostic := fmt.Sprintf("CompletionPort.CompleteWithArtifacts failed for artifact-producing job %q: %v", j.Type, completionErr)
		w.log.Error("failed to mark artifact-producing job as completed via CompletionPort — failing job",
			zap.String("job_id", j.ID),
			zap.String("job_type", j.Type),
			zap.Error(completionErr))
		// PR-COMPLETE-WORKER-BROAD-FIX (July 2026): the pre-PR code
		// silently logged the error and returned, leaving the job
		// RUNNING forever. The canonical fix is to fail the job
		// with a diagnostic naming the CompletionPort failure so
		// the operator can see WHY the job never reached SUCCEEDED.
		if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision,
			diagnostic); failErr != nil {
			if errors.Is(failErr, job.ErrLeaseLost) {
				w.log.Warn("lease lost during fail-after-completion-error",
					zap.String("job_id", j.ID))
			} else {
				w.log.Error("failed to mark artifact-producing job as failed (after CompletionPort error)",
					zap.String("job_id", j.ID),
					zap.Error(failErr))
			}
		}
		// DeadLetter for audit-trail completeness — mirrors the
		// dispatchErr exhausted-retries path (code-review, July 2026).
		if dlqErr := w.repo.DeadLetter(ctx, j.ID, diagnostic); dlqErr != nil {
			w.log.Warn("failed to dead-letter job after CompletionPort error",
				zap.String("job_id", j.ID), zap.Error(dlqErr))
		}
	} else {
		w.log.Info("job completed with artifacts",
			zap.String("job_id", j.ID),
			zap.String("job_type", j.Type))
	}
	return canonicalAssetIDs
}

// finalizeJobLegacyComplete handles the ProducesArtifacts=false terminal
// path via the legacy w.repo.Complete call. Lease-loss logs warn (the
// next worker's transition is authoritative); the FASE 0.1
// domainremote.ErrCompleteJobPathViolation sentinel fails the job toward
// a terminal state instead of leaving it RUNNING forever; generic
// errors log without re-raising.
func (w *Worker) finalizeJobLegacyComplete(ctx context.Context, j *job.Job, workerID, leaseID string, finalRevision int, result map[string]any) {
	if completeErr := w.repo.Complete(ctx, j.ID, workerID, leaseID, finalRevision, mapToRawMessage(result)); completeErr != nil {
		if errors.Is(completeErr, job.ErrLeaseLost) {
			w.log.Warn("lease lost during complete — another worker claimed this job",
				zap.String("job_id", j.ID))
		} else if errors.Is(completeErr, domainremote.ErrCompleteJobPathViolation) {
			// FASE 0.1 (July 4 2026): the legacy Worker path cannot
			// call CompleteWithArtifacts — the job.Store interface has
			// no such method. The canonical typed sentinel
			// domainremote.ErrCompleteJobPathViolation (per godlike/06
			// SSOT at internal/domain/remote/complete_job.go) gates the
			// typoevolee, so this branch fails the job toward a
			// terminal state instead of staying RUNNING forever
			// (godlike/07 no-fake-availability).
			w.log.Error("artifact-producing job cannot complete via legacy Worker path — failing job",
				zap.String("job_id", j.ID),
				zap.String("job_type", j.Type),
				zap.Error(completeErr))
			if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision,
				fmt.Sprintf("legacy Worker cannot complete artifact-producing job %q: %v", j.Type, completeErr)); failErr != nil {
				if errors.Is(failErr, job.ErrLeaseLost) {
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

// isSQLiteBusy reports whether err is (or wraps) a mattn/go-sqlite3
// SQLITE_BUSY / SQLITE_LOCKED error — the canonical "database is locked"
// transient shape. Typed probe (errors.As on the driver's Error value or
// pointer), NOT substring matching, mirroring the typed-probe convention
// already used by enqueue_service.go's UNIQUE-constraint rescue. The
// finalizer wraps the driver error with %w, so errors.As walks the
// full "...: upsert media_assets: database is locked" chain.
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	var value sqlite3.Error
	if errors.As(err, &value) {
		return value.Code == sqlite3.ErrBusy || value.Code == sqlite3.ErrLocked
	}
	var ptr *sqlite3.Error
	if errors.As(err, &ptr) && ptr != nil {
		return ptr.Code == sqlite3.ErrBusy || ptr.Code == sqlite3.ErrLocked
	}
	return false
}
