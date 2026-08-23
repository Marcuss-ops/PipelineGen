// Package scripts — generation_single_executor.go owns the
// single-item script.generate execution path (PR-GODOBJ-4 KILL list,
// July 2026). It is invoked by the GenerationDispatcher when the
// envelope contains exactly one item.
//
// Responsibilities:
//   - pipeline context cancellation check at single-item boundary
//   - progress tracker + event emission
//   - source text metrics logging (redacted)
//   - dispatch to GenerateOneUseCase.Execute
//   - outcome classification via ClassifySingleOutcome
//   - artifact persistence, manifest validation, typed envelope merge
//
// The executor returns a (map[string]any, error) pair so the worker
// broker can route FAILED vs COMPLETED correctly (godlike/07
// no-fake-availability).
package jobs

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// SingleGenerationExecutor is the narrow port for single-item
// script generation. The production implementation is
// singleGenerationExecutor; tests may inject stubs.
type SingleGenerationExecutor interface {
	Execute(
		ctx context.Context,
		j *job.Job,
		env *domainScript.GenerationEnvelopeV2,
		tools *appjobs.JobTools,
	) (map[string]any, error)
}

// singleGenerationExecutor implements SingleGenerationExecutor using
// the canonical GenerateOneUseCase.
type singleGenerationExecutor struct {
	one *usecase.GenerateOneUseCase
	log *zap.Logger
}

// NewSingleGenerationExecutor constructs the canonical
// SingleGenerationExecutor. one may be nil; Execute will fail-closed
// at runtime rather than panic.
func NewSingleGenerationExecutor(one *usecase.GenerateOneUseCase, log *zap.Logger) SingleGenerationExecutor {
	return &singleGenerationExecutor{
		one: one,
		log: log,
	}
}

// Execute runs the single-item script generation pipeline.
// It preserves the exact behavior of the former
// GenerateJobHandler.handleSingle method.
func (e *singleGenerationExecutor) Execute(
	ctx context.Context,
	j *job.Job,
	env *domainScript.GenerationEnvelopeV2,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if e == nil {
		return nil, fmt.Errorf("single generation executor: not constructed")
	}
	if e.one == nil {
		return nil, fmt.Errorf("single generation executor: GenerateOneUseCase not configured")
	}
	if err := checkPipelineCtx(ctx, e.log, "single-item-pre-execute"); err != nil {
		return nil, err
	}

	item := env.Items[0]
	// The envelope-level control applies to every item. Carry it into the
	// item plan consumed by the engine; otherwise the HTTP idempotency layer
	// bypasses replay while the application generation cache still returns an
	// exact hit.
	if env.ForceRefresh {
		item.ScriptParams.ForceRefresh = true
	}

	progressFn := appjobs.SafeProgressFn(tools)
	eventFn := appjobs.SafeEventFn(tools)
	tracker := usecase.NewProgressTracker(progressFn, item.ID)
	tracker.SetEventFn(eventFn)
	tracker.TrackStage(string(job.StageScript), item.Language, string(job.StageRunning), j.ID, "")
	tracker.SetEventFn(eventFn)
	eventFn("job.created", "Script generation job created", map[string]any{
		"job_id":  j.ID,
		"item_id": item.ID,
		"preset":  string(env.Preset),
	})

	// Log source text metrics without ever logging the raw text.
	if e.log != nil {
		e.log.Info("script.generate: item source text metrics",
			zap.String("job_id", j.ID),
			zap.String("item_id", item.ID),
			zap.Any("source_text", usecase.SourceTextLogFields(item.Source.SourceText, adapters.NormalizationConfig{LogSourceTextPreview: true, SourceTextPreviewChars: 80})))
	}

	execCtx := context.WithValue(ctx, "script_job_id", j.ID)
	result, err := e.one.Execute(execCtx, item, env.Preset, tracker)
	if err != nil {
		tracker.TrackStage(string(job.StageScript), item.Language, string(job.StageFailed), j.ID, err.Error())
	} else {
		tracker.TrackStage(string(job.StageScript), item.Language, string(job.StageCompleted), j.ID, "")
	}
	diag := ClassifySingleOutcome(result, err)
	if diag.Outcome == OutcomeCanceled {
		if e.log != nil {
			e.log.Warn("script.generate: single-item cancelled mid-run",
				zap.String("job_id", j.ID),
				zap.Error(diag.Err))
		}
		return nil, diag.Err
	}
	if diag.Outcome == OutcomeSingleFailure {
		if e.log != nil {
			e.log.Error("script.generate: single-item failed",
				zap.String("job_id", j.ID),
				zap.Error(diag.Err))
		}
		eventFn("job.failed", "Script generation failed", map[string]any{
			"job_id":  j.ID,
			"item_id": item.ID,
			"error":   diag.Err.Error(),
		})
		mapped, mapErr := toMap(buildSingleFailureEnvelope(item.ID, err, result))
		if mapErr != nil {
			return nil, fmt.Errorf("generate job handler: marshal envelope: %w", mapErr)
		}
		return mapped, diag.Err
	}

	// OutcomeSingleSuccess
	envelope := buildSingleSuccessEnvelope(item.ID, result)
	mapped, mapErr := toMap(envelope)
	if mapErr != nil {
		return nil, fmt.Errorf("generate job handler: marshal envelope: %w", mapErr)
	}

	artifacts, persistErr := adapters.PersistGeneratedArtifacts(ctx, j.ID, result, e.log)
	if persistErr != nil {
		// FASE 1 (c) — typed-error contract: the handler MUST NOT
		// swallow persistence failure as a silent drop. The error is
		// non-nil so the worker's Fail/DeadLetter path takes over.
		if e.log != nil {
			e.log.Error("single executor: artifact persistence failed — failing job (typed-error contract FASE 1 c)",
				zap.String("job_id", j.ID),
				zap.Error(persistErr))
		}
		return mapped, fmt.Errorf("script.generate: artifact persistence: %w", persistErr)
	}

	manifest := buildManifestFromArtifacts(j.ID, artifacts)
	if vErr := manifest.Validate(); vErr != nil {
		// FASE 1 (c) — typed-error contract: a manifest that fails
		// Validate is a hard handler bug and the job MUST NOT reach
		// SUCCEEDED.
		if e.log != nil {
			e.log.Error("single executor: manifest validation failed — failing job (typed-error contract FASE 1 c)",
				zap.String("job_id", j.ID),
				zap.Error(vErr))
		}
		return mapped, fmt.Errorf("script.generate: artifact manifest: %w: %v", job.ErrArtifactManifestInvalid, vErr)
	}

	// Merge the C10 dual-shape typed envelope (Data + Artifacts) into
	// the broker handlerResult map.
	if mErr := MergeTypedExecutionEnvelope(mapped, result, manifest); mErr != nil {
		if e.log != nil {
			e.log.Warn("single executor: typed envelope merge failed — manifest sidecar only",
				zap.String("job_id", j.ID),
				zap.Error(mErr))
		}
		mapped[job.ManifestKey] = manifest
	}
	if e.log != nil {
		e.log.Info("single executor: artifact manifest injected (§8.4 multi-artifact shape)",
			zap.String("job_id", j.ID),
			zap.Int("artifacts", len(manifest.Artifacts)))
	}
	return mapped, nil
}
