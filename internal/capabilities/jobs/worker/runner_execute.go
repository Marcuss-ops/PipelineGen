package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	// kerneljob is aliased because runLease binds the local variable
	// `job := lease.Job`; using the bare package name would shadow it.
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

// runLease is the main job execution pipeline. Called from the Run
//  1. Handler lookup + workspace preparation
//  2. Lease renewal loop start
//  3. Asset download (if input_assets in payload)
//  4. Handler dispatch via registry.Dispatch
//  5. Artifact manifest upload (or legacy fallback)
//  6. Terminal completion (Complete or CompleteWithArtifacts)
//
// godlike/07 P0 #5 fail-closed: drains renewal-loop errors BEFORE
// calling tools.Complete, preventing phantom completes on reassigned
// leases.
func (r *Runner) runLease(parent context.Context, lease *jobs.Lease) (retErr error) {
	job := lease.Job
	attempt := newLeaseAttempt(r, job)

	// PR-COMPLETE-FAIL-CLOSED (August 2026): if the registry is nil
	// (composition bug or unwired worker), fail-closed immediately.
	// A nil registry means we can't look up handlers, can't determine
	// artifact routing, and would silently degrade to tools.Complete
	// for artifact-producing jobs — silently dropping asset records.
	// FAIL BOOT in wire_services_composition.go already prevents this
	// at startup; this is the runtime defense-in-depth layer.
	if r.registry == nil {
		r.log.Error("nil registry — fail-closed (FAIL BOOT should have caught this)",
			zap.String("job_type", job.Type),
			zap.String("job_id", job.ID),
		)
		terminalErr := fmt.Errorf("%w: nil registry — worker must be composed with a job Registry", ErrHandlerNotRegistered)
		attempt.terminalErr = terminalErr
		attempt.start(parent)
		attempt.finish(parent, nil, nil)
		return r.fail(parent, lease, terminalErr)
	}

	// Defensive: the claim filter should prevent this, but verify the
	// claimed job type is actually supported before doing any work.
	if !r.registry.Has(job.Type) {
		r.log.Error("claimed unsupported job type — releasing",
			zap.String("job_type", job.Type),
			zap.String("job_id", job.ID),
		)
		unsupportedErr := fmt.Errorf("%w: %s", ErrHandlerNotRegistered, job.Type)
		attempt.terminalErr = unsupportedErr
		attempt.start(parent)
		attempt.finish(parent, nil, nil)
		return r.fail(parent, lease, unsupportedErr)
	}

	jobCtx, cancel := context.WithCancel(parent)
	defer cancel()

	// FASE 2 observability: every claimed lease is one Run (= one attempt).
	// queue_wait_ms = server-side started_at − created_at; the per-attempt token
	// is the canonical lease. The run is bound to jobCtx so the handler and
	// adapters can record stages/operations.
	//
	// Terminal-status classification: tools.Fail / r.fail can return NIL when
	// the broker accepts the failure report, so the return value alone cannot
	// distinguish "attempt failed, broker accepted" from "attempt succeeded".
	// attempt.terminalErr is set at each fail-return site; the deferred closure
	// prefers retErr (a non-nil finalisation error, e.g.
	// ErrLeaseLostDuringRun), then terminalErr, and only otherwise closes the
	// run as SUCCEEDED.
	jobCtx, run := r.startLeaseRun(parent, jobCtx, lease, attempt)
	defer func() {
		if rec := recover(); rec != nil {
			if run != nil {
				run.FinishWithPanic(rec)
			}
			attempt.finish(parent, retErr, runReport(run))
			panic(rec)
		}
		if run != nil {
			switch {
			case retErr != nil:
				run.FinishWithError(retErr)
			case attempt.terminalErr != nil:
				run.FinishWithError(attempt.terminalErr)
			default:
				run.Finish()
			}
		}
		attempt.finish(parent, retErr, runReport(run))
	}()

	jobDir, err := r.workspace.Prepare(job.ID)
	if err != nil {
		attempt.terminalErr = err
		return r.fail(jobCtx, lease, err)
	}
	defer func() {
		_ = r.workspace.Cleanup(job.ID)
	}()

	tools := r.executionTools(job, jobDir, attempt.ledger)

	// Start lease renewal loop (W1 Phase 7).
	renewal := r.startLeaseRenewalCheck(jobCtx, tools, job, cancel)
	defer renewal.stop()

	if err := renewal.check(); err != nil {
		attempt.terminalErr = err
		return tools.Fail(jobCtx, err.Error())
	}

	// Asset download phase
	if err := r.downloadInputAssets(jobCtx, lease, tools, attempt, renewal); err != nil {
		attempt.terminalErr = err
		return tools.Fail(jobCtx, err.Error())
	}

	// Handler dispatch + manifest upload
	resultJSON, uploaded, err := r.dispatchAndUpload(jobCtx, lease, tools, attempt, renewal)
	if err != nil {
		attempt.terminalErr = err
		return tools.Fail(jobCtx, err.Error())
	}

	// Stop renewal BEFORE Complete so a final tick that lands while Complete is
	// mid-flight doesn't try to flip a job we just terminal-reported.
	renewal.stop()
	// P0 #5 (July 2026) — fail-closed seam: drain the renewal loop's error
	// channel BEFORE calling tools.Complete.
	if preCompleteErr := postRenewFailClosedCheck(renewal.errs); preCompleteErr != nil {
		return preCompleteErr
	}

	if completeErr := r.completeLease(jobCtx, lease, tools, attempt, resultJSON, uploaded); completeErr != nil {
		attempt.terminalErr = completeErr
		return completeErr
	}
	return nil
}

