// Package vector provides the canonical interface for vector storage operations.
//
// The Qdrant implementation lives in internal/media/vectorstore/.
// This package defines the interface contract that infrastructure adapters
// must satisfy. New code should depend on this interface, not the concrete
// Qdrant client.
package vector

import "context"

// Store is the canonical contract for vector storage operations.
type Store interface {
	EnsureCollection(ctx context.Context) error
	UpsertAsset(ctx context.Context, assetID string, vectors map[string][]float32, payload map[string]any) error
	Search(ctx context.Context, query []float32, vectorName string, limit int, minScore float64) ([]SearchResult, error)
	DeleteAsset(ctx context.Context, assetID string) error
	Health(ctx context.Context) error
	Close() error
}

// SearchResult is a single hit from a vector search.
type SearchResult struct {
	AssetID string
	Score   float64
	Payload map[string]any
}
