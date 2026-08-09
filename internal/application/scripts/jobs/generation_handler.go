// Package scripts — generation_handler.go is the canonical owner of
// the script.generate job-system entry point (godlike/06 SSOT: one
// owner per fact: the broker entrypoint).
//
// PR-GODOBJ-4 KILL list applied (per user spec, July 2026):
//
//	(1) Single + batch paths MUST NOT cohabit in the same body.
//	    This file now only parses the envelope and delegates to
//	    generation_dispatcher.go, which routes to the single or
//	    batch executor.
//	(2) Filesystem ops are NOT in this file. They live in the
//	    single executor (generation_single_executor.go) which
//	    delegates persistence to adapters/artifacts_persistence.go.
//	(3) Envelope construction is in generation_result_mapper.go.
//	    Outcome classification is in generation_outcome.go.
//	    Broker registration is in generation_registration.go.
//
// godlike/07 typed-error contract: the handler returns
// (map[string]any, error). The error is non-nil exactly when the
// worker broker should ROUTE the job through FAILED + retry
// (godlike/07 no-fake-availability).
package jobs

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"go.uber.org/zap"
)

// GenerateJobHandler is the application-layer broker-handler for
// `script.generate` jobs. Constructed via NewGenerateJobHandler;
// registered by generation_registration.go::RegisterJobs under
// job.TypeScriptGenerate.
//
// The handler is intentionally thin: it decodes the envelope and
// delegates execution to a GenerationDispatcher. All single/batch
// policy lives in the executors.
type GenerateJobHandler struct {
	log        *zap.Logger
	dispatcher GenerationDispatcher
	runRepo    scriptgen.RunRepository
}

// NewGenerateJobHandler wires the handler to the unified use cases.
// It internally builds the single/batch executors and the dispatcher.
func NewGenerateJobHandler(
	one *usecase.GenerateOneUseCase,
	many *usecase.GenerateManyUseCase,
	log *zap.Logger,
	runRepo ...scriptgen.RunRepository,
) *GenerateJobHandler {
	var repo scriptgen.RunRepository
	if len(runRepo) > 0 {
		repo = runRepo[0]
	}
	return &GenerateJobHandler{
		log:     log,
		runRepo: repo,
		dispatcher: NewGenerationDispatcher(
			NewSingleGenerationExecutor(one, log),
			NewBatchGenerationExecutor(many, log),
		),
	}
}

// SetRunRepository wires the optional durable run lifecycle observer after
// the composition root has built the SQLite adapter. Keeping this setter
// separate avoids moving repository construction ahead of use-case wiring.
func (h *GenerateJobHandler) SetRunRepository(repo scriptgen.RunRepository) {
	if h != nil {
		h.runRepo = repo
	}
}

// checkPipelineCtx returns a typed cancel-error when the pipeline
// ctx has been cancelled. The label is logged via Warn so operators
// can audit which phase the cancel was observed at. The underlying
// ctx.Err() is wrapped with %w so errors.Is(err, context.Canceled)
// remains reliable for cancellation classification.
func checkPipelineCtx(ctx context.Context, logger *zap.Logger, phase string) error {
	if err := ctx.Err(); err != nil {
		if logger != nil {
			logger.Warn("script.generate: cancelled at phase boundary",
				zap.String("phase", phase),
				zap.Error(err))
		}
		return fmt.Errorf("script.generate cancelled at %s: %w", phase, err)
	}
	return nil
}

// Handle is the queue-worker entry point. It decodes the envelope
// and delegates to the GenerationDispatcher. The dispatcher routes
// single-item envelopes to the SingleGenerationExecutor and
// multi-item envelopes to the BatchGenerationExecutor.
func (h *GenerateJobHandler) Handle(
	ctx context.Context,
	j *job.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if h == nil || h.dispatcher == nil {
		return nil, fmt.Errorf("generate job handler: not constructed")
	}
	if err := checkPipelineCtx(ctx, h.log, "handler-entry"); err != nil {
		return nil, err
	}
	var run *scriptgen.GenerationRun
	if h.runRepo != nil {
		run, _ = h.runRepo.GetByJobID(ctx, j.ID)
		if run != nil {
			// The HTTP starter creates the run before the job is committed;
			// the worker is the owner of the execution lifecycle thereafter.
			_ = h.runRepo.UpdateStage(ctx, run.ID, scriptgen.RunStatusRunning, scriptgen.StageGeneratingSceneText)
		}
	}

	env, err := domainScript.DecodeEnvelopeV2(j.Payload)
	if err != nil {
		if run != nil {
			_ = h.runRepo.FailRun(ctx, scriptgen.FailRunInput{
				RunID:        run.ID,
				FailedStage:  scriptgen.StageFailed,
				ErrorCode:    "INVALID_GENERATION_PAYLOAD",
				ErrorMessage: err.Error(),
			})
		}
		return nil, fmt.Errorf("generate job handler: decode envelope: %w", err)
	}

	if h.log != nil {
		h.log.Info("handling script.generate job",
			zap.String("job_id", j.ID),
			zap.String("preset", string(env.Preset)),
			zap.Int("items", len(env.Items)))
	}

	result, dispatchErr := h.dispatcher.Dispatch(ctx, j, env, tools)
	if run != nil {
		if dispatchErr != nil {
			_ = h.runRepo.FailRun(ctx, scriptgen.FailRunInput{
				RunID:        run.ID,
				FailedStage:  scriptgen.StageFailed,
				ErrorCode:    "SCRIPT_GENERATION_FAILED",
				ErrorMessage: dispatchErr.Error(),
			})
		} else if parentState, _ := result["parent_state"].(string); parentState == "waiting_children" {
			// The parent aggregator owns terminal completion for batches.
			_ = h.runRepo.UpdateStage(ctx, run.ID, scriptgen.RunStatusRunning, scriptgen.StageWorkerQueued)
		} else {
			_ = h.runRepo.UpdateStage(ctx, run.ID, scriptgen.RunStatusCompleted, scriptgen.StageCompleted)
		}
	}
	return result, dispatchErr
}
