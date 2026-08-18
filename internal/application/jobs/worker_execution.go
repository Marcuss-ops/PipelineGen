// Package jobs — worker_execution.go (per-job orchestration envelope,
// July 2026; split out of the pre-PR7 monolithic worker.go).
//
// Owns:
//
//  1. func (w *Worker) runJob — the per-job dispatcher envelope:
//
//     parent ctx
//     → correlation-id enriched ctx                (corid)
//     → emit "leased" event                         (worker.go::Start booked
//     with the canonical
//     "queued" bookend; this
//     is the "leased" one)
//     → timeout-bounded jobCtx                      (HC-1 Registry lookup via
//     w.jobTimeoutFor(j.Type))
//     → lease-renewal goroutine                     (FASE 4(b) typed
//     LeaseState propagation;
//     see
//     worker_execution_heartbeat.go)
//     → Dispatcher.Dispatch(jobCtx, j, tools)
//     → AGENTS.md-allowlisted finalizationCtx       (the canonical
//     context.WithTimeout(
//     context.Background(),
//     finalizationTimeout) site — see below)
//     → worker_execution_result.go::finalizeJob     (4 terminal-state paths)
//     → defers unwind (jobCancel, stopLease, finalCancel)
//
// CRITICAL INVARIANT — finalizationCtx (AGENTS.md §context-util-table
// allowlist, MUST-stay-byte-for-byte across the 2026-07 file split):
//
//	finalizationCtx, finalCancel := context.WithTimeout(
//	    context.Background(), finalizationTimeout)
//	defer finalCancel()
//
// This is one of the AGENTS.md context-util-table explicitly
// allowlisted `context.Background()` sites. The purpose is to
// survive jobCtx cancellation so the terminal writes (the DB flip
// AND the artifact-publication spine — script.json, scenes.json,
// per-scene voiceovers, ... to their destination) can complete even
// when the worker is mid-shutdown. Detaching from jobCtx (rather
// than from ctx / worker lifecycle) prevents losing outcome
// persistence when jobCtx is cancelled by either timeout or by the
// outer worker Stop.
//
// The bound (finalizationTimeout) must cover publishing EVERY
// staged artifact to Drive: artifact-producing jobs scale with
// artifact count (a 46-clip run publishes 48 artifacts at ~2.5s of
// sequential Drive I/O each ≈ 2 minutes), so the legacy 30s bound
// that only covered the DB flip would fail mid-publication. The
// bound keeps shutdown bounded while staying far below the per-job
// timeout (30–60m).
//
// The finalizationCtx IS PASSED to worker_execution_result.go's
// finalizeJob as the ctx parameter. finalizeJob does NOT
// reconstruct it. This preserves the PR7 invariant end-to-end.
//
// FASE 4(b) (July 2026) — startCancelWatcher REMOVED: the
// pre-Fase-4 2-second IsCancelled-poll goroutine is gone.
// Cancellation now propagates through the typed
// kerneljob.RenewLeaseResult.State return value (Continue |
// CancelRequested | LeaseLost) on every lease-renewal tick —
// see worker_execution_heartbeat.go::renewLeaseLoopWith. The
// renew-loop observes LeaseStateCancelRequested and calls
// jobCancel on the worker jobCtx. Handlers that poll ctx.Err()
// at phase boundaries short-circuit the same way they did under
// the pre-Fase-4 polling model; the canonical propagation seam
// is now native context cancellation rather than a callback.
//
// Mechanical split locating the 4 finalisation paths in
// worker_execution_result.go::finalizeJob. Zero behavior change.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
	"go.uber.org/zap"
)

