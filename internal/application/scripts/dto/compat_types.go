package dto

import (
	"context"
	"encoding/json"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// EntityExtractor extracts typed entities from generated script text.
type EntityExtractor interface {
	ExtractEntities(ctx context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error)
}

// MetadataGenerator generates typed YouTube metadata for a script.
type MetadataGenerator interface {
	GenerateMetadata(ctx context.Context, req scriptpkg.MetadataGenerationRequest) ([]scriptpkg.VideoMetadata, error)
}

// PostProcessArtifact is the historical accumulator name used by tests and
// several processors. It aliases the canonical interface{}.
type PostProcessArtifact = interface{}

// SerializeEntityResultRoundTrip preserves the typed entity result as JSON for
// legacy read-only compatibility. It never mutates the source of truth.
func SerializeEntityResultRoundTrip(res *scriptpkg.EntityResult) (string, error) {
	if res == nil {
		return "", nil
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("serialize entity result: %w", err)
	}
	return string(raw), nil
}
