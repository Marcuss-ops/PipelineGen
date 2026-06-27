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

	if len(env.Items) == 1 {
		// Single-item path (PR 7): even single-item runs emit the
		// canonical envelope shape (Version=2, Items=[1], Summary
		// counts). No special `Single` flattening.
		tracker := usecase.NewProgressTracker(progressFn, env.Items[0].ID)
		single, err := h.one.Execute(ctx, env.Items[0], env.Preset, tracker)
		if err != nil {
			if h.log != nil {
				h.log.Error("script.generate: single-item failed",
					zap.String("job_id", j.ID),
					zap.Error(err))
			}
			return toMap(buildSingleFailureEnvelope(env.Items[0].ID, err.Error()))
		}
		return toMap(buildSingleSuccessEnvelope(env.Items[0].ID, single))
	}

	// Multi-item path.
	manyResult, err := h.many.Execute(ctx, env, h.cfg, progressFn)
	if err != nil {
		if h.log != nil {
			h.log.Error("script.generate: multi-item failed",
				zap.String("job_id", j.ID),
				zap.Error(err))
		}
		// Return partial results even on error (some items may
		// have succeeded before the aggregate failure).
		if manyResult != nil {
			return toMap(buildEnvelopeResult(manyResult))
		}
		return nil, err
	}
	return toMap(buildEnvelopeResult(manyResult))
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
