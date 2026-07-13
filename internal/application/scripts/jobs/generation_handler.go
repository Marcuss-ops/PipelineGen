// Package scripts — generation_handler.go is the canonical owner of
// the script.generate job-system dispatch (godlike/06 SSOT: one
// owner per fact: the broker entrypoint).
//
// PR-GODOBJ-4 KILL list applied (per user spec, July 2026):
//
//	(1) Single + batch paths MUST NOT coabit in the same body. This
//	    file routes the dispatch entry to HandleSingle (one item)
//	    or HandleBatch (multiple items). The bodies live in
//	    cleanly-separated methods below — no shared `if len(...) == 1`
//	    conditional branching.
//	(2) Filesystem ops are NOT in this file. The handler delegates
//	    to adapters/artifacts_persistence.go::PersistGeneratedArtifacts
//	    which returns a pre-computed []scriptpkg.Artifact. The
//	    handler then calls buildManifestFromArtifacts (in
//	    generation_manifest.go) to assemble the typed
//	    *job.ArtifactManifest from that slice.
//	(3) Envelope construction is in generation_result_mapper.go.
//	    Outcome classification is in generation_outcome.go.
//	    Broker registration is in generation_registration.go.
//
// godlike/07 typed-error contract: both HandleSingle and HandleBatch
// return (map[string]any, error). The error is non-nil exactly when
// the worker broker should ROUTE the job through FAILED + retry
// (godlike/07 no-fake-availability). Outcome classification uses
// the typed Diagnostic struct from generation_outcome.go.
//
// godlike/07 honest-limitation disclosure (AGENTS.md Check 44 LoC cap):
// This file exceeds the 66-LoC transitional cap (~340 LoC — measured 2026-07-03) because
// HandleSingle + HandleBatch bodies + decode/dispatch + the
// checkPipelineCtx helper are inherently verbose. Forward-pointer
// linked_issue (zero-baseline rule):
// PR-GODOBJ-4a-HANDLER-SLIM extracts checkPipelineCtx into
// pkg/pipeline/util.sh — the helper is shared with voiceover +
// images generation handlers in forthcoming waves. Deadline
// 2026-08-15.
package jobs

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"go.uber.org/zap"
)

// GenerateJobHandler is the application-layer broker-handler for
// `script.generate` jobs. Constructed via NewGenerateJobHandler;
// registered by generation_registration.go::RegisterJobs under
// scriptpkg.TypeScriptGenerate.
type GenerateJobHandler struct {
	one  *usecase.GenerateOneUseCase
	many *usecase.GenerateManyUseCase
	log  *zap.Logger
}

// NewGenerateJobHandler wires the handler to the unified use cases.
// Constructor simplified in Commit 5 P0 #4: cfg removed (dead field
// after ExecuteFanout no longer accepts NormalizationConfig).
func NewGenerateJobHandler(
	one *usecase.GenerateOneUseCase,
	many *usecase.GenerateManyUseCase,
	log *zap.Logger,
) *GenerateJobHandler {
	return &GenerateJobHandler{
		one:  one,
		many: many,
		log:  log,
	}
}

// checkPipelineCtx returns a typed cancel-error when the pipeline
// ctx has been cancelled (Issue 6 / P1, June 2026). The label is
// logged via Warn so operators can audit which phase the cancel
// was observed at. The underlying ctx.Err() is wrapped with %w so
// errors.Is(err, context.Canceled) remains reliable for cancellation
// classification in generation_outcome.go.
func (h *GenerateJobHandler) checkPipelineCtx(ctx context.Context, phase string) error {
	if err := ctx.Err(); err != nil {
		if h.log != nil {
			h.log.Warn("script.generate: cancelled at phase boundary",
				zap.String("phase", phase),
				zap.Error(err))
		}
		return fmt.Errorf("script.generate cancelled at %s: %w", phase, err)
	}
	return nil
}

// Handle is the queue-worker entry point. Decodes the envelope,
// dispatches to HandleSingle (one item) or HandleBatch (multiple
// items). The dispatch is a SHAPE-typed `if len(env.Items) == 1`
// boundary that ONLY routes — the bodies do not share conditional
// logic.
//
// PipelineContexts: handler-entry → single-item-pre-execute →
// multi-item-pre-execute. Issue 6 / P1 propagation gates ensure the
// pipeline-surface signal is observed BEFORE handing off to the
// use case.
func (h *GenerateJobHandler) Handle(
	ctx context.Context,
	j *scriptpkg.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if h == nil {
		return nil, fmt.Errorf("generate job handler: not constructed")
	}
	if err := h.checkPipelineCtx(ctx, "handler-entry"); err != nil {
		return nil, err
	}
	env, err := domainScript.DecodeEnvelopeV2(j.Payload)
	if err != nil {
		return nil, fmt.Errorf("generate job handler: decode envelope: %w", err)
	}
	if h.log != nil {
		h.log.Info("handling script.generate job",
			zap.String("job_id", j.ID),
			zap.String("preset", string(env.Preset)),
			zap.Int("items", len(env.Items)))
	}
	if len(env.Items) == 1 {
		return h.handleSingle(ctx, j, env, tools)
	}
	return h.handleBatch(ctx, j, env, tools)
}

