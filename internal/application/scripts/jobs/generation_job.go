// Package scripts — generation_job.go is the single job-system
// handler for `script.generate` jobs. It decodes a
// GenerationEnvelopeV2 from the job payload and delegates to
// GenerateOneUseCase (single item) or GenerateManyUseCase
// (multiple items).
//
// This handler replaces the fragmented per-job-type handlers:
//   - PipelineUseCase.HandleJob (script.generate_from_clips)
//   - BatchJobHandler.Handle (script.generate_batch)
//   - CatalogJobServiceImpl.HandleCatalogScriptGenerateJob
//     (script.generate_from_catalog)
//
// All job-type registration lives in GenerateJobHandler.RegisterJobs.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	ports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"

	"go.uber.org/zap"
)

// GenerateJobHandler is the application-layer job-system handler for
// `script.generate` jobs. Registered via the jobs broker in
// wire_script.go:
//
//	root.Jobs.Service.RegisterHandler(job.TypeScriptGenerate,
//	    genJobHandler.Handle)
type GenerateJobHandler struct {
	one  *usecase.GenerateOneUseCase
	many *usecase.GenerateManyUseCase
	cfg  adapters.NormalizationConfig
	log  *zap.Logger
}

// NewGenerateJobHandler wires the handler to the unified use cases.
func NewGenerateJobHandler(
	one *usecase.GenerateOneUseCase,
	many *usecase.GenerateManyUseCase,
	cfg adapters.NormalizationConfig,
	log *zap.Logger,
) *GenerateJobHandler {
	return &GenerateJobHandler{
		one:  one,
		many: many,
		cfg:  cfg,
		log:  log,
	}
}

// Handle is the queue-worker entry point. Decodes the envelope,
// dispatches to single or batch generation, builds a typed
// GenerationEnvelopeResult, and serialises it to the job-system
// map at the boundary.
//
// PR 7 (June 2026): single-item and multi-item paths emit the
// same canonical envelope shape (Version + OK + Items + Summary).
// The legacy `Single` field is gone; the boundary `toMap` is a
// straight marshal/unmarshal cycle on the typed envelope.
//
// P0 (Issue 1, June 2026): contract clarification for the worker
// dispatch boundary. Every failure path returns a non-nil Go
// error so the worker treats the job as FAILED and triggers retry
// instead of marking it COMPLETED with ok=false. The three
// outcomes are:
//
//   (a) Single-item failure        → (mapped_envelope, wrapped_err)
//   (b) Multi-item pure infra fail → (nil, err)
//   (c) Multi-item all-failed      → (mapped_envelope, wrapped_err)
//   (d) Multi-item partial/full    → (mapped_envelope, nil)
//
// Cases (a) and (c) used to return (mapped_envelope, nil) which
// caused the worker to mark the job as COMPLETED on the
// /api/script/jobs/:id/full wire even when the generation actually
// failed (status="COMPLETED", result.ok=false, summary.failed>0).
// Retry never kicked in because dispatchErr==nil. The fix wraps
// the typed envelope result with a Go error so the broker sees
// dispatch failure and routes the job through the FAILED path.
// checkPipelineCtx returns a typed cancel-error when the pipeline
// ctx has been cancelled. The label is logged via Warn so operators
// can audit which phase the cancel was observed at. Issue 6
// (June 2026, P1) propagates ctx.Err() across the 5 user-visible
// pipeline phases: source resolver, Ollama, postprocessors,
// voiceover scenes, image generation -- each phase boundary in
// the unified Handle below calls this helper. The labelling is
// for operator audit; the underlying ctx.Err() is wrapped with
// %w so errors.Is(err, context.Canceled) remains reliable.
func (h *GenerateJobHandler) checkPipelineCtx(ctx context.Context, phase string) error {
	if err := ctx.Err(); err != nil {
		if h.log != nil {
			h.log.Warn("script.generate: cancelled at phase boundary",
				zap.String("job_id_or_phase", phase),
				zap.Error(err))
		}
		return fmt.Errorf("script.generate cancelled at %s: %w", phase, err)
	}
	return nil
}