// finalizationTimeout bounds the broker-side finalize phase
// (runJob → finalizeJob). It covers the terminal DB flip AND the
// artifact-publication spine (CompleteWithArtifacts), which publishes
// every staged artifact (script.json, scenes.json, per-scene voiceovers,
// ...) to Drive sequentially. Artifact count scales with the job: a
// 46-clip run publishes 48 artifacts at ~2.5s each ≈ 2 minutes, so the
// legacy 30s bound (pre-artifact-publication) would fail mid-publish.
// 10 minutes covers 46+ artifact runs with retry headroom while keeping
// worker shutdown bounded and far below the per-job timeout (30–60m).
const finalizationTimeout = 10 * time.Minute

func (w *Worker) runJob(parent context.Context, j *job.Job) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	if j.CorrelationID != "" {
		ctx = corid.WithCorrelationID(ctx, j.CorrelationID)
	}

	// FASE 2 observability (kernel/observability): every claim is one
	// Run (= one attempt). queue_wait_ms = claim-time started_at −
	// enqueue created_at; the per-attempt token is the canonical lease
	// (the runtime has no separate attempt_id table). The run is bound
	// to ctx so handlers and adapters downstream can record stages /
	// operations via MeasureStage / MeasureOperation; the run itself is
	// finished in the deferred closure with the attempt outcome. The
	// recorder/collector sink receives the parent ctx (not the
	// timeout-bounded jobCtx) so final writes survive jobCtx
	// cancellation.
	var (
		run         *kernobs.Run
		dispatchErr error
	)
	if w.observer != nil {
		// The lease fence MUST be surfaced on the run: RecoverAbandoned
		// (run_recorder.go) only reclaims RUNNING runs whose
		// lease_expires_at has a non-NULL past value. Without LeaseID /
		// WorkerID / LeaseExpiresAt here, lease_expires_at stays NULL for
		// every run and a worker crash can never be recovered into
		// ABANDONED.
		leaseExpiry := time.Time{}
		if j.LeaseExpiry != nil {
			leaseExpiry = *j.LeaseExpiry
		}
		run = w.observer.StartRunForClaim(parent, kernobs.ClaimRunInfo{
			JobID:          j.ID,
			JobType:        j.Type,
			AttemptID:      kernobs.NewAttemptID(), // persistent execution identity; LeaseID remains the worker fence
			LeaseID:        j.LeaseID,
			WorkerID:       w.id,
			LeaseExpiresAt: leaseExpiry,
			CreatedAt:      j.CreatedAt,
			StartedAt:      j.StartedAt,
			ParentJobID:    job.ParentLinkFromPayload(j.Payload).ParentJobID,
			ParentRunID:    job.ParentLinkFromPayload(j.Payload).ParentRunID,
			RetryCount:     j.RetryCount,
		})
		ctx = kernobs.WithRun(ctx, run)
		defer func() {
			if run == nil {
				return
			}
			if rec := recover(); rec != nil {
				run.FinishWithPanic(rec)
				panic(rec)
			}
			if dispatchErr != nil {
				run.FinishWithError(dispatchErr)
			} else {
				run.Finish()
			}
		}()
	}

	claimAt := time.Now().UTC()
	w.log.Info("running job",
		zap.String("job_id", j.ID),
		zap.String("type", j.Type),
		zap.String("correlation_id", j.CorrelationID),
		zap.String("lease_id", j.LeaseID),
		zap.Int("revision", j.Revision),
		zap.Time("lease_acquired_at", claimAt),
	)

	ledger := NewJobRegistryRecorder(w.jobLedger, w.log)
	attemptID := ""
	if run != nil {
		if report := run.Report(); report != nil {
			attemptID = report.AttemptID
		}
	}
	if attemptID == "" {
		attemptID = fmt.Sprintf("%s:%d", j.ID, j.Revision)
	}
	stepID := ledger.Start(ctx, j, w.id, attemptID)
	defer func() {
		if rec := recover(); rec != nil {
			ledger.Finish(context.Background(), j, stepID, w.id, attemptID, "FAILED", nil, fmt.Errorf("panic in worker execution: %v", rec), nil)
			panic(rec)
		}
	}()

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

	// Lease renewal — FASE 4(b) typed LeaseState integration.
	// The renew-loop in worker_execution_heartbeat.go inspects the
	// typed kerneljob.RenewLeaseResult.State on every tick and calls
	// jobCancel on LeaseStateCancelRequested. This replaces the
	// pre-Fase-4 2-second IsCancelled-poll goroutine (the
	// startCancelWatcher + cancelPollInterval pair REMOVED from
	// this file in FASE 4(b)). The cancel signal propagates through
	// native context cancellation: handlers that poll ctx.Err() at
	// phase boundaries short-circuit the same way they did under the
	// pre-Fase-4 polling model.
	stopLease := make(chan struct{})
	leaseDone := make(chan struct{})
	var renewCount atomic.Int64
	go w.renewLeaseLoopWith(jobCtx, j.ID, stopLease, leaseDone,
		renewLeaseLoopOpts{jobCancel: jobCancel, renewCount: &renewCount})
	defer func() {
		close(stopLease)
		<-leaseDone
	}()

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
	}

	// FASE 4(b) (July 2026): the startCancelWatcher call site is
	// REMOVED. Cancellation propagates through the typed
	// renewLeaseLoopWith LeaseState observation (see above). The
	// pre-Fase-4 IsCancelled callback that wrapped w.repo.Get() is
	// no longer part of the JobTools struct (domain/job/handler.go
	// ::JobExecutionTools).

	result, dispatchErr := w.dispatcher.Dispatch(jobCtx, j, tools)
	writerCompletedAt := time.Now().UTC()

	// FASE 2 observability: the attempt status mirrors the dispatcher
	// outcome (dispatchErr != nil → the run closes as FAILED with the
	// typed error; a retry scheduled by finalizeJob is still a failed
	// attempt). The deferred closure above finishes the run after
	// finalizeJob runs.

	// ── finalizationCtx (AGENTS.md §context-util-table allowlist) ──
	// MUST stay `context.WithTimeout(context.Background(),
	// finalizationTimeout)`. See the package-level doc-comment above
	// for the full invariant. finalizeJob (worker_execution_result.go)
	// consumes this ctx as-is and does NOT reconstruct it.
	finalizationCtx, finalCancel := context.WithTimeout(context.Background(), finalizationTimeout)
	defer finalCancel()

	canonicalAssetIDs := w.finalizeJob(finalizationCtx, j, result, dispatchErr)
	w.log.Info("worker: post-writer finalization complete",
		zap.String("job_id", j.ID),
		zap.String("job_type", j.Type),
		zap.Time("writer_completed_at", writerCompletedAt),
		zap.Int64("post_writer_finalize_ms", time.Since(writerCompletedAt).Milliseconds()),
		zap.Int64("lease_renew_count", renewCount.Load()),
		zap.Int64("lease_duration_ms", time.Since(claimAt).Milliseconds()),
	)

	finalStatus := "SUCCEEDED"
	var finalResult []byte
	if dispatchErr != nil {
		finalStatus = "FAILED"
	}
	if finalJob, getErr := w.repo.Get(finalizationCtx, j.ID); getErr == nil && finalJob != nil {
		finalStatus = string(finalJob.Status)
		finalResult = finalJob.Result
	} else if getErr != nil {
		// If finalization state cannot be read, fail closed in the
		// execution ledger rather than claiming success based only on
		// the dispatcher result.
		finalStatus = "FAILED"
	}
	if len(finalResult) == 0 {
		finalResult, _ = json.Marshal(result)
	}
	var report *kernobs.RunReport
	if run != nil {
		if dispatchErr != nil {
			run.FinishWithError(dispatchErr)
		} else {
			run.Finish()
		}
		report = run.Report()
	}
	ledger.Finish(finalizationCtx, j, stepID, w.id, attemptID, finalStatus, finalResult, dispatchErr, report)
	ledger.RecordCanonicalOutputs(finalizationCtx, j.ID, OutputRelationForJobType(j.Type), canonicalAssetIDs)
}