// leaseAttempt owns the ledger bookkeeping of one claimed lease: the attempt
// identity, the step it opened, and the handler result the terminal row must
// carry. finish is idempotent because the panic path and the normal path can both
// reach it.
type leaseAttempt struct {
	ledger        *jobs.JobRegistryRecorder
	job           *jobs.Job
	workerID      string
	attemptID     string
	stepID        string
	handlerResult map[string]any
	terminalErr   error
	finished      bool
}

func newLeaseAttempt(r *Runner, job *jobs.Job) *leaseAttempt {
	return &leaseAttempt{
		ledger:    jobs.NewJobRegistryRecorder(r.jobLedger, r.log),
		job:       job,
		workerID:  r.workerID,
		attemptID: fmt.Sprintf("%s:%d", job.ID, job.Revision),
	}
}

func (a *leaseAttempt) start(ctx context.Context) {
	a.stepID = a.ledger.Start(ctx, a.job, a.workerID, a.attemptID)
}

func (a *leaseAttempt) finish(parent context.Context, retErr error, report *kernobs.RunReport) {
	if a.finished {
		return
	}
	a.finished = true
	status := "SUCCEEDED"
	var finishErr error
	if retErr != nil {
		status = "FAILED"
		finishErr = retErr
	}
	if a.terminalErr != nil {
		status = "FAILED"
		finishErr = a.terminalErr
	}
	var resultJSON []byte
	if a.handlerResult != nil {
		resultJSON, _ = json.Marshal(a.handlerResult)
	}
	a.ledger.Finish(parent, a.job, a.stepID, a.workerID, a.attemptID, status, resultJSON, finishErr, report)
}

func runReport(run *kernobs.Run) *kernobs.RunReport {
	if run == nil {
		return nil
	}
	return run.Report()
}