func (h *GenerateJobHandler) Handle(
	ctx context.Context,
	j *scriptpkg.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if h == nil {
		return nil, fmt.Errorf("generate job handler: not constructed")
	}

	// Issue 6 (P1): Phase 0 / handler-entry cancellation short-circuit.
	// The user-press-cancel watcher in worker_execution.go flips
	// jobCtx.Done() in <=2 seconds; handlers must propagate that
	// signal into the pipeline so source resolver / Ollama /
	// postprocessors / voiceover scenes / image generation all
	// bail out instead of running for the full 60-min job timeout.
	if err := h.checkPipelineCtx(ctx, "handler-entry"); err != nil {
		return nil, err
	}

	// Decode the envelope.
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

	// Pipe progress through.
	var progressFn func(int, string)
	if tools != nil && tools.Progress != nil {
		progressFn = tools.Progress
	}

	if len(env.Items) == 1 {
		// Single-item path (PR 7): even single-item runs emit the
		// canonical envelope shape (Version=2, Items=[1], Summary
		// counts). No special `Single` flattening.
		//
		// P0 (Issue 1): on per-item failure, return BOTH the
		// typed failure envelope AND a wrapped Go error. The
		// worker treats dispatchErr != nil as FAILED → triggers
		// retry and never marks the job COMPLETED with ok=false.
		//
		// Issue 6 (P1): Phase 1B / single-item pipeline gate.
		// Generous use case = source resolver (Phase 1A) →
		// Ollama (Phase 1B) → postprocessors (Phase 1C) →
		// voiceover (Phase 1D) → image generation (Phase 1E);
		// the use case internally re-checks ctx.Err() at each
		// phase boundary, but this top-level check ensures the
		// signal is observed BEFORE we hand off to the use
		// case so e.g. a cancel mid-BuildPlan doesn't waste an
		// Ollama round-trip.
		if err := h.checkPipelineCtx(ctx, "single-item-pre-execute"); err != nil {
			return nil, err
		}
		tracker := usecase.NewProgressTracker(progressFn, env.Items[0].ID)
		single, err := h.one.Execute(ctx, env.Items[0], env.Preset, tracker)
		if err != nil {
			// Issue 6 (P1): Phase 1F / single-item post-cancel.
			// If the failure was a context.Canceled signal
			// (versus an infra error), surface it as the cancel
			// so the worker does not retry a job the user
			// already pressed cancel on.
			if errors.Is(err, context.Canceled) {
				if h.log != nil {
					h.log.Warn("script.generate: single-item cancelled mid-run",
						zap.String("job_id", j.ID),
						zap.Error(err))
				}
				return nil, fmt.Errorf("script.generate cancelled mid-single-item: %w", err)
			}
			if h.log != nil {
				h.log.Error("script.generate: single-item failed",
					zap.String("job_id", j.ID),
					zap.Error(err))
			}
			mapped, mapErr := toMap(buildSingleFailureEnvelope(env.Items[0].ID, err.Error()))
			if mapErr != nil {
				return nil, fmt.Errorf("generate job handler: marshal envelope: %w", mapErr)
			}
			return mapped, fmt.Errorf("script generation failed: %w", err)
		}
		return toMap(buildSingleSuccessEnvelope(env.Items[0].ID, single))
	}

	// Multi-item path. Three outcomes (P0, Issue 1):
	//
	//   (a) Pure infra failure (e.g. use case not constructed,
	//       envelope nil): return (nil, err). The worker sees
	//       dispatchErr != nil and marks FAILED.
	//   (b) All items failed (Summary.Failed == Summary.Total
	//       and Total > 0): return (mapped_envelope, wrapped_err).
	//       The worker still sees the partial envelope for
	//       /api/script/jobs/:id/full reading, but the Go-level
	//       error routes the job through FAILED + retry.
	//   (c) Partial or full success: return (mapped_envelope, nil).
	//       The envelope.ok=false still signals operator-visible
	//       partial failure, but at least one item completed so
	//       the job is treated as COMPLETED.
	//
	// (b) used to silently leak as (mapped_envelope, nil) under the
	// old `if err != nil { return manyResult-or-err }` shape, which
	// caused the worker to mark the job COMPLETED with summary.failed
	// == summary.total.
	//
	// Issue 6 (P1): Phase 2B / multi-item pipeline gate.
	if err := h.checkPipelineCtx(ctx, "multi-item-pre-execute"); err != nil {
		return nil, err
	}
	manyResult, err := h.many.Execute(ctx, env, h.cfg, progressFn)

	// Issue 6 (P1): Phase 2C / multi-item post-cancel.
	// If the use case observed a ctx.Canceled signal and returned
	// it via err while still producing a partial-or-full result,
	// surface the cancel rather than swallowing it as a partial
	// success -- the user pressed cancel; we honour the signal
	// and the worker does not retry.
	if manyResult != nil && errors.Is(err, context.Canceled) {
		if h.log != nil {
			h.log.Warn("script.generate: multi-item cancelled mid-run",
				zap.String("job_id", j.ID),
				zap.Int("succeeded", manyResult.Summary.Succeeded),
				zap.Int("failed", manyResult.Summary.Failed),
				zap.Error(err))
		}
		return nil, fmt.Errorf("script.generate cancelled mid-multi-item: %w", err)
	}

	// Case (a): pure infra failure. manyResult is nil when the use
	// case could not even produce aggregate counts (defensive
	// against future refactors that surface a nil result; current
	// GenerateManyUseCase always returns a non-nil result).
	if manyResult == nil {
		if h.log != nil {
			h.log.Error("script.generate: multi-item use-case failure",
				zap.String("job_id", j.ID),
				zap.Error(err))
		}
		if err == nil {
			// Defensive: surface a synthesised error so the worker
			// never silently marks an empty result COMPLETED.
			return nil, fmt.Errorf("script generation: use case returned nil result without error")
		}
		return nil, err
	}

	envelope := buildEnvelopeResult(manyResult)
	mapped, mapErr := toMap(envelope)
	if mapErr != nil {
		return nil, fmt.Errorf("generate job handler: marshal envelope: %w", mapErr)
	}

	// Case (b): ALL items failed → wrap as a Go error so the worker
	// marks FAILED + retry.
	if envelope.Summary.Total > 0 && envelope.Summary.Failed == envelope.Summary.Total {
		var wrapped error
		if err != nil {
			wrapped = fmt.Errorf("script generation: all %d items failed: %w", envelope.Summary.Total, err)
		} else {
			wrapped = fmt.Errorf("script generation: all %d items failed", envelope.Summary.Total)
		}
		if h.log != nil {
			h.log.Error("script.generate: multi-item all-failed",
				zap.String("job_id", j.ID),
				zap.Int("total", envelope.Summary.Total),
				zap.Int("failed", envelope.Summary.Failed),
				zap.Error(wrapped))
		}
		return mapped, wrapped
	}

	// Case (c): partial or full success → (mapped, nil). err may
	// still be non-nil (e.g. context cancelled mid-run, some
	// items already completed); we deliberately don't propagate
	// it as a Go-level error because at least one item succeeded
	// and the envelope already carries the partial-failure signal
	// via summary.failed > 0 + ok=false.
	if err != nil && h.log != nil {
		h.log.Warn("script.generate: multi-item partial completion with infra error",
			zap.String("job_id", j.ID),
			zap.Int("succeeded", envelope.Summary.Succeeded),
			zap.Int("failed", envelope.Summary.Failed),
			zap.Error(err))
	}
	return mapped, nil
}

