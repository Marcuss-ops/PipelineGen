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
// AND the artifact-publication spine — script.json, ...
// to their destination; per-scene voiceovers are NOT re-uploaded, the
// TTS pipeline publishes them during generation, and a certified
// final_audio is a REFERENCE to the master publishFinalAudio already
// published — only an unreferenced master is staged) can complete even
// when the worker is mid-shutdown.
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
	"errors"
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
// the staged manifest artifacts (script.json) to Drive.
// Per-scene voiceovers are NOT staged artifacts (Aug 2026 P0: the TTS
// pipeline publishes them during generation) and a certified
// final_audio is a reference to the master publishFinalAudio already
// published (single publication owner), so finalize is O(1) uploads
// instead of scaling with the artifact count. The legacy 30s bound (pre-artifact-publication)
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

	// FASE 2 observability: one claim = one Run. The run is bound to ctx so
	// handlers and adapters record stages/operations into the same report, and
	// it is finished by the deferred closure below with the attempt outcome.
	// The recorder sink receives the parent ctx (not the timeout-bounded
	// jobCtx) so final writes survive jobCtx cancellation.
	ctx, attemptTrace, run, stopSampler := w.startClaimRun(parent, ctx, j, attemptTrace)
	var dispatchErr error
	if run != nil {
		defer func() {
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
		// Registered AFTER the run-finish defer, so sampling halts (LIFO)
		// before the run is finalized.
		if stopSampler != nil {
			defer stopSampler()
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
	attemptID := resolveAttemptID(j, run)
	ctx = job.WithActivityTraceIfAbsent(ctx, job.ActivityTrace{AttemptID: attemptID})
	stepID := ledger.Start(ctx, j, w.id, attemptID)
	defer func() {
		if rec := recover(); rec != nil {
			ledger.Finish(context.Background(), j, stepID, w.id, attemptID, "FAILED", nil, fmt.Errorf("panic in worker execution: %v", rec), nil)
			panic(rec)
		}
	}()

	// Step 8 (July 2026): emit "leased" as the first per-attempt event.
	w.recordLeasedEvent(ctx, j)

	jobCtx, jobCancel := w.newJobContext(ctx, parent, j.Type)
	defer jobCancel()

	var renewCount atomic.Int64
	stopRenewal := w.startLeaseRenewal(jobCtx, j, jobCancel, &renewCount)
	defer stopRenewal()

	initialProgress := 0
	w.reportCalendar(j.ID, j.Type, "RUNNING", "worker_started", &initialProgress, nil)
	tools := w.jobTools(jobCtx, j)

	result, dispatchErr, writerCompletedAt := w.dispatchJob(jobCtx, j, tools)
	finalizationCtx, finalCancel, canonicalAssetIDs := w.finalizeRun(jobCtx, j, result, dispatchErr, run, writerCompletedAt, renewCount.Load(), claimAt)
	defer finalCancel()

	finalStatus, finalResult, report := w.resolveFinalJobState(finalizationCtx, j, result, dispatchErr, run)
	w.reportFinalCalendar(j, finalStatus, dispatchErr)
	ledger.Finish(finalizationCtx, j, stepID, w.id, attemptID, finalStatus, finalResult, dispatchErr, report)
	ledger.RecordCanonicalOutputs(finalizationCtx, j.ID, OutputRelationForJobType(j.Type), canonicalAssetIDs)
}

// startClaimRun opens the observability Run for the attempt and returns the
// stop function for the resource sampler. A nil run means no observer is
// wired: every downstream Run binding is then a pass-through, so
// instrumentation can never change behaviour.
func (w *Worker) startClaimRun(parent, ctx context.Context, j *job.Job, attemptTrace job.ActivityTrace) (context.Context, job.ActivityTrace, *kernobs.Run, func()) {
	if w.observer == nil {
		return ctx, attemptTrace, nil, nil
	}
	// The lease fence MUST be surfaced on the run: RecoverAbandoned
	// (run_recorder.go) only reclaims RUNNING runs whose lease_expires_at has a
	// non-NULL past value. Without LeaseID / WorkerID / LeaseExpiresAt here,
	// lease_expires_at stays NULL for every run and a worker crash can never be
	// recovered into ABANDONED.
	leaseExpiry := time.Time{}
	if j.LeaseExpiry != nil {
		leaseExpiry = *j.LeaseExpiry
	}
	run := w.observer.StartRunForClaim(parent, kernobs.ClaimRunInfo{
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

	// FASE resource telemetry (August 2026): sample host/process resources every
	// 500ms for the run's canonical identity (run_id/job_id/attempt_id/
	// worker_id/host) until runJob returns, covering dispatch AND finalize.
	// Sampling is best-effort: failures are logged by the loop and never affect
	// the job outcome.
	var stop func()
	if w.resourceSampler != nil {
		if report := run.Report(); report != nil {
			stop = w.resourceSampler.SampleLoop(ctx, kernobs.ResourceSampleIdentity{
				RunID:     report.RunID,
				JobID:     report.JobID,
				AttemptID: report.AttemptID,
				WorkerID:  report.WorkerID,
				Host:      w.host,
			}, w.log)
		}
	}
	return ctx, attemptTrace, run, stop
}

func (w *Worker) recordLeasedEvent(ctx context.Context, j *job.Job) {
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
}

// newJobContext applies the HC-1 per-job-type timeout (typed Registry attached
// via WithRegistry) and binds the worker-stop probe. A graceful restart cancels
// the worker lifecycle context, which reaches every in-flight jobCtx; the probe
// lets the finalize path and the durable runner tell "the worker is stopping"
// from "the job failed". A job timeout or a user cancel leaves `parent` alive,
// so both keep their historical failure semantics.
func (w *Worker) newJobContext(ctx, parent context.Context, jobType string) (context.Context, context.CancelFunc) {
	jobCtx, jobCancel := context.WithTimeout(ctx, w.jobTimeoutFor(jobType))
	jobCtx = job.WithWorkerStopProbe(jobCtx, func() bool { return parent.Err() != nil })
	return jobCtx, jobCancel
}

// startLeaseRenewal starts the FASE 4(b) renew loop and returns the stop
// function the caller defers. Cancellation arrives through the typed
// kerneljob.RenewLeaseResult.State on each tick (the renew loop calls jobCancel
// on LeaseStateCancelRequested), replacing the pre-Fase-4 IsCancelled polling
// goroutine; handlers polling ctx.Err() short-circuit the same way.
func (w *Worker) startLeaseRenewal(jobCtx context.Context, j *job.Job, jobCancel context.CancelFunc, renewCount *atomic.Int64) func() {
	stopLease := make(chan struct{})
	leaseDone := make(chan struct{})
	go w.renewLeaseLoopWith(jobCtx, j.ID, stopLease, leaseDone,
		renewLeaseLoopOpts{jobCancel: jobCancel, renewCount: renewCount, onHeartbeat: func() { w.reportCalendar(j.ID, j.Type, "RUNNING", "lease_heartbeat", nil, nil) }})
	return func() {
		close(stopLease)
		<-leaseDone
	}
}

func (w *Worker) jobTools(jobCtx context.Context, j *job.Job) *JobTools {
	return &JobTools{
		// Durable per-stage sub-status: derived from the wired store, so a
		// running handler populates the stage table that
		// GET /api/jobs/{id}/stages reads back. Fail-soft by contract (the
		// handler logs and continues) and nil when the store has no such port.
		// Same derivation as the standalone worker subpackage, so the two
		// runtimes cannot disagree about whether stages are reported.
		StageStatus: stageStatusSink(w.repo),
		Progress: func(progress int, message string) {
			w.reportJobProgress(jobCtx, j, progress, message)
		},
		Event: func(eventType string, message string, data map[string]any) {
			w.recordHandlerEvent(jobCtx, j, eventType, message, data)
		},
	}
}

// recordHandlerEvent normalizes every handler event at the worker boundary so
// even legacy producers expose the same kind/sub_kind/detail/payload contract as
// ProgressTracker-generated activities. A failed write bumps the canonical
// per-job_type drop counter instead of silently disappearing.
func (w *Worker) recordHandlerEvent(jobCtx context.Context, j *job.Job, eventType, message string, data map[string]any) {
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
	if err := w.repo.AddEvent(jobCtx, j.ID, eventType, message, data); err != nil {
		w.log.Warn("failed to record event",
			zap.String("job_id", j.ID),
			zap.String("event_type", eventType),
			zap.Error(err))
		observability.WorkerEventDropsTotal.WithLabelValues(j.Type).Inc()
	}
}

// dispatchJob runs the handler and converts a lifecycle-caused dispatch error
// into a deferral. Shutdown is a WAIT, not a verdict: the handler died with the
// process, so its error describes the worker's exit, and finalising it as a
// failure dead-lettered healthy runs on restart and spent a retry per restart.
func (w *Worker) dispatchJob(jobCtx context.Context, j *job.Job, tools *JobTools) (Result, error, time.Time) {
	result, dispatchErr := w.dispatcher.Dispatch(jobCtx, j, tools)
	writerCompletedAt := time.Now().UTC()
	if interruption := workerShutdownInterruption(jobCtx, dispatchErr); interruption != nil {
		w.log.Warn("worker: shutdown interrupted the attempt — deferring it instead of failing the job",
			zap.String("job_id", j.ID),
			zap.String("job_type", j.Type),
			zap.Error(dispatchErr))
		dispatchErr = interruption
	}
	return result, dispatchErr, writerCompletedAt
}

// finalizeRun builds the allowlisted finalizationCtx, runs finalizeJob and
// records the publication spine's own wall time. The caller owns finalCancel.
func (w *Worker) finalizeRun(jobCtx context.Context, j *job.Job, result Result, dispatchErr error, run *kernobs.Run, writerCompletedAt time.Time, leaseRenewCount int64, claimAt time.Time) (context.Context, context.CancelFunc, []string) {
	// ── finalizationCtx (AGENTS.md §context-util-table allowlist) ──
	// MUST stay `context.WithTimeout(context.Background(),
	// finalizationTimeout)`. See the package-level doc-comment for the full
	// invariant. finalizeJob (worker_execution_result.go) consumes this ctx
	// as-is and does NOT reconstruct it.
	finalizationCtx, finalCancel := context.WithTimeout(context.Background(), finalizationTimeout)

	// FASE 2 observability — additive run binding: the allowlisted site above
	// stays untouched; WithRun only layers the attempt's Run onto the detached
	// context so MeasureOperation/RecordStage calls made inside the finalize
	// path (artifact prepare/hash/Drive publish and the completion TX) land in
	// the SAME RunReport as the dispatcher stages. The run is still finished by
	// the caller after this returns, so these records are never written on a
	// closed report.
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
	// The stage measures the publication spine's OWN wall. A deferral is a wait
	// owned by the dispatch layer (finalizeJobDeferral), not a failed finalize:
	// recording it here made every re-poll of a long run add a phantom
	// "post_writer_finalize FAILED" row (measured: 6.628 rows, most on
	// deferred/retried attempts). Real dispatch failures keep the typed error on
	// the stage so the failure stays attributable.
	finalizeStageErr := dispatchErr
	if errors.Is(dispatchErr, job.ErrDeferred) {
		finalizeStageErr = nil
	}
	kernobs.RecordStage(jobCtx, kernobs.StageInfo{
		Stage: kernobs.StagePostWriterFinalize,
	}, postWriterFinalizeStarted, time.Now().UTC(), finalizeStageErr)
	w.log.Info("worker: post-writer finalization complete",
		zap.String("job_id", j.ID),
		zap.String("job_type", j.Type),
		zap.Time("writer_completed_at", writerCompletedAt),
		zap.Int64("post_writer_finalize_ms", time.Since(writerCompletedAt).Milliseconds()),
		zap.Int64("lease_renew_count", leaseRenewCount),
		zap.Int64("lease_duration_ms", time.Since(claimAt).Milliseconds()),
	)
	return finalizationCtx, finalCancel, canonicalAssetIDs
}

// resolveAttemptID prefers the observability attempt identity so the ledger and
// the RunReport agree; without an observer it falls back to the durable
// (job, revision) pair.
func resolveAttemptID(j *job.Job, run *kernobs.Run) string {
	if run != nil {
		if report := run.Report(); report != nil && report.AttemptID != "" {
			return report.AttemptID
		}
	}
	return fmt.Sprintf("%s:%d", j.ID, j.Revision)
}

// resolveFinalJobState reads back the terminal state the finalizer committed.
// If that state cannot be read the ledger fails closed instead of claiming
// success based only on the dispatcher result.
func (w *Worker) resolveFinalJobState(ctx context.Context, j *job.Job, result Result, dispatchErr error, run *kernobs.Run) (string, []byte, *kernobs.RunReport) {
	finalStatus := "SUCCEEDED"
	var finalResult []byte
	if dispatchErr != nil {
		finalStatus = "FAILED"
	}
	if finalJob, getErr := w.repo.Get(ctx, j.ID); getErr == nil && finalJob != nil {
		finalStatus = string(finalJob.Status)
		finalResult = finalJob.Result
	} else if getErr != nil {
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
	return finalStatus, finalResult, report
}

// reportFinalCalendar projects the terminal state onto the Calendar view. The
// error tail is capped so a runaway handler cannot write a multi-megabyte
// failure reason into the spool.
func (w *Worker) reportFinalCalendar(j *job.Job, finalStatus string, dispatchErr error) {
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

// workerShutdownInterruption reports the deferral that replaces a dispatch
// error caused by the worker lifecycle going away, and nil when the error is a
// real verdict (or there is no error at all).
//
// The decisive facts are the job context having actually been cancelled and the
// lifecycle probe saying the WORKER is stopping. Requiring both keeps a handler
// that failed for its own reason a millisecond before shutdown from being
// laundered into a deferral: only an attempt whose context died with the
// process qualifies.
func workerShutdownInterruption(jobCtx context.Context, dispatchErr error) error {
	if dispatchErr == nil || jobCtx == nil || jobCtx.Err() == nil || !job.WorkerStopped(jobCtx) {
		return nil
	}
	return job.DeferredAfter(0, fmt.Sprintf(
		"worker shutdown interrupted the attempt (%v); the durable run resumes from its checkpoint on the next dispatch",
		jobCtx.Err()))
}

func (w *Worker) jobTimeoutFor(jobType string) time.Duration {
	if w.timeouts != nil {
		if d, ok := w.timeouts[jobType]; ok && d > 0 {
			return d
		}
	}
	return 10 * time.Minute
}
