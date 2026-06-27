// Package scripts — compat_adapters.go (PR 3 — May 2026).
//
// Defines the canonical typed ports EntityExtractor + MetadataGenerator
// that postprocessors consume at composition time, plus the noop adapters
// used when a backend is unavailable. Production wiring injects real
// adapters; composition-time guards fail closed if a Required
// postprocessor is requested without a registered adapter.
//
// PR 3 (June 2026): replaces the legacy PostGenFunc callback +
// GenerationSpec bridge with typed ports. Previously entities and
// metadata processors consumed an opaque interface{}; the PR 3
// typed ports enable compile-time audits.
package adapters

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Typed ports (PR 3 — May 2026) ────────────────────────────────────────

// EntityExtractor is the canonical port for entity extraction.
// Processors (EntitiesProcessor) consume an EntityExtractor at
// composition time and dispatch EntityExtractionRequest → EntityResult.
type EntityExtractor interface {
	ExtractEntities(ctx context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error)
}

// MetadataGenerator is the canonical port for video metadata
// generation. Processors (MetadataProcessor) consume a
// MetadataGenerator at composition time and dispatch
// MetadataGenerationRequest → []VideoMetadata.
type MetadataGenerator interface {
	GenerateMetadata(ctx context.Context, req scriptpkg.MetadataGenerationRequest) ([]scriptpkg.VideoMetadata, error)
}

// ── Noop adapters (composition-time placeholder when no real backend) ───

type noopEntityExtractionAdapter struct{}

func NewEntityExtractionAdapter(_ any) EntityExtractor {
	return noopEntityExtractionAdapter{}
}

func (noopEntityExtractionAdapter) ExtractEntities(ctx context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	_ = ctx
	_ = req
	return &scriptpkg.EntityResult{}, nil
}

type noopMetadataGenerationAdapter struct{}

func NewMetadataGenerationAdapter(_ any, _ string) MetadataGenerator {
	return noopMetadataGenerationAdapter{}
}

func (noopMetadataGenerationAdapter) GenerateMetadata(ctx context.Context, req scriptpkg.MetadataGenerationRequest) ([]scriptpkg.VideoMetadata, error) {
	_ = ctx
	_ = req
	return nil, nil
}
