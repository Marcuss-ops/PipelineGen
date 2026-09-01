package adapters

import (
	"context"

	entityports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities/ports"
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

func NewFallbackEntityExtractor(primary, fallback entityports.EntityExtractor) entityports.EntityExtractor {
	if primary == nil {
		return fallback
	}
	if fallback == nil {
		return primary
	}
	return &FallbackEntityExtractor{Primary: primary, Fallback: fallback}
}

func (e *FallbackEntityExtractor) ExtractEntities(ctx context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	if e == nil {
		return nil, ErrEntityExtractorUnavailable
	}
	result, primaryErr := e.Primary.ExtractEntities(ctx, req)
	if primaryErr == nil && entityResultHasValues(result) {
		return result, nil
	}
	fallbackResult, fallbackErr := e.Fallback.ExtractEntities(ctx, req)
	if fallbackErr == nil && fallbackResult != nil {
		return fallbackResult, nil
	}
	if primaryErr != nil {
		return nil, primaryErr
	}
	return result, fallbackErr
}

func entityResultHasValues(result *scriptpkg.EntityResult) bool {
	return result != nil && len(result.Persons)+len(result.Places)+len(result.Concepts) > 0
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

func NewUnavailableMetadataGenerationAdapter() MetadataGenerator {
	return unavailableMetadataGenerationAdapter{}
}

func (unavailableMetadataGenerationAdapter) GenerateMetadata(_ context.Context, _ scriptpkg.MetadataGenerationRequest) ([]scriptpkg.VideoMetadata, error) {
	return nil, ErrMetadataGeneratorUnavailable
}
