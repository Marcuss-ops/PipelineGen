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
// final_audio.m4a, ... to their destination; per-scene voiceovers
// are NOT re-uploaded, the TTS pipeline publishes them during
// generation) can complete even when the worker is mid-shutdown.
// Detaching from jobCtx (rather than from ctx / worker lifecycle)
// prevents losing outcome persistence when jobCtx is cancelled by
// either timeout or by the outer worker Stop.
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
	"strings"
	"sync/atomic"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	instaeditcalendar "github.com/Marcuss-ops/PipelineGen/internal/platform/instaeditcalendar"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
	"go.uber.org/zap"
)

// finalizationTimeout bounds the broker-side finalize phase
// (runJob → finalizeJob). It covers the terminal DB flip AND the
// artifact-publication spine (CompleteWithArtifacts), which publishes
// the staged manifest artifacts (script.json, scenes.json,
// final_audio.m4a) to Drive. Per-scene voiceovers are NOT staged
// artifacts (Aug 2026 P0: the TTS pipeline publishes them during
// generation), so finalize is O(1) uploads instead of scaling with
// the artifact count. The legacy 30s bound (pre-artifact-publication)
// would have failed mid-publish; 10 minutes keeps worker shutdown
// bounded while staying far below the per-job timeout (30–60m).
const finalizationTimeout = 10 * time.Minute