// RegisterJobs registers the handler for TypeScriptGenerate with
// the canonical ports.Broker port.
func (h *GenerateJobHandler) RegisterJobs(jobsSvc ports.Broker) error {
	if h == nil {
		return fmt.Errorf("generate job handler: not constructed")
	}
	if jobsSvc == nil {
		return nil
	}
	if err := jobsSvc.RegisterHandler(scriptpkg.TypeScriptGenerate, h.Handle); err != nil {
		return fmt.Errorf("generate job handler: register: %w", err)
	}
	if h.log != nil {
		h.log.Info("registered script.generate job handler")
	}
	return nil
}

// Typed result construction

// buildSingleSuccessEnvelope wraps a successful single-item result
// in the canonical envelope shape (Version=2, Items=[1], Summary
// counts). The previous implementation used a `Single *GenerationResult`
// field that required a special toMap flattening path; PR 7 removes
// that asymmetry.
func buildSingleSuccessEnvelope(itemID string, single *domainScript.GenerationResult) domainScript.GenerationEnvelopeResult {
	if single == nil {
		return buildSingleFailureEnvelope(itemID, "nil generation result")
	}
	return domainScript.GenerationEnvelopeResult{
		Version: domainScript.EnvelopeVersion,
		OK:      true,
		Items: []domainScript.GenerationEnvelopeItem{{
			ItemID: itemID,
			Result: single,
		}},
		Summary: domainScript.GenerationEnvelopeSummary{
			Total:     1,
			Succeeded: 1,
			Failed:    0,
		},
	}
}

