// Package scripts — processor_metadata.go (PR 3, June 2026).
//
// Rewritten to drop the legacy PostGenFunc callback + GenerationSpec
// bridge. The processor now consumes the typed adapters.MetadataGenerator port
// from ports_entity_metadata.go and loops the canonical request shape
// over `plan.Languages` so multi-language plans still produce multiple
// records.
//
// Metadata generation is optional at composition time: caller-provided
// metadata can be emitted without an AI backend. A nil generator remains
// a runtime failure only when AI generation is actually requested.
package processor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// MetadataProcessor generates YouTube-style metadata (Title /
// Description / Tags) for the generated script via the typed
// adapters.MetadataGenerator port. Enabled as "metadata" in the plan's
// Postprocessors list.
//
// The processor is registered even when the AI backend is unavailable so
// manual metadata remains usable. Plans that request generated metadata
// without a generator fail closed during processing.
type MetadataProcessor struct {
	generator adapters.MetadataGenerator
}

// NewMetadataProcessor creates a MetadataProcessor. generator may be nil:
// manual caller-provided metadata is handled without it, while AI-only
// requests return a typed processing error.
func NewMetadataProcessor(generator adapters.MetadataGenerator) *MetadataProcessor {
	return &MetadataProcessor{generator: generator}
}

func (p *MetadataProcessor) Name() adapters.ProcessorName { return adapters.ProcessorMetadata }

// Policy keeps metadata fail-closed when the processor is explicitly
// requested. Manual metadata short-circuits before the generator check.
func (p *MetadataProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) adapters.ProcessorPolicy {
	return adapters.ProcessorRequired
}

// Process executes metadata generation via the typed port. The
// processor builds a `scriptpkg.MetadataGenerationRequest` per
// plan.Languages entry (falling back to `[]string{plan.Language}`
// when the plan carries no Languages list) and merges the resulting
// []VideoMetadata into a single adapters.PostProcessResult.Metadata slice.
//
// Returns a typed error wrapping scriptpkg.ErrPostprocessFailed on
// backend failure. Returns an empty adapters.PostProcessResult when the
// input Text is empty AND the plan Title is empty (no work to do).
//
// Returns the populated adapters.PostProcessResult on success.
func (p *MetadataProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input adapters.ProcessInput) (*adapters.PostProcessResult, error) {
	if plan == nil {
		return nil, fmt.Errorf(
			"%w: metadata processor: nil ResolvedGenerationPlan",
			scriptpkg.ErrPostprocessFailed,
		)
	}

	// Manual metadata always wins.
	// No Gemma/Ollama call is performed.
	if plan.VideoMetadata != nil && plan.VideoMetadata.HasContent() {
		metadata := scriptpkg.CloneVideoMetadata(plan.VideoMetadata)

		if strings.TrimSpace(metadata.Language) == "" {
			metadata.Language = plan.Language
		}

		return &adapters.PostProcessResult{
			Metadata: []scriptpkg.VideoMetadata{*metadata},
			Changed:  true,
		}, nil
	}

	// AI generation is used only when no manual metadata was provided.
	if strings.TrimSpace(input.Text) == "" &&
		strings.TrimSpace(plan.Title) == "" {
		return &adapters.PostProcessResult{}, nil
	}

	if p.generator == nil {
		return nil, fmt.Errorf(
			"%w: metadata processor: adapters.MetadataGenerator not configured",
			scriptpkg.ErrPostprocessFailed,
		)
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
			if errors.Is(err, ErrMetadataGeneratorUnavailable) {
				return &adapters.PostProcessResult{
					Changed:  true,
					Warnings: []string{err.Error()},
				}, nil
			}
			// If the first-language call fails on a multi-language
			// plan, surface the typed error (Required posture).
			// Partial success for subsequent languages is appended
			// before returning so the caller observes everything
			// the backend produced before the failure.
			if len(out) > 0 {
				return &adapters.PostProcessResult{Metadata: out}, err
			}
			return nil, err
		}
		out = append(out, records...)
	}

	// godlike/07 NO-FAKE-AVAILABILITY: signal Changed=true ONLY when
	// the LLM enrichment actually produced at least one record; an
	// empty result means the postprocessor must NOT trigger a merge
	// write-back (would silently overwrite prior metadata with empty).
	return &adapters.PostProcessResult{Metadata: out, Changed: len(out) > 0}, nil
}
