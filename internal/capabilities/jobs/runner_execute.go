package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	// kerneljob is aliased because runLease binds the local variable
	// `job := lease.Job`; using the bare package name would shadow it.
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
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
func (r *Runner) runLease(parent context.Context, lease *appjobs.Lease) (retErr error) {
	job := lease.Job
	ledger := appjobs.NewJobRegistryRecorder(r.jobLedger, r.log)
	attemptID := fmt.Sprintf("%s:%d", job.ID, job.Revision)
	stepID := ""
	var handlerResult map[string]any
	var terminalErr error
	ledgerFinished := false
	finishLedger := func(report *kernobs.RunReport) {
		if ledgerFinished {
			return
		}
		ledgerFinished = true
		status := "SUCCEEDED"
		var finishErr error
		if retErr != nil {
			status = "FAILED"
			finishErr = retErr
		}
		if terminalErr != nil {
			status = "FAILED"
			finishErr = terminalErr
		}
		var resultJSON []byte
		if handlerResult != nil {
			resultJSON, _ = json.Marshal(handlerResult)
		}
		ledger.Finish(parent, job, stepID, r.workerID, attemptID, status, resultJSON, finishErr, report)
	}

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
		terminalErr = fmt.Errorf("%w: nil registry — worker must be composed with a job Registry", ErrHandlerNotRegistered)
		stepID = ledger.Start(parent, job, r.workerID, attemptID)
		finishLedger(nil)
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
		terminalErr = unsupportedErr
		stepID = ledger.Start(parent, job, r.workerID, attemptID)
		finishLedger(nil)
		return r.fail(parent, lease, unsupportedErr)
	}

	jobCtx, cancel := context.WithCancel(parent)
	defer cancel()

	// FASE 2 observability (kernel/observability): every claimed lease
	// is one Run (= one attempt). queue_wait_ms = server-side
	// started_at − created_at (the broker's Claim populates StartedAt
	// via ClaimNext); the per-attempt token is the canonical lease. The
	// run is bound to jobCtx so the handler and adapters can record
	// stages/operations.
	//
	// Terminal-status classification: tools.Fail / r.fail can return
	// NIL when the broker accepts the failure report, so the return
	// value alone cannot distinguish "attempt failed, broker
	// accepted" from "attempt succeeded". terminalErr is set at each
	// fail-return site; the deferred closure prefers retErr (a
	// non-nil finalisation error, e.g. ErrLeaseLostDuringRun), then
	// terminalErr, and only otherwise closes the run as SUCCEEDED.
	var run *kernobs.Run
	if r.observer != nil {
		// The lease fence MUST be surfaced on the run: RecoverAbandoned
		// (run_recorder.go) only reclaims RUNNING runs whose
		// lease_expires_at has a non-NULL past value. Without LeaseID /
		// WorkerID / LeaseExpiresAt here, lease_expires_at stays NULL for
		// every run and a worker crash can never be recovered into
		// ABANDONED. Prefer the Lease envelope's ExpiresAt; fall back to
		// the claimed Job's LeaseExpiry when the envelope is unset (test
		// stubs populate only one of the two).
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
		defer func() {
			if run == nil {
				return
			}
			if rec := recover(); rec != nil {
				run.FinishWithPanic(rec)
				finishLedger(run.Report())
				panic(rec)
			}
			switch {
			case retErr != nil:
				run.FinishWithError(retErr)
			case terminalErr != nil:
				run.FinishWithError(terminalErr)
			default:
				run.Finish()
			}
			finishLedger(run.Report())
		}()
	}
	if run == nil {
		defer func() { finishLedger(nil) }()
	}

	jobDir, err := r.workspace.Prepare(lease.Job.ID)
	if err != nil {
		terminalErr = err
		return r.fail(jobCtx, lease, err)
	}
	defer func() {
		_ = r.workspace.Cleanup(lease.Job.ID)
	}()

	var store eventStore
	if s, ok := r.broker.(eventStore); ok {
		store = s
	}
	tools := NewTools(r.broker, store, r.workerID, r.sessionID, lease.Job, jobDir, r.assetClient).WithJobRegistry(ledger)

	// Start lease renewal loop (W1 Phase 7).
	renewCtx, renewCancel := context.WithCancel(jobCtx)
	defer renewCancel()
	renewErrs := make(chan error, 1)
	go r.renewLoop(renewCtx, tools, job.ID, renewErrs, cancel)

	// checkRenew non-blockingly reads any error that the renewal
	// goroutine has already emitted.
	checkRenew := func() error {
		select {
		case err := <-renewErrs:
			if err != nil {
				r.log.Warn("lease renewal failed — failing the job",
					zap.String("job_id", job.ID),
					zap.Error(err))
			}
			return err
		default:
			return nil
		}
	}

	if err := checkRenew(); err != nil {
		terminalErr = err
		return tools.Fail(jobCtx, err.Error())
	}

	// Asset download phase
	if assets := ParseInputAssets(lease.Job.Payload); len(assets) > 0 {
		for i, assetID := range assets {
			if _, err := tools.DownloadAsset(jobCtx, assetID); err != nil {
				downloadErr := fmt.Errorf("download asset %d (%s): %w", i, assetID, err)
				terminalErr = downloadErr
				return tools.Fail(jobCtx, downloadErr.Error())
			}
			ledger.Downloaded(jobCtx, job.ID, assetID, i)
			// FASE 0.2 (July 4 2026) silent-drop rewrite per
			// PR-GODOBJ-14-WORKER-REGISTRY godlike/07 no-fake-availability:
			// pre-PR the runner used `_ = tools.Progress(...)` which
			// dropped the broker emit error without any observability.
			if progErr := tools.Progress(jobCtx, 5+i, "staged input asset"); progErr != nil {
				r.log.Warn("worker Progress emit failed (FASE 0.2 silent-drop rewrite)",
					zap.String("job_id", job.ID),
					zap.String("job_type", job.Type),
					zap.Int("progress", 5+i),
					zap.Error(progErr))
				observability.WorkerProgressEmittedTotal.WithLabelValues(job.Type, "error").Inc()
				observability.WorkerProgressErrorsTotal.WithLabelValues(job.Type, "broker_emit_failed").Inc()
			} else {
				observability.WorkerProgressEmittedTotal.WithLabelValues(job.Type, "success").Inc()
			}
			if err := checkRenew(); err != nil {
				terminalErr = err
				return tools.Fail(jobCtx, err.Error())
			}
		}
	}

	// Handler dispatch
	handlerResult, err = r.registry.Dispatch(jobCtx, lease.Job, tools)
	if err != nil {
		terminalErr = err
		return tools.Fail(jobCtx, err.Error())
	}
	if err := checkRenew(); err != nil {
		terminalErr = err
		return tools.Fail(jobCtx, err.Error())
	}

	// Manifest upload. Only artifact-producing job types should
	// attempt to upload a manifest; non-artifact jobs (e.g.
	// media.reindex) legitimately run with a nil assetClient and
	// a non-empty handlerResult. Calling uploadManifest for those
	// jobs would fail with ErrArtifactClientRequired and
	// incorrectly terminal-fail an otherwise successful job.
	//
	// The typed nil pointer preserves the nil-check semantics:
	// a typed-nil *RemoteArtifactManifest satisfies uploaded != nil
	// correctly, unlike an interface-typed any which would treat a
	// typed-nil pointer as non-nil.
	var uploaded *kerneljob.RemoteArtifactManifest
	if r.registry.ProducesArtifacts(lease.Job.Type) {
		var upErr error
		uploaded, upErr = r.uploadManifest(jobCtx, lease.Job.ID, handlerResult)
		if upErr != nil {
			terminalErr = upErr
			return tools.Fail(jobCtx, upErr.Error())
		}
	}

	var resultJSON json.RawMessage
	if uploaded != nil {
		resultJSON, err = json.Marshal(uploaded)
	} else {
		resultJSON, err = json.Marshal(handlerResult)
	}
	if err != nil {
		terminalErr = err
		return tools.Fail(jobCtx, err.Error())
	}
	if err := checkRenew(); err != nil {
		terminalErr = err
		return tools.Fail(jobCtx, err.Error())
	}

	// Stop renewal BEFORE Complete so a final tick that lands while
	// Complete is mid-flight doesn't try to flip a job we just
	// terminal-reported.
	renewCancel()
	// P0 #5 (July 2026) — fail-closed seam: drain the renewal
	// loop's error channel BEFORE calling tools.Complete.
	if preCompleteErr := postRenewFailClosedCheck(renewErrs); preCompleteErr != nil {
		return preCompleteErr
	}

	// AZIONE 7 (July 2026): branch on registry.ProducesArtifacts.
	if r.registry.ProducesArtifacts(lease.Job.Type) {
		var publishedJSON json.RawMessage
		if uploaded != nil {
			publishedJSON, err = json.Marshal(uploaded)
			if err != nil {
				terminalErr = err
				return tools.Fail(jobCtx, err.Error())
			}
		}
		canonicalAssetIDs, completeErr := tools.CompleteWithArtifacts(jobCtx, resultJSON, publishedJSON, nil)
		if completeErr != nil {
			terminalErr = completeErr
			return completeErr
		}
		ledger.RecordCanonicalOutputs(jobCtx, job.ID, appjobs.OutputRelationForJobType(job.Type), canonicalAssetIDs)
		return nil
	}
	if completeErr := tools.Complete(jobCtx, resultJSON); completeErr != nil {
		terminalErr = completeErr
		return completeErr
	}
	return nil
}