// buildSingleFailureEnvelope captures a per-item failure in the
// canonical envelope shape. Same schema-version, same summary
// counts, same per-item Error field.
func buildSingleFailureEnvelope(itemID string, errMsg string) domainScript.GenerationEnvelopeResult {
	return domainScript.GenerationEnvelopeResult{
		Version: domainScript.EnvelopeVersion,
		OK:      false,
		Items: []domainScript.GenerationEnvelopeItem{{
			ItemID: itemID,
			Error:  errMsg,
		}},
		Summary: domainScript.GenerationEnvelopeSummary{
			Total:     1,
			Succeeded: 0,
			Failed:    1,
		},
	}
}

// buildEnvelopeResult converts a GenerateManyResult into a typed
// GenerationEnvelopeResult. PR 7 contract: Summary is a value
// type, Version is always EnvelopeVersion, no Single field.
func buildEnvelopeResult(r *usecase.GenerateManyResult) domainScript.GenerationEnvelopeResult {
	if r == nil {
		return domainScript.GenerationEnvelopeResult{
			Version: domainScript.EnvelopeVersion,
			OK:      false,
			Summary: domainScript.GenerationEnvelopeSummary{},
		}
	}
	items := make([]domainScript.GenerationEnvelopeItem, len(r.Items))
	for i, item := range r.Items {
		var errStr string
		var genResult *domainScript.GenerationResult
		if item.Error != "" {
			errStr = item.Error
		} else {
			genResult = item.Result
		}
		items[i] = domainScript.GenerationEnvelopeItem{
			ItemID: item.ItemID,
			Result: genResult,
			Error:  errStr,
		}
	}
	return domainScript.GenerationEnvelopeResult{
		Version: domainScript.EnvelopeVersion,
		OK:      r.Summary.Failed == 0,
		Items:   items,
		Summary: domainScript.GenerationEnvelopeSummary{
			Total:     r.Summary.Total,
			Succeeded: r.Summary.Succeeded,
			Failed:    r.Summary.Failed,
		},
		Warnings: r.Warnings,
	}
}

func singleEnvelopeResult(itemID string, result *domainScript.GenerationResult) domainScript.GenerationEnvelopeResult {
	return domainScript.GenerationEnvelopeResult{
		OK: result != nil,
		Items: []domainScript.GenerationEnvelopeItem{{
			ItemID: itemID,
			Result: result,
		}},
		Summary: domainScript.GenerationEnvelopeSummary{
			Total:     1,
			Succeeded: 1,
			Failed:    0,
		},
	}
}

// toMap serialises a GenerationEnvelopeResult to map[string]any
// via a JSON marshal/unmarshal cycle. This is the LEGAL boundary
// between typed domain results and the job-system map contract
// (the only place map[string]any appears in the application
// layer). Every envelope variant — success, single-item, multi-item,
// failure, partial — flows through this single path now that the
// Single field has been removed (PR 7).
func toMap(r domainScript.GenerationEnvelopeResult) (map[string]any, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("generate job handler: marshal envelope: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("generate job handler: unmarshal envelope: %w", err)
	}
	return out, nil
}
