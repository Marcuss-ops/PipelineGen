package processor

import (
	"context"

	entityports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// These sentinels are aliases of the kernel errors, preserving one owner for
// the domain facts while allowing adapter callers to keep their API.
var ErrEntityExtractorUnavailable = scriptpkg.ErrEntityExtractorUnavailable
var ErrMetadataGeneratorUnavailable = scriptpkg.ErrMetadataGeneratorUnavailable

// FallbackEntityExtractor preserves a source-bound extraction path when a
// primary extractor returns no values.
type FallbackEntityExtractor struct {
	Primary  entityports.EntityExtractor
	Fallback entityports.EntityExtractor
}

type batchEntityExtractor = entityports.BatchEntityExtractor

type unavailableEntityExtractionAdapter struct{}

func NewUnavailableEntityExtractionAdapter() entityports.EntityExtractor {
	return unavailableEntityExtractionAdapter{}
}

func (unavailableEntityExtractionAdapter) ExtractEntities(_ context.Context, _ scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	return nil, ErrEntityExtractorUnavailable
}

type unavailableMetadataGenerationAdapter struct{}

func NewUnavailableMetadataGenerationAdapter() adapters.MetadataGenerator {
	return unavailableMetadataGenerationAdapter{}
}

func (unavailableMetadataGenerationAdapter) GenerateMetadata(_ context.Context, _ scriptpkg.MetadataGenerationRequest) ([]scriptpkg.VideoMetadata, error) {
	return nil, ErrMetadataGeneratorUnavailable
}
