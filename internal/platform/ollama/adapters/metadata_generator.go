// Package adapters — metadata_generator.go bridges the kernel-owned
// script.MetadataGenerator port with the real ollama.Generator video metadata
// generation backend.
//
// PR-METADATA-GENERATOR-WIRING (July 2026): the V2 postprocessor pipeline
// had the metadata processor wired through the fail-closed
// unavailableMetadataGenerationAdapter which always returned
// ErrMetadataGeneratorUnavailable. This adapter replaces it so the
// ollama.Generator.GenerateVideoMetadataWithModel backend is invoked
// at runtime, populating the canonical VideoMetadata with Title,
// Description, Tags, and TranslationStatus.
//
// godlike/06 SSOT (one canonical owner per fact): this adapter is the
// SOLE bridge between the MetadataGenerator port and the real Ollama
// backend. No other package may define a parallel adapter.
//
// godlike/07 NO-FAKE-AVAILABILITY: the adapter calls the real Ollama
// backend — it does not return empty/synthetic results. A nil
// generator at construction time is rejected via the adapter returning
// ErrMetadataGeneratorUnavailable at runtime (mirrors the unavailable
// adapter contract so callers can probe identically).
package adapters

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
)

// OllamaMetadataGeneratorAdapter implements script.MetadataGenerator
// by delegating to the real ollama.Generator video metadata backend.
type OllamaMetadataGeneratorAdapter struct {
	gen *ollama.Generator
}

// NewOllamaMetadataGeneratorAdapter constructs the adapter. gen may be
// nil — in that case, GenerateMetadata returns
// script.ErrMetadataGeneratorUnavailable (fail-closed per
// godlike/07) so callers probe identically to the unavailable adapter.
func NewOllamaMetadataGeneratorAdapter(gen *ollama.Generator) script.MetadataGenerator {
	return &OllamaMetadataGeneratorAdapter{gen: gen}
}

// GenerateMetadata implements script.MetadataGenerator.
//
// Flow:
//  1. Call ollama.Generator.GenerateVideoMetadataWithModel with the
//     request Title and Model.
//  2. Convert (description, tags, error) → []script.VideoMetadata:
//     - Language from request
//     - Title from request
//     - Description from backend
//     - Tags from backend
//     - TranslationStatus = "translated" (backend returns English)
func (a *OllamaMetadataGeneratorAdapter) GenerateMetadata(ctx context.Context, req script.MetadataGenerationRequest) ([]script.VideoMetadata, error) {
	if a.gen == nil {
		return nil, script.ErrMetadataGeneratorUnavailable
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, nil
	}

	desc, tags, err := a.gen.GenerateVideoMetadataWithModel(ctx, title, req.Model)
	if err != nil {
		return nil, err
	}

	return []script.VideoMetadata{
		{
			Language:          req.Language,
			Title:             title,
			Description:       strings.TrimSpace(desc),
			Tags:              tags,
			TranslationStatus: "translated",
		},
	}, nil
}

// Compile-time pin: OllamaMetadataGeneratorAdapter satisfies
// script.MetadataGenerator.
var _ script.MetadataGenerator = (*OllamaMetadataGeneratorAdapter)(nil)
