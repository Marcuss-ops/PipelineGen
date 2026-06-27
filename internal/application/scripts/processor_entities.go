// Package scripts — processor_entities.go (PR 3, June 2026).
//
// Rewritten to drop the legacy PostGenFunc callback + GenerationSpec
// bridge. The processor now consumes the typed EntityExtractor port
// from ports_entity_metadata.go, building a typed
// `scriptpkg.EntityExtractionRequest` from `ProcessInput.Text` (the
// canonical V1 `output.text`) plus the ResolvedGenerationPlan identity
// fields.
//
// Policy is ProcessorRequired per the PR 3 spec — composition must
// wire a backend extractor and the runtime preflight rejects plans
// that request "entities" without one.
package scripts

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// EntitiesProcessor extracts named entities (Persons / Places /
// Concepts) from the generated script via the typed EntityExtractor
// port. Enabled as "entities" in the plan's Postprocessors list.
//
// PR 3 (June 2026): promoted to ProcessorRequired (was BestEffort
// in PR 2). Composition root fails closed without a wired backend;
// the runtime preflight rejects plans that request "entities"
// without a registered adapter.
type EntitiesProcessor struct {
	extractor EntityExtractor
}

// NewEntitiesProcessor creates an EntitiesProcessor. extractor must
// be non-nil at composition time (composition-side validation
// enforces this via validateRequiredProcessors).
func NewEntitiesProcessor(extractor EntityExtractor) *EntitiesProcessor {
	return &EntitiesProcessor{extractor: extractor}
}

func (p *EntitiesProcessor) Name() string { return "entities" }

// Policy classifies entities as ProcessorRequired. The plan arg is
// accepted for interface uniformity but ignored for now — a future
// PR can read plan.OutputSpec.ExtractEntities (or similar payload)
// and conditionally resolve. Until then, the static Required
// classification is the canonical source.
func (p *EntitiesProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorRequired
}

// Process executes entity extraction via the typed port. The
// processor does NOT depend on GenerationSpec or share state
// with the metadata path; the EntityExtractor port encapsulates
// the backend (production adapter wraps EntityScriptExtractor;
// tests inject a fake extractor returning a hand-crafted
// EntityResult).
//
// Returns (*PostProcessResult{Entities: result}, nil) on success.
// Returns an empty PostProcessResult (no error) when the input Text
// is empty — defensive short-circuit so the processor does not
// waste a backend call.
//
// Returns a typed error wrapping scriptpkg.ErrPostprocessFailed on
// backend failure.
func (p *EntitiesProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.extractor == nil {
		return nil, fmt.Errorf("%w: entities processor: EntityExtractor not configured", scriptpkg.ErrPostprocessFailed)
	}
	if strings.TrimSpace(input.Text) == "" {
		return &PostProcessResult{}, nil
	}
	if plan == nil {
		return nil, fmt.Errorf("%w: entities processor: nil ResolvedGenerationPlan", scriptpkg.ErrPostprocessFailed)
	}
	req := scriptpkg.EntityExtractionRequest{
		Text:      input.Text,
		Title:     plan.Title,
		Language:  plan.Language,
		Model:     plan.Model,
		SpecScene: input.SpecScene,
	}
	res, err := p.extractor.ExtractEntities(ctx, req)
	if err != nil {
		return nil, err
	}
	if res == nil {
		// Port contract: nil result, nil error is treated as
		// "no entities" — produce an empty result with a
		// warning so the caller sees the observation.
		return &PostProcessResult{
			Warnings: []string{"entities: backend returned no result"},
		}, nil
	}
	return &PostProcessResult{
		Entities: res,
	}, nil
}
