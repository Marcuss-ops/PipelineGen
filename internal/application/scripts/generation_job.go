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
package scripts

import (
	"context"
	"encoding/json"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"go.uber.org/zap"
)

// GenerateJobHandler is the application-layer job-system handler for
// `script.generate` jobs. Registered via the jobs broker in
// wire_script.go:
//
//	root.Jobs.Service.RegisterHandler(job.TypeScriptGenerate,
//	    genJobHandler.Handle)
type GenerateJobHandler struct {
	one  *GenerateOneUseCase
	many *GenerateManyUseCase
	cfg  NormalizationConfig
	log  *zap.Logger
}

// NewGenerateJobHandler wires the handler to the unified use cases.
func NewGenerateJobHandler(
	one *GenerateOneUseCase,
	many *GenerateManyUseCase,
	cfg NormalizationConfig,
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
func (h *GenerateJobHandler) Handle(
	ctx context.Context,
	j *scriptpkg.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if h == nil {
		return nil, fmt.Errorf("generate job handler: not constructed")
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

	var result domainScript.GenerationEnvelopeResult

	if len(env.Items) == 1 {
		// Single-item path (PR 7, June 2026).
		// PR 7 contract: a single-item run still emits the canonical
		// envelope shape (Items + Summary + Version=2). The previous
		// code routed single-item runs through the `Single` field
		// exclusively, requiring callers to special-case the JSON
		// shape. The unified envelope removes that branch.
		tracker := NewProgressTracker(progressFn, env.Items[0].ID)
		single, err := h.one.Execute(ctx, env.Items[0], env.Preset, tracker)
		if err != nil {
			if h.log != nil {
				h.log.Error("script.generate: single-item failed",
					zap.String("job_id", j.ID),
					zap.Error(err))
			}
			// PR 7: emit a single-item envelope with the failure
			// captured per-item so callers see a consistent shape.
			result = domainScript.GenerationEnvelopeResult{
				Version: domainScript.EnvelopeVersion,
				OK:      false,
				Items: []domainScript.GenerationEnvelopeItem{{
					ItemID: env.Items[0].ID,
					Error:  err.Error(),
				}},
				Summary: domainScript.GenerationEnvelopeSummary{
					Total:     1,
					Succeeded: 0,
					Failed:    1,
				},
			}
			return toMap(result)
		}
		result = singleEnvelopeResult(env.Items[0].ID, single)
	} else {
		// Multi-item path.
		manyResult, err := h.many.Execute(ctx, env, h.cfg, progressFn)
		if err != nil {
			if h.log != nil {
				h.log.Error("script.generate: multi-item failed",
					zap.String("job_id", j.ID),
					zap.Error(err))
			}
			// Return partial results even on error (some items may
			// have succeeded before the failure).
			if manyResult != nil {
				r := buildEnvelopeResult(manyResult)
				return toMap(r)
			}
			return nil, err
		}
		result = buildEnvelopeResult(manyResult)
	}

	return toMap(result)
}

// RegisterJobs registers the handler for TypeScriptGenerate with
// the canonical Broker port.
func (h *GenerateJobHandler) RegisterJobs(jobsSvc Broker) error {
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

// ── Typed result construction ──────────────────────────────────────

// buildEnvelopeResult converts a GenerateManyResult into a typed
// GenerationEnvelopeResult. This replaces the old mapManyResult
// function — the typed struct owns its shape; the map boundary is
// handled by toMap.
func buildEnvelopeResult(r *GenerateManyResult) domainScript.GenerationEnvelopeResult {
	if r == nil {
		return domainScript.GenerationEnvelopeResult{OK: false}
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
		OK:    r.Summary.Failed == 0,
		Items: items,
		Summary: &domainScript.GenerationEnvelopeSummary{
			Total:     r.Summary.Total,
			Succeeded: r.Summary.Succeeded,
			Failed:    r.Summary.Failed,
		},
		Warnings: r.Warnings,
	}
}

// toMap serialises a GenerationEnvelopeResult to map[string]any
// via a JSON marshal/unmarshal cycle. This is the boundary between
// typed domain results and the job-system map contract.
//
// Single-item path: the GenerationResult is marshalled flat and
// "ok" + "warnings" are injected — no nested "single" key.
// Multi-item path: the full envelope (items + summary) is marshalled.
func toMap(r domainScript.GenerationEnvelopeResult) (map[string]any, error) {
	var out map[string]any

	if r.Single != nil {
		// Single-item: flatten GenerationResult and inject envelope fields.
		b, err := json.Marshal(r.Single)
		if err != nil {
			return nil, fmt.Errorf("generate job handler: marshal single: %w", err)
		}
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, fmt.Errorf("generate job handler: unmarshal single: %w", err)
		}
		out["ok"] = r.OK
		if len(r.Warnings) > 0 {
			out["warnings"] = r.Warnings
		}
		return out, nil
	}

	// Multi-item: marshal whole envelope.
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("generate job handler: marshal result: %w", err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("generate job handler: unmarshal result: %w", err)
	}
	return out, nil
}