// startLeaseRun opens the observability Run (when wired) and binds the attempt
// identity to the job context. Without an observer the context is returned with
// only the activity trace, and every downstream Run binding is a pass-through.
func (r *Runner) startLeaseRun(parent, jobCtx context.Context, lease *jobs.Lease, attempt *leaseAttempt) (context.Context, *kernobs.Run) {
	job := lease.Job
	var run *kernobs.Run
	if r.observer != nil {
		// The lease fence MUST be surfaced on the run: RecoverAbandoned
		// (run_recorder.go) only reclaims RUNNING runs whose lease_expires_at
		// has a non-NULL past value. Without LeaseID / WorkerID /
		// LeaseExpiresAt here, lease_expires_at stays NULL for every run and a
		// worker crash can never be recovered into ABANDONED. Prefer the Lease
		// envelope's ExpiresAt; fall back to the claimed Job's LeaseExpiry when
		// the envelope is unset (test stubs populate only one of the two).
		leaseExpiry := lease.ExpiresAt
		if leaseExpiry.IsZero() && job.LeaseExpiry != nil {
			leaseExpiry = *job.LeaseExpiry
		}
		run = r.observer.StartRunForClaim(parent, kernobs.ClaimRunInfo{
			JobID:          job.ID,
			JobType:        job.Type,
			AttemptID:      kernobs.NewAttemptID(), // persistent execution identity; LeaseID remains the worker fence
			LeaseID:        lease.LeaseID,
			WorkerID:       r.workerID,
			LeaseExpiresAt: leaseExpiry,
			CreatedAt:      job.CreatedAt,
			StartedAt:      job.StartedAt,
			ParentJobID:    kerneljob.ParentLinkFromPayload(job.Payload).ParentJobID,
			ParentRunID:    kerneljob.ParentLinkFromPayload(job.Payload).ParentRunID,
			RetryCount:     job.RetryCount,
		})
		jobCtx = kernobs.WithRun(jobCtx, run)
	}

	trace := kerneljob.ActivityTrace{CorrelationID: job.CorrelationID}
	if link := kerneljob.ParentLinkFromPayload(job.Payload); link.ParentRunID != "" {
		trace.ParentRunID = link.ParentRunID
	}
	if report := runReport(run); report != nil {
		trace.RunID = report.RunID
		trace.AttemptID = report.AttemptID
		trace.ParentRunID = report.ParentRunID
		attempt.attemptID = report.AttemptID
	}
	if trace.AttemptID == "" {
		trace.AttemptID = attempt.attemptID
	}
	jobCtx = kerneljob.WithActivityTraceIfAbsent(jobCtx, trace)
	return jobCtx, run
}

func (r *Runner) executionTools(job *jobs.Job, jobDir string, ledger *jobs.JobRegistryRecorder) *Tools {
	var store eventStore
	if s, ok := r.broker.(eventStore); ok {
		store = s
	}
	// The SAME recorder instance the attempt finishes with: the handler's stage
	// rows and the terminal ledger row must belong to one attempt.
	return NewTools(r.broker, store, r.workerID, r.sessionID, job, jobDir, r.assetClient).WithJobRegistry(ledger)
}

// leaseRenewal is the lease renewal loop plus its non-blocking error drain.
// check() is the single seam every phase uses to notice a lost lease before it
// writes a terminal state.
type leaseRenewal struct {
	r      *Runner
	cancel context.CancelFunc
	errs   chan error
	jobID  string
}

func (r *Runner) startLeaseRenewalCheck(jobCtx context.Context, tools *Tools, job *jobs.Job, jobCancel context.CancelFunc) *leaseRenewal {
	renewCtx, renewCancel := context.WithCancel(jobCtx)
	errs := make(chan error, 1)
	go r.renewLoop(renewCtx, tools, job.ID, errs, jobCancel)
	return &leaseRenewal{r: r, cancel: renewCancel, errs: errs, jobID: job.ID}
}

// check non-blockingly reads any error that the renewal goroutine has already
// emitted.
func (n *leaseRenewal) check() error {
	select {
	case err := <-n.errs:
		if err != nil {
			n.r.log.Warn("lease renewal failed — failing the job",
				zap.String("job_id", n.jobID),
				zap.Error(err))
		}
		return err
	default:
		return nil
	}
}

func (n *leaseRenewal) stop() {
	n.cancel()
}

