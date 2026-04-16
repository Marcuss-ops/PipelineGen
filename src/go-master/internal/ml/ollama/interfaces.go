// Package ollama provides Ollama AI model integration.
package ollama

import (
	"context"
)

// EntityExtractor interface for entity extraction
type EntityExtractor interface {
	ExtractEntitiesFromSegment(ctx context.Context, req EntityExtractionRequest) (*EntityExtractionResult, error)
	ExtractEntitiesFromScript(ctx context.Context, segments []string, entityCount int) (*FullEntityAnalysis, error)
}

// Ensure Client satisfies EntityExtractor interface
var _ EntityExtractor = (*Client)(nil)
