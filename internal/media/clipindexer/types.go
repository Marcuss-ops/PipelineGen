package clipindexer

import (
	"context"
	"time"
)

// VectorStoreIndexer defines the interface for upserting indexed assets into a vector store.
// This keeps the clipindexer decoupled from the actual Qdrant implementation.
type VectorStoreIndexer interface {
	// UpsertFromClip reads a single clip's updated data (search_text, embeddings) from DB
	// and pushes it to the vector index.
	UpsertFromClip(ctx context.Context, clipID string) error
	// UpsertFromClips reads multiple clips and pushes them in a single batch upsert.
	// Must be faster than N individual UpsertFromClip calls for bulk operations.
	UpsertFromClips(ctx context.Context, clipIDs []string) error
}

// IndexResult represents the result of an indexing operation
type IndexResult struct {
	ClipID            string    `json:"clip_id"`
	Success           bool      `json:"success"`
	Error             string    `json:"error,omitempty"`
	IndexedAt         time.Time `json:"indexed_at"`
	SearchText        string    `json:"search_text,omitempty"`
	Category          string    `json:"category,omitempty"`
	EmbeddingComputed bool      `json:"embedding_computed"`
}

// BatchIndexRequest represents a batch indexing request
type BatchIndexRequest struct {
	ClipIDs []string `json:"clip_ids"`
}

// BatchIndexResponse represents a batch indexing response
type BatchIndexResponse struct {
	Total      int           `json:"total"`
	Successful int           `json:"successful"`
	Failed     int           `json:"failed"`
	Results    []IndexResult `json:"results"`
}
