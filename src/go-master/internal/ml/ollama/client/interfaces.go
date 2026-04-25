// Package ollama provides Ollama AI model integration.
package client

import (
	"velox/go-master/internal/ml/ollama/types"
	"context"
)

// EntityExtractor interface for entity extraction
type EntityExtractor interface {
	ExtractEntitiesFromSegment(ctx context.Context, req types.EntityExtractionRequest) (*types.EntityExtractionResult, error)
	ExtractEntitiesFromScript(ctx context.Context, segments []string, entityCount int) (*types.FullEntityAnalysis, error)
}

// Ensure Client satisfies EntityExtractor interface
var _ EntityExtractor = (*Client)(nil)
