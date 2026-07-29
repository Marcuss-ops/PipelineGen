package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	// kerneljob is aliased because runLease binds the local variable
	// `job := lease.Job`; using the bare package name would shadow it.
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
func (r *Runner) runLease(parent context.Context, lease *appjobs.Lease) error {
	job := lease.Job

	// Defensive: the claim filter should prevent this, but verify the
	// claimed job type is actually supported before doing any work.
	if !r.registry.Has(job.Type) {
		r.log.Error("claimed unsupported job type — releasing",
			zap.String("job_type", job.Type),
			zap.String("job_id", job.ID),
		)
		return r.fail(parent, lease, fmt.Errorf("%w: %s", ErrHandlerNotRegistered, job.Type))
	}

	jobCtx, cancel := context.WithCancel(parent)
	defer cancel()

	jobDir, err := r.workspace.Prepare(lease.Job.ID)
	if err != nil {
		return r.fail(jobCtx, lease, err)
	}
	defer func() {
		_ = r.workspace.Cleanup(lease.Job.ID)
	}()

	var store eventStore
	if s, ok := r.broker.(eventStore); ok {
		store = s
	}
	tools := NewTools(r.broker, store, r.workerID, r.sessionID, lease.Job, jobDir, r.assetClient)

	// Start lease renewal loop (W1 Phase 7).
	renewCtx, renewCancel := context.WithCancel(jobCtx)
	defer renewCancel()
	renewErrs := make(chan error, 1)
	go r.renewLoop(renewCtx, tools, job.ID, renewErrs)

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
		return tools.Fail(jobCtx, err.Error())
	}

	// Asset download phase
	if assets := ParseInputAssets(lease.Job.Payload); len(assets) > 0 {
		for i, assetID := range assets {
			if _, err := tools.DownloadAsset(jobCtx, assetID); err != nil {
				return tools.Fail(jobCtx, fmt.Errorf("download asset %d (%s): %w", i, assetID, err).Error())
			}
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
				return tools.Fail(jobCtx, err.Error())
			}
		}
	}

	// Handler dispatch
	handlerResult, err := r.registry.Dispatch(jobCtx, lease.Job, tools)
	if err != nil {
		return tools.Fail(jobCtx, err.Error())
	}
	if err := checkRenew(); err != nil {
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
		return tools.Fail(jobCtx, err.Error())
	}
	if err := checkRenew(); err != nil {
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
				return tools.Fail(jobCtx, err.Error())
			}
		}
		return tools.CompleteWithArtifacts(jobCtx, resultJSON, publishedJSON, nil)
	}
	return tools.Complete(jobCtx, resultJSON)
}

// sleep wraps time.Sleep for testability (package-level var swap in tests).
var sleep = time.Sleep
