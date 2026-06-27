package scripts

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

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