// downloadInputAssets stages every payload input_assets entry through the
// worker's tools. A failed download or a lost lease aborts the attempt before
// dispatch, so the handler never runs on missing bytes.
func (r *Runner) downloadInputAssets(jobCtx context.Context, lease *jobs.Lease, tools *Tools, attempt *leaseAttempt, renewal *leaseRenewal) error {
	for i, assetID := range ParseInputAssets(lease.Job.Payload) {
		if _, err := tools.DownloadAsset(jobCtx, assetID); err != nil {
			return fmt.Errorf("download asset %d (%s): %w", i, assetID, err)
		}
		attempt.ledger.Downloaded(jobCtx, lease.Job.ID, assetID, i)
		// FASE 0.2 (July 4 2026) silent-drop rewrite per
		// PR-GODOBJ-14-WORKER-REGISTRY godlike/07 no-fake-availability:
		// pre-PR the runner used `_ = tools.Progress(...)` which dropped the
		// broker emit error without any observability.
		if progErr := tools.Progress(jobCtx, 5+i, "staged input asset"); progErr != nil {
			r.log.Warn("worker Progress emit failed (FASE 0.2 silent-drop rewrite)",
				zap.String("job_id", lease.Job.ID),
				zap.String("job_type", lease.Job.Type),
				zap.Int("progress", 5+i),
				zap.Error(progErr))
			observability.WorkerProgressEmittedTotal.WithLabelValues(lease.Job.Type, "error").Inc()
			observability.WorkerProgressErrorsTotal.WithLabelValues(lease.Job.Type, "broker_emit_failed").Inc()
		} else {
			observability.WorkerProgressEmittedTotal.WithLabelValues(lease.Job.Type, "success").Inc()
		}
		if err := renewal.check(); err != nil {
			return err
		}
	}
	return nil
}

// dispatchAndUpload runs the handler and, for artifact-producing job types,
// uploads the manifest. Only artifact producers should attempt the upload:
// non-artifact jobs (e.g. media.reindex) legitimately run with a nil assetClient
// and a non-empty handlerResult, and calling uploadManifest for them would
// fail an otherwise successful job with ErrArtifactClientRequired.
func (r *Runner) dispatchAndUpload(jobCtx context.Context, lease *jobs.Lease, tools *Tools, attempt *leaseAttempt, renewal *leaseRenewal) (json.RawMessage, *kerneljob.RemoteArtifactManifest, error) {
	result, err := r.registry.Dispatch(jobCtx, lease.Job, tools)
	if err != nil {
		return nil, nil, err
	}
	attempt.handlerResult = result
	if err := renewal.check(); err != nil {
		return nil, nil, err
	}

	// The typed nil pointer preserves the nil-check semantics: a typed-nil
	// *RemoteArtifactManifest satisfies uploaded != nil correctly, unlike an
	// interface-typed any which would treat a typed-nil pointer as non-nil.
	var uploaded *kerneljob.RemoteArtifactManifest
	if r.registry.ProducesArtifacts(lease.Job.Type) {
		uploaded, err = r.uploadManifest(jobCtx, lease.Job.ID, result)
		if err != nil {
			return nil, nil, err
		}
	}

	var resultJSON json.RawMessage
	if uploaded != nil {
		resultJSON, err = json.Marshal(uploaded)
	} else {
		resultJSON, err = json.Marshal(result)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := renewal.check(); err != nil {
		return nil, nil, err
	}
	return resultJSON, uploaded, nil
}

// completeLease performs the terminal write. Artifact producers publish through
// CompleteWithArtifacts and record the publication spine as
// post_writer_finalize (mirrors the legacy Worker's RecordStage) so the finalize
// wall time stays attributed instead of surfacing as unattributed time.
func (r *Runner) completeLease(jobCtx context.Context, lease *jobs.Lease, tools *Tools, attempt *leaseAttempt, resultJSON json.RawMessage, uploaded *kerneljob.RemoteArtifactManifest) error {
	if r.registry.ProducesArtifacts(lease.Job.Type) {
		var publishedJSON json.RawMessage
		if uploaded != nil {
			var err error
			publishedJSON, err = json.Marshal(uploaded)
			if err != nil {
				return err
			}
		}
		finalizeStarted := time.Now().UTC()
		canonicalAssetIDs, completeErr := tools.CompleteWithArtifacts(jobCtx, resultJSON, publishedJSON, nil)
		kernobs.RecordStage(jobCtx, kernobs.StageInfo{
			Stage: kernobs.StagePostWriterFinalize,
		}, finalizeStarted, time.Now().UTC(), completeErr)
		if completeErr != nil {
			return completeErr
		}
		attempt.ledger.RecordCanonicalOutputs(jobCtx, lease.Job.ID, jobs.OutputRelationForJobType(lease.Job.Type), canonicalAssetIDs)
		return nil
	}
	return tools.Complete(jobCtx, resultJSON)
}
