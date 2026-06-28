// Package scripts — processor_metadata.go (PR 3, June 2026).
//
// Rewritten to drop the legacy PostGenFunc callback + GenerationSpec
// bridge. The processor now consumes the typed MetadataGenerator port
// from ports_entity_metadata.go and loops the canonical request shape
// over `plan.Languages` so multi-language plans still produce multiple
// records.
//
// Policy is ProcessorRequired per the PR 3 spec — composition must
// wire a backend generator and the runtime preflight rejects plans
// that request "metadata" without one.
//
// Audit #6 (Phase 4): Policy() returns scripts.ProcessorPolicy
// (canonical). See adapters/postprocessor_types.go for the cross-
// package type normalisation rationale.
package adapters

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// MetadataProcessor generates YouTube-style metadata (Title /
// Description / Tags) for the generated script via the typed
// MetadataGenerator port. Enabled as "metadata" in the plan's
// Postprocessors list.
//
// PR 3 (June 2026): promoted to ProcessorRequired (was BestEffort
// in PR 2). Composition root fails closed without a wired backend;
// the runtime preflight rejects plans that request "metadata"
// without a registered adapter.
type MetadataProcessor struct {
	generator MetadataGenerator
}

// NewMetadataProcessor creates a MetadataProcessor. generator must be
// non-nil at composition time (composition-side validation enforces
// this via validateRequiredProcessors).
func NewMetadataProcessor(generator MetadataGenerator) *MetadataProcessor {
	return &MetadataProcessor{generator: generator}
}

// Ready reports whether the metadata processor is fully configured
// at composition time. PR ref fix/required-postprocessor-semantics
// (audit #3): a nil generator means the canonical metadata
// generator was never wired — failing closed at boot prevents the
// runtime preflight from silently degrading metadata generation to
// "not configured" warnings.
//
// Returns true when generator is non-nil; false otherwise.
// Wraps the nil-receiver defensive check (panics on nil receiver
// would defeat the purpose of the readiness probe).
func (p *MetadataProcessor) Ready() bool {
	return p != nil && p.generator != nil
}

func (p *MetadataProcessor) Name() string { return "metadata" }

// Policy classifies metadata as ProcessorRequired. Static for now;
// future PR can read plan.OutputSpec.GenerateMetadata (or similar
// payload) and conditionally resolve.
func (p *MetadataProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) scripts.ProcessorPolicy {
	return scripts.ProcessorRequired
}

// Process executes metadata generation via the typed port. The
// processor builds a `scriptpkg.MetadataGenerationRequest` per
// plan.Languages entry (falling back to `[]string{plan.Language}`
// when the plan carries no Languages list) and merges the resulting
// []VideoMetadata into a single PostProcessResult.Metadata slice.
//
// Returns a typed error wrapping scriptpkg.ErrPostprocessFailed on
// backend failure. Returns an empty PostProcessResult when the
// input Text is empty AND the plan Title is empty (no work to do).
//
// Returns the populated PostProcessResult on success.
func (p *MetadataProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.generator == nil {
		return nil, fmt.Errorf("%w: metadata processor: MetadataGenerator not configured", scriptpkg.ErrPostprocessFailed)
	}
	if strings.TrimSpace(input.Text) == "" && strings.TrimSpace(plan.Title) == "" {
		return &PostProcessResult{}, nil
	}
	if plan == nil {
		return nil, fmt.Errorf("%w: metadata processor: nil ResolvedGenerationPlan", scriptpkg.ErrPostprocessFailed)
	}

	langs := plan.Languages
	if len(langs) == 0 {
		langs = []string{plan.Language}
	}

	out := make([]scriptpkg.VideoMetadata, 0, len(langs))
	for _, lang := range langs {
		req := scriptpkg.MetadataGenerationRequest{
			Text:      input.Text,
			Title:     plan.Title,
			Language:  lang,
			Model:     plan.Model,
			SpecScene: input.SpecScene,
		}
		records, err := p.generator.GenerateMetadata(ctx, req)
		if err != nil {
			// If the first-language call fails on a multi-language
			// plan, surface the typed error (Required posture).
			// Partial success for subsequent languages is appended
			// before returning so the caller observes everything
			// the backend produced before the failure.
			if len(out) > 0 {
				return &PostProcessResult{Metadata: out}, err
			}
			return nil, err
		}
		out = append(out, records...)
	}

	return &PostProcessResult{Metadata: out}, nil
}
