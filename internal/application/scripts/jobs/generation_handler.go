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
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

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
	log           *zap.Logger
	dispatcher    GenerationDispatcher
	runRepo       scriptgen.RunRepository
	durableRunner *scriptgen.Runner
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

// SetDurableRunner wires the canonical single-item generation runtime. The
// worker owns execution after the submission job is committed; the HTTP
// starter deliberately does not launch a second local runner.
func (h *GenerateJobHandler) SetDurableRunner(runner *scriptgen.Runner) {
	if h != nil {
		h.durableRunner = runner
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
		// The submission transaction commits the job before the HTTP handler
		// can persist the canonical run job_id. A fast worker may therefore claim
		// this job in the small correlation window. The submission service
		// mirrors Idempotency-Key into job.CorrelationID, so use that durable
		// identity as the deterministic fallback instead of bypassing the
		// canonical run ledger.
		if run == nil && j != nil && j.CorrelationID != "" {
			if finder, ok := h.runRepo.(interface {
				GetByIdempotencyKey(context.Context, string) (*scriptgen.GenerationRun, error)
			}); ok {
				run, _ = finder.GetByIdempotencyKey(ctx, j.CorrelationID)
				if run != nil && run.JobID == "" {
					if setter, ok := h.runRepo.(interface {
						SetJobID(context.Context, string, string) error
					}); ok {
						// Best-effort self-healing: the worker already has the
						// authoritative job ID, so close the race for retries and
						// GET /full without changing execution semantics.
						_ = setter.SetJobID(ctx, run.ID, j.ID)
						run.JobID = j.ID
					}
				}
			}
		}
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

	// Single-item jobs use the durable capability runtime so the same
	// CanonicalTimeline and RenderPlan flow reaches the canonical Velox
	// executor. Batch jobs retain the existing fan-out dispatcher until
	// their per-item runtime is migrated to the same port.
	if h.durableRunner != nil && len(env.Items) == 1 && run != nil {
		runRequest, buildErr := scriptgen.BuildGenerateRequest(env, run.Request.IdempotencyKey)
		if buildErr != nil {
			_ = h.runRepo.FailRun(ctx, scriptgen.FailRunInput{RunID: run.ID, FailedStage: scriptgen.StageCompilingAudio, ErrorCode: "INVALID_GENERATION_REQUEST", ErrorMessage: buildErr.Error()})
			return nil, fmt.Errorf("generate job handler: build durable request: %w", buildErr)
		}
		parentLink := job.ParentLinkFromPayload(j.Payload)
		execution := scriptgen.ExecutionContext{
			RootJobID:     run.ID,
			JobID:         j.ID,
			ParentJobID:   parentLink.ParentJobID,
			ProjectID:     j.Project,
			VideoID:       j.VideoName,
			CorrelationID: j.CorrelationID,
		}
		h.durableRunner.ExecuteWithContext(ctx, run.ID, runRequest, execution)
		updated, getErr := h.runRepo.Get(ctx, run.ID)
		if getErr != nil {
			return nil, fmt.Errorf("generate job handler: read durable run result: %w", getErr)
		}
		if updated == nil || updated.Status != scriptgen.RunStatusCompleted {
			if updated != nil && updated.ErrorMessage != "" {
				return nil, fmt.Errorf("generate job handler: durable run failed: %s", updated.ErrorMessage)
			}
			return nil, fmt.Errorf("generate job handler: durable run did not complete")
		}
		// The durable runner owns generation/render execution, but the broker
		// still requires the same canonical artifact manifest as the legacy
		// dispatcher path. Rehydrate the domain result and use the single
		// artifact persister; never return a successful artifact-producing job
		// without its manifest sidecar.
		domainResult := scriptgen.DurableResultToDomain(updated.Result)
		if domainResult == nil {
			return nil, fmt.Errorf("generate job handler: durable result is empty")
		}
		artifacts, persistErr := adapters.PersistGeneratedArtifacts(ctx, j.ID, domainResult, h.log)
		if persistErr != nil {
			return nil, fmt.Errorf("generate job handler: durable artifact persistence: %w", persistErr)
		}
		manifest := buildManifestFromArtifacts(j.ID, artifacts)
		if validateErr := manifest.Validate(); validateErr != nil {
			return nil, fmt.Errorf("generate job handler: durable artifact manifest: %w", validateErr)
		}
		return map[string]any{"run_id": run.ID, "parent_state": "completed", "result": updated.Result, job.ManifestKey: manifest}, nil
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
			// The parent aggregator owns terminal completion for batches. The
			// run stays RUNNING in the scene-generation phase while children
			// produce their per-item text.
			_ = h.runRepo.UpdateStage(ctx, run.ID, scriptgen.RunStatusRunning, scriptgen.StageGeneratingSceneText)
		} else {
			_ = h.runRepo.UpdateStage(ctx, run.ID, scriptgen.RunStatusCompleted, scriptgen.StageCompleted)
		}
	}
	return result, dispatchErr
}