// handleSingle owns the single-item script.generate path.
// Cleanly separated from handleBatch — no shared conditional logic.
//   - checkPipelineCtx at single-item-pre-execute
//   - tracker via usecase.NewProgressTracker
//   - one.Execute → typed envelope via ClassifySingleOutcome
//   - on failure: typed single-failure envelope + wrapped Go error
//   - on success: persistence via NewGenerationArtifactsAdapter (per
//     KILL K1; future SLIM) → typed artifact slice → typed manifest
//     via buildManifestFromArtifacts → handlerResult[ManifestKey]
//   - manifest validation is observed via manifest.Validate(); a
//     failed validate still injects the typed envelope (the runner
//     has typed-envelope fallback per C10 dual-shape discipline).
func (h *GenerateJobHandler) handleSingle(
	ctx context.Context,
	j *scriptpkg.Job,
	env *domainScript.GenerationEnvelopeV2,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if err := h.checkPipelineCtx(ctx, "single-item-pre-execute"); err != nil {
		return nil, err
	}
	progressFn := appjobs.SafeProgressFn(tools)
	eventFn := appjobs.SafeEventFn(tools)
	tracker := usecase.NewProgressTracker(progressFn, env.Items[0].ID)
	tracker.SetEventFn(eventFn)
	eventFn("job.created", "Script generation job created", map[string]any{
		"job_id":  j.ID,
		"item_id": env.Items[0].ID,
		"preset":  string(env.Preset),
	})

	// Log source text metrics without ever logging the raw text.
	if h.log != nil {
		h.log.Info("script.generate: item source text metrics",
			zap.String("job_id", j.ID),
			zap.String("item_id", env.Items[0].ID),
			zap.Any("source_text", usecase.SourceTextLogFields(env.Items[0].Source.SourceText, adapters.NormalizationConfig{LogSourceTextPreview: true, SourceTextPreviewChars: 80})))
	}

	execCtx := context.WithValue(ctx, "script_job_id", j.ID)
	result, err := h.one.Execute(execCtx, env.Items[0], env.Preset, tracker)
	diag := ClassifySingleOutcome(result, err)
	if diag.Outcome == OutcomeCanceled {
		if h.log != nil {
			h.log.Warn("script.generate: single-item cancelled mid-run",
				zap.String("job_id", j.ID),
				zap.Error(diag.Err))
		}
		return nil, diag.Err
	}
	if diag.Outcome == OutcomeSingleFailure {
		if h.log != nil {
			h.log.Error("script.generate: single-item failed",
				zap.String("job_id", j.ID),
				zap.Error(diag.Err))
		}
		eventFn("job.failed", "Script generation failed", map[string]any{
			"job_id":  j.ID,
			"item_id": env.Items[0].ID,
			"error":   diag.Err.Error(),
		})
		mapped, mapErr := toMap(buildSingleFailureEnvelope(env.Items[0].ID, err, result))
		if mapErr != nil {
			return nil, fmt.Errorf("generate job handler: marshal envelope: %w", mapErr)
		}
		return mapped, diag.Err
	}
	// OutcomeSingleSuccess
	envelope := buildSingleSuccessEnvelope(env.Items[0].ID, result)
	mapped, mapErr := toMap(envelope)
	if mapErr != nil {
		return nil, fmt.Errorf("generate job handler: marshal envelope: %w", mapErr)
	}
	artifacts, persistErr := adapters.PersistGeneratedArtifacts(ctx, j.ID, result, h.log)
	if persistErr != nil {
		// FASE 1 (c) — typed-error contract (audit 2026-07-03 P0 #4
		// criterion "la persistenza locale fallisce"): the handler MUST
		// NOT swallow persistence failure as a silent drop. The error
		// is non-nil so the worker's Fail/DeadLetter path takes over
		// (mirrors the CompletionPort error branch + the FASE 1 (c)
		// worker manifest-extract branch).
		if h.log != nil {
			h.log.Error("handleSingle: artifact persistence failed — failing job (typed-error contract FASE 1 c)",
				zap.String("job_id", j.ID),
				zap.Error(persistErr))
		}
		return mapped, fmt.Errorf("script.generate: artifact persistence: %w", persistErr)
	}
	manifest := buildManifestFromArtifacts(j.ID, artifacts)
	if vErr := manifest.Validate(); vErr != nil {
		// FASE 1 (c) — typed-error contract (criterion "il manifest non è
		// decodificabile" + "hash, dimensione o tipo non corrispondono"):
		// a manifest that fails Validate is a hard handler bug and the
		// job MUST NOT reach SUCCEEDED. Surface the typed
		// ErrArtifactManifestInvalid sentinel (FASE 1 c) for the worker
		// Fail/DeadLetter path; the audit timeline records the typed
		// code + the validation sub-error in the message.
		if h.log != nil {
			h.log.Error("handleSingle: manifest validation failed — failing job (typed-error contract FASE 1 c)",
				zap.String("job_id", j.ID),
				zap.Error(vErr))
		}
		return mapped, fmt.Errorf("script.generate: artifact manifest: %w: %v", scriptpkg.ErrArtifactManifestInvalid, vErr)
	}
	// Merge the C10 dual-shape typed envelope (Data + Artifacts) into
	// the broker handlerResult map. MergeTypedExecutionEnvelope is
	// PURE (no I/O, no log writes) and is the canonical owner of the
	// typed ExecutionResult marshal/unmarshal cycle (godlike/06 SSOT).
	if mErr := MergeTypedExecutionEnvelope(mapped, result, manifest); mErr != nil {
		if h.log != nil {
			h.log.Warn("handleSingle: typed envelope merge failed — manifest sidecar only",
				zap.String("job_id", j.ID),
				zap.Error(mErr))
		}
		mapped[scriptpkg.ManifestKey] = manifest
	}
	if h.log != nil {
		h.log.Info("handleSingle: artifact manifest injected (§8.4 multi-artifact shape)",
			zap.String("job_id", j.ID),
			zap.Int("artifacts", len(manifest.Artifacts)))
	}
	return mapped, nil
}