func (w *Worker) runJob(parent context.Context, j *job.Job) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	if j.CorrelationID != "" {
		ctx = corid.WithCorrelationID(ctx, j.CorrelationID)
	}
	attemptTrace := job.ActivityTrace{CorrelationID: j.CorrelationID}

	if link := job.ParentLinkFromPayload(j.Payload); link.ParentRunID != "" {
		attemptTrace.ParentRunID = link.ParentRunID
	}
	if attemptTrace.CorrelationID == "" {
		attemptTrace.CorrelationID = corid.FromContext(ctx)
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
		if report := run.Report(); report != nil {
			attemptTrace.RunID = report.RunID
			attemptTrace.AttemptID = report.AttemptID
			attemptTrace.ParentRunID = report.ParentRunID
		}
		claimedAt := time.Now().UTC()
		kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseClaimed, claimedAt, claimedAt, kernobs.StageStatusCompleted, nil)
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
		// FASE resource telemetry (August 2026): sample host/process
		// resources every 500ms for the run's canonical identity
		// (run_id/job_id/attempt_id/worker_id/host) until runJob returns,
		// covering dispatch AND finalize. The stop defer is registered
		// AFTER the run-finish defer, so sampling halts before the run is
		// finalized. Sampling is best-effort: failures are logged by the
		// loop and never affect the job outcome (instrumentation must
		// never change behaviour).
		if w.resourceSampler != nil {
			if report := run.Report(); report != nil {
				stop := w.resourceSampler.SampleLoop(ctx, kernobs.ResourceSampleIdentity{
					RunID:     report.RunID,
					JobID:     report.JobID,
					AttemptID: report.AttemptID,
					WorkerID:  report.WorkerID,
					Host:      w.host,
				}, w.log)
				defer stop()
			}
		}
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

	ctx = job.WithActivityTraceIfAbsent(ctx, attemptTrace)
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
	ctx = job.WithActivityTraceIfAbsent(ctx, job.ActivityTrace{AttemptID: attemptID})
	stepID := ledger.Start(ctx, j, w.id, attemptID)
	defer func() {
		if rec := recover(); rec != nil {
			ledger.Finish(context.Background(), j, stepID, w.id, attemptID, "FAILED", nil, fmt.Errorf("panic in worker execution: %v", rec), nil)
			panic(rec)
		}
	}()

	// Step 8 (July 2026): emit "leased" as the first per-attempt event.
	if err := w.repo.AddEvent(ctx, j.ID, "leased",
		fmt.Sprintf("job claimed by worker %s", w.id),
		job.ActivityDataWithTrace(j.Type, "worker.claim", "running", "job lease acquired", map[string]any{
			"worker_id": w.id,
			"lease_id":  j.LeaseID,
			"revision":  j.Revision,
		}, job.ActivityTraceFromContext(ctx)),
	); err != nil {
		w.log.Warn("failed to record leased event",
			zap.String("job_id", j.ID),
			zap.Error(err))
	}

	// HC-1 (June 2026): per-job-type timeout resolves through the
	// typed Registry attached via WithRegistry().
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
		renewLeaseLoopOpts{jobCancel: jobCancel, renewCount: &renewCount, onHeartbeat: func() { w.reportCalendar(j.ID, j.Type, "RUNNING", "lease_heartbeat", nil, nil) }})
	defer func() {
		close(stopLease)
		<-leaseDone
	}()

	initialProgress := 0
	w.reportCalendar(j.ID, j.Type, "RUNNING", "worker_started", &initialProgress, nil)
	tools := &JobTools{
		// Durable per-stage sub-status: derived from the wired store, so a
		// running handler populates the stage table that
		// GET /api/jobs/{id}/stages reads back. Fail-soft by contract (the
		// handler logs and continues) and nil when the store has no such
		// port. Same derivation as the standalone worker subpackage, so the
		// two runtimes cannot disagree about whether stages are reported.
		StageStatus: stageStatusSink(w.repo),
		Progress: func(progress int, message string) {
			w.reportJobProgress(jobCtx, j, progress, message)
		},
		Event: func(eventType string, message string, data map[string]any) {
			// Normalize all handler events at the worker boundary so even
			// legacy producers expose the same kind/sub_kind/detail/payload
			// contract as ProgressTracker-generated activities.
			subKind := eventType
			if data == nil {
				data = map[string]any{}
			}
			if value, ok := data["sub_kind"].(string); ok && value != "" {
				subKind = value
			} else if value, ok := data["stage"].(string); ok && value != "" {
				subKind = value
			} else if value, ok := data["phase"].(string); ok && value != "" {
				subKind = value
			}
			status := job.ActivityStatus(eventType, data)
			detail := message
			if value, ok := data["detail"].(string); ok && value != "" {
				detail = value
			}
			data = job.ActivityDataWithTrace(j.Type, subKind, status, detail, data, job.ActivityTraceFromData(data, jobCtx))
			if trace, ok := data["trace"].(map[string]any); ok {
				data["sequence"] = trace["sequence"]
			}
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

	// FASE 2 observability — additive run binding: the allowlisted
	// context.WithTimeout(context.Background(), finalizationTimeout) site
	// above stays untouched; WithRun only layers the attempt's Run onto
	// the detached context so MeasureOperation/RecordStage calls made
	// inside the finalize path (artifact prepare/hash/Drive publish and
	// the completion TX) land in the SAME RunReport as the dispatcher
	// stages. The run is still finished by the deferred closure below,
	// after finalizeJob returns, so these records are never written on a
	// closed report. Jobs that never bound a run keep the pass-through
	// behaviour (instrumentation must never change behaviour).
	if run != nil {
		finalizationCtx = kernobs.WithRun(finalizationCtx, run)
	}
	finalizationCtx = job.WithActivityTraceFrom(finalizationCtx, jobCtx)

	postWriterFinalizeStarted := time.Now().UTC()
	finalizeStartedAt := time.Now().UTC()
	canonicalAssetIDs := w.finalizeJob(finalizationCtx, j, result, dispatchErr)
	kernobs.RecordClipPhase(finalizationCtx, kernobs.ClipPhaseFinalize, finalizeStartedAt, time.Now().UTC(), kernobs.StageStatusCompleted, dispatchErr)
	// CompleteWithArtifacts is outside the dispatcher-owned pipeline stages,
	// but it is still on the worker critical path. Record it explicitly so
	// SQLite contention, artifact publication, or finalizer retries cannot be
	// misreported as unattributed wall time.
	kernobs.RecordStage(jobCtx, kernobs.StageInfo{
		Stage: kernobs.StagePostWriterFinalize,
	}, postWriterFinalizeStarted, time.Now().UTC(), dispatchErr)
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
	calendarStatus := strings.ToUpper(finalStatus)
	if calendarStatus == "SUCCEEDED" {
		complete := 100
		w.reportCalendar(j.ID, j.Type, "COMPLETED", "completed", &complete, nil)
	} else if calendarStatus == "CANCELLED" || calendarStatus == "CANCELED" {
		w.reportCalendar(j.ID, j.Type, "CANCELLED", "cancelled", nil, nil)
	} else if calendarStatus == "FAILED" || dispatchErr != nil {
		reason := "remote job failed"
		tail := ""
		if dispatchErr != nil {
			reason = dispatchErr.Error()
			tail = reason
		}
		if len(tail) > 12000 {
			tail = tail[len(tail)-12000:]
		}
		w.reportCalendar(j.ID, j.Type, "FAILED", "failed", nil, &instaeditcalendar.WorkerError{ErrorCode: "REMOTE_JOB_FAILED", Reason: reason, OutputTail: tail})
	}
	ledger.Finish(finalizationCtx, j, stepID, w.id, attemptID, finalStatus, finalResult, dispatchErr, report)
	ledger.RecordCanonicalOutputs(finalizationCtx, j.ID, OutputRelationForJobType(j.Type), canonicalAssetIDs)
}

// reportJobProgress sends progress to the job store and Calendar's durable
// spool independently. A broker write failure must not hide a stage update
// from the Calendar operator view.
func (w *Worker) reportJobProgress(ctx context.Context, j *job.Job, progress int, message string) {
	trace := job.ActivityTraceFromContext(ctx)
	activity := job.ProgressActivityDataWithTrace(j.Type, progress, message, trace)
	if sink, ok := w.repo.(interface {
		SetProgressData(context.Context, string, int, string, map[string]any) error
	}); ok {
		if err := sink.SetProgressData(ctx, j.ID, progress, message, activity); err != nil {
			w.log.Warn("failed to report structured progress",
				zap.String("job_id", j.ID), zap.Int("progress", progress), zap.Error(err))
			observability.WorkerProgressEmittedTotal.WithLabelValues(j.Type, "error").Inc()
			observability.WorkerProgressErrorsTotal.WithLabelValues(j.Type, "broker_emit_failed").Inc()
		} else {
			observability.WorkerProgressEmittedTotal.WithLabelValues(j.Type, "success").Inc()
		}
	} else if err := w.repo.SetProgress(ctx, j.ID, progress, message); err != nil {
		w.log.Warn("failed to report progress",
			zap.String("job_id", j.ID),
			zap.Int("progress", progress),
			zap.Error(err))
		observability.WorkerProgressEmittedTotal.WithLabelValues(j.Type, "error").Inc()
		observability.WorkerProgressErrorsTotal.WithLabelValues(j.Type, "broker_emit_failed").Inc()
	} else {
		observability.WorkerProgressEmittedTotal.WithLabelValues(j.Type, "success").Inc()
	}
	value := progress
	w.reportCalendar(j.ID, j.Type, "RUNNING", message, &value, nil)
}

func (w *Worker) jobTimeoutFor(jobType string) time.Duration {
	if w.timeouts != nil {
		if d, ok := w.timeouts[jobType]; ok && d > 0 {
			return d
		}
	}
	return 10 * time.Minute
}

// maxRetriesFor returns the default max-retry count for a job type,
// sourced from the attached Registry. Falls back to the canonical
// 3-retry default when the worker has no attached Registry or the
// job type is not registered. Mirrors the timeout lookup pattern
// (jobTimeoutFor).
//
// Issue 2 / P0 (June 2026): locks the Worker-side retry lookup so
// the future Issue 4 (P1, Enqueue path) integration into runJob is
// a one-line swap — pass effectiveRetries := w.maxRetriesFor(j.Type)
// when j.MaxRetries == 0. The companion regression test
// TestWorker_HonorsRegistryRetries (in registry_wiring_test.go)
// pins this contract today so Issue 4 cannot accidentally regress
// the lookup surface.
func (w *Worker) maxRetriesFor(jobType string) int {
	if w.reg != nil {
		return w.reg.DefaultMaxRetries(jobType)
	}
	return 3
}
