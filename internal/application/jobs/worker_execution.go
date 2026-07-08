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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainremote "github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
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

// extractStagedArtifacts reads the __artifact_manifest from the handler
// result map and converts it to the JSON wire format consumed by
// CompleteWithArtifactsCommand.StagedArtifacts. When no manifest is
// present, returns an empty JSON array (byte-stable "no manifest" sentinel).
//
// The mapping from job.Artifact → finalization.PublishedArtifact:
//
//	id          → artifact_id
//	kind        → kind
//	filename    → filename
//	mime_type   → mime_type
//	size_bytes  → size_bytes
//	sha256      → sha256
//	local_path  → (discarded — the Sender never sees local paths)
//	required    → requirement (bool → ArtifactRequirement enum)
//	source      → source (PR-SOURCE-FIX: derived from jobType prefix)
func extractStagedArtifacts(result map[string]any, jobType string) json.RawMessage {
	manifest, err := job.Decode(result)
	if err != nil || manifest == nil || len(manifest.Artifacts) == 0 {
		return json.RawMessage("[]")
	}

	published := make([]finalization.PublishedArtifact, 0, len(manifest.Artifacts))
	// PR-SOURCE-FIX: derive source from job type prefix
	// ("script.generate" → "script", "image.generate.google" → "image", etc.).
	// Hoisted outside the loop — all artifacts in one manifest share the
	// same job type.
	src := ""
	if idx := strings.Index(jobType, "."); idx > 0 {
		src = jobType[:idx]
	}
	for _, a := range manifest.Artifacts {
		req := finalization.ArtifactRequirementOptional
		if a.Required {
			req = finalization.ArtifactRequirementRequired
		}
		published = append(published, finalization.PublishedArtifact{
			ArtifactID:     a.ID,
			Kind:           finalization.ArtifactKind(a.Kind),
			Filename:       a.Filename,
			MIMEType:       a.MIMEType,
			SizeBytes:      a.SizeBytes,
			SHA256:         a.SHA256,
			Requirement:    req,
			IdempotencyKey: a.ID,
			Source:         src,
		})
	}

	raw, err := json.Marshal(published)
	if err != nil {
		return json.RawMessage("[]")
	}
	return json.RawMessage(raw)
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

	// Step 8 (July 2026): emit "leased" event so the operator can
	// trace the full job lifecycle: queued → leased → ... → completed.
	// The enqueuer emits "queued"; this is the "leased" bookend.
	if err := w.repo.AddEvent(ctx, j.ID, "leased",
		fmt.Sprintf("job claimed by worker %s", w.id),
		map[string]any{
			"worker_id": w.id,
			"lease_id":  j.LeaseID,
			"revision":  j.Revision,
		}); err != nil {
		w.log.Warn("failed to record leased event",
			zap.String("job_id", j.ID),
			zap.Error(err))
	}

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
	defer func() {
		close(stopLease)
		<-leaseDone
	}()

	// Snapshot lease tokens for finalisation.
	workerID := w.id
	leaseID := j.LeaseID
	revision := j.Revision

	tools := &JobTools{
		Progress: func(progress int, message string) {
			// FASE 0.2 (July 4 2026) silent-drop rewrite per
			// PR-GODOBJ-14-WORKER-REGISTRY godlike/07 no-fake-availability:
			// pre-PR the log.Warn was the only observable signal; a DB
			// hiccup would log but the operator dashboard could not
			// quantify it. Post-PR we increment both
			// WorkerProgressEmittedTotal{outcome="error"} and
			// WorkerProgressErrorsTotal{reason="broker_emit_failed"}
			// so dashboards can alert on the failure rate. The log
			// is preserved for diagnostic-context value (job_id +
			// progress value + error chain).
			if err := w.repo.SetProgress(jobCtx, j.ID, progress, message); err != nil {
				w.log.Warn("failed to report progress",
					zap.String("job_id", j.ID),
					zap.Int("progress", progress),
					zap.Error(err))
				observability.WorkerProgressEmittedTotal.WithLabelValues(j.Type, "error").Inc()
				observability.WorkerProgressErrorsTotal.WithLabelValues(j.Type, "broker_emit_failed").Inc()
				return
			}
			observability.WorkerProgressEmittedTotal.WithLabelValues(j.Type, "success").Inc()
		},
		Event: func(eventType string, message string, data map[string]any) {
			// FASE 0.2 silent-drop rewrite: same reasoning as Progress
			// above; on AddEvent failure bump WorkerEventDropsTotal
			// with the canonical job_type label so dashboards can
			// alert per-job_type on silent event drops.
			if err := w.repo.AddEvent(jobCtx, j.ID, eventType, message, data); err != nil {
				w.log.Warn("failed to record event",
					zap.String("job_id", j.ID),
					zap.String("event_type", eventType),
					zap.Error(err))
				observability.WorkerEventDropsTotal.WithLabelValues(j.Type).Inc()
				return
			}
		},
		IsCancelled: func() bool {
			domJob, err := w.repo.Get(jobCtx, j.ID)
			if err != nil {
				// FASE 0.2 silent-drop rewrite: previously
				// swallowed err entirely (godlike/07 violation).
				// Post-PR we surface the IsCancelled check failure
				// via the counter (WorkerProgressErrors is the
				// canonical signal surface for runtime telemetry
				// failures) and fail-closed to `false` so a
				// transient broker/DB error does NOT prematurely
				// trip the cancellation branch.
				observability.WorkerProgressErrorsTotal.WithLabelValues(j.Type, "is_cancelled_check_failed").Inc()
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
			if retryErr := w.repo.ScheduleRetry(finalizationCtx, j.ID, workerID, leaseID, finalRevision, dispatchErr.Error(), backoff); retryErr != nil {
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
	// PR-WORKER-RUNNER-INPROCESS-MIGRATION (July 2026): artifact-
	// producing jobs MUST be completed via the typed CompletionPort
	// (broker.CompleteWithArtifacts) — NOT the legacy w.repo.Complete
	// path. The SQL-layer gate at
	// internal/infrastructure/database/sqlite/jobs/repository_lifecycle.go:115
	// returns the typed sentinel domainremote.ErrCompleteJobPathViolation
	// for artifact-producing jobs that attempt the legacy path, which
	// failed FASE B Smoke Test 9 (duplicate payload → same drive_file_id
	// across 2 runs; the SUCCEEDED transition never fired because the
	// asset's drive_file_id write requires a clean broker closure).
	// This branch routes those jobs through the typed narrow port,
	// unblocking the canonical mark-SUCCEEDED.
	//
	// godlike/06 SSOT: ProducesArtifacts lookup lives ONLY on the typed
	// JobTypeRegistry (reg.ProducesArtifacts) at
	// internal/application/jobs/registry.go; nil reg = legacy behaviour,
	// preserving existing test fixtures that don't build a registry.
	//
	// godlike/07 typed-error contract: nil broker + ProducesArtifacts=true
	// fails closed via w.repo.Fail with a diagnostic naming the
	// (.WithBroker) composition miss. NO silent fallback to legacy path;
	// the SQL-layer ErrCompleteJobPathViolation rejection is replaced
	// with a clean Fail row that surfaces the wiring gap in the audit
	// timeline.
	producesArtifacts := w.reg != nil && w.reg.ProducesArtifacts(j.Type)
	if producesArtifacts {
		if w.broker == nil {
			w.log.Error("artifact-producing job encountered without CompletionPort wired — failing job",
				zap.String("job_id", j.ID),
				zap.String("job_type", j.Type),
				zap.Error(fmt.Errorf("worker.CompletionPort unset (call WithBroker(cp) at composition time)")))
			if failErr := w.repo.Fail(finalizationCtx, j.ID, workerID, leaseID, finalRevision,
				fmt.Sprintf("worker.CompletionPort not wired for artifact-producing job %q; call WithBroker(cp) on the Worker constructor", j.Type)); failErr != nil {
				if errors.Is(failErr, sqljobs.ErrLeaseLost) {
					w.log.Warn("lease lost during fail-after-missing-broker",
						zap.String("job_id", j.ID))
				} else {
					w.log.Error("failed to mark artifact-producing job as failed (after missing-broker gate)",
						zap.String("job_id", j.ID),
						zap.Error(failErr))
				}
			}
			return
		}

		// Extract the artifact manifest from the handler result. Handlers
		// that produce files (script.generate, image.generate.google,
		// etc.) inject a __artifact_manifest key into the result map.
		// The worker extracts it here and passes it as StagedArtifacts
		// so the broker's CompleteWithArtifacts can persist the artifact
		// metadata atomically with the job SUCCEEDED transition.
		//
		// When no manifest is present, StagedArtifacts stays empty ([])
		// — the broker still marks the job SUCCEEDED via the single-TX
		// spine, just without artifact metadata.
		stagedArtifacts := extractStagedArtifacts(result, j.Type)

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

		if _, err := w.broker.CompleteWithArtifacts(finalizationCtx, cmd); err != nil {
			completionErr := fmt.Sprintf("CompletionPort.CompleteWithArtifacts failed for artifact-producing job %q: %v", j.Type, err)
			w.log.Error("failed to mark artifact-producing job as completed via CompletionPort — failing job",
				zap.String("job_id", j.ID),
				zap.String("job_type", j.Type),
				zap.Error(err))
			// PR-COMPLETE-WORKER-BROAD-FIX (July 2026): the pre-PR code
			// silently logged the error and returned, leaving the job
			// RUNNING forever. The canonical fix is to fail the job
			// with a diagnostic naming the CompletionPort failure so
			// the operator can see WHY the job never reached SUCCEEDED.
			if failErr := w.repo.Fail(finalizationCtx, j.ID, workerID, leaseID, finalRevision,
				completionErr); failErr != nil {
				if errors.Is(failErr, sqljobs.ErrLeaseLost) {
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
			if dlqErr := w.repo.DeadLetter(finalizationCtx, j.ID, completionErr); dlqErr != nil {
				w.log.Warn("failed to dead-letter job after CompletionPort error",
					zap.String("job_id", j.ID), zap.Error(dlqErr))
			}
		} else {
			w.log.Info("job completed with artifacts",
				zap.String("job_id", j.ID),
				zap.String("job_type", j.Type))
		}
		return
	}

	if completeErr := w.repo.Complete(finalizationCtx, j.ID, workerID, leaseID, finalRevision, mapToRawMessage(result)); completeErr != nil {
		if errors.Is(completeErr, sqljobs.ErrLeaseLost) {
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