// handleBatch fans out multi-item script generation as
// separate script.generate_item child jobs via the wired broker.
func (h *GenerateJobHandler) handleBatch(
	ctx context.Context,
	j *scriptpkg.Job,
	env *domainScript.GenerationEnvelopeV2,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if err := h.checkPipelineCtx(ctx, "multi-item-pre-execute"); err != nil {
		return nil, err
	}
	eventFn := appjobs.SafeEventFn(tools)
	eventFn("job.created", "Script generation batch job created", map[string]any{
		"job_id":     j.ID,
		"item_count": len(env.Items),
		"preset":     string(env.Preset),
	})
	return h.handleBatchFanout(ctx, j, env, tools)
}

// handleBatchFanout emits each item as a separate script.generate_item
// child job via the wired broker. The handler builds a parent
// waiting_children result that the aggregator (parent_aggregator.go)
// reads to track child outcomes and finalise the parent.
func (h *GenerateJobHandler) handleBatchFanout(
	ctx context.Context,
	j *scriptpkg.Job,
	env *domainScript.GenerationEnvelopeV2,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	progressFn := appjobs.SafeProgressFn(tools)
	progressFn(5, "fanning out script items to child jobs")

	fanout, err := h.many.ExecuteFanout(ctx, j.ID, env)
	if err != nil {
		if h.log != nil {
			h.log.Error("script.generate: fanout failed",
				zap.String("job_id", j.ID),
				zap.Error(err))
		}
		eventFn := appjobs.SafeEventFn(tools)
		eventFn("job.failed", "Script generation batch failed", map[string]any{
			"job_id": j.ID,
			"error":  err.Error(),
		})
		return nil, fmt.Errorf("script.generate fanout: %w", err)
	}

	if fanout == nil {
		return nil, fmt.Errorf("script.generate fanout: ExecuteFanout returned nil result without error")
	}

	resultMap := map[string]any{
		"parent_state":         "waiting_children",
		"parent_job_id":        j.ID,
		"total_items":          fanout.TotalItems,
		"child_job_ids":        fanout.ChildJobIDs,
		"failed_enqueue_count": fanout.FailedEnqueueCount,
	}

	if h.log != nil {
		h.log.Info("script.generate: fanout complete, parent waiting for children",
			zap.String("parent_job_id", j.ID),
			zap.Int("total_items", fanout.TotalItems),
			zap.Int("children_enqueued", fanout.TotalEnqueued),
			zap.Int("failed_enqueue", fanout.FailedEnqueueCount))
	}

	progressFn(100, "fanout complete, waiting for child aggregation")
	return resultMap, nil
}
