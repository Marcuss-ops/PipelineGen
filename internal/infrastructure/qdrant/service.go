// Package qdrant — canonical Service for Qdrant vector database operations.
// Recreated as a stub after the original implementation was removed from
// the remote (June 2026). All operations return errors; the real Qdrant
// backend is not available at this time.
package qdrant

import (
	"context"
	"fmt"
)

// Service is the canonical Qdrant vector database client. Currently a
// stub — the real implementation was removed from the remote and needs
// to be recreated.
type Service struct {
	config Config
}

// NewService creates a new Qdrant Service (currently a stub).
func NewService(cfg Config) *Service {
	return &Service{config: cfg}
}

// Enabled returns whether the service is configured.
func (s *Service) Enabled() bool {
	if s == nil {
		return false
	}
	return s.config.Enabled
}

// Health checks the Qdrant connection.
func (s *Service) Health(ctx context.Context) error {
	return fmt.Errorf("qdrant service not available — implementation removed from remote")
}

// EmbedTextForVector generates an embedding for the given text.
func (s *Service) EmbedTextForVector(ctx context.Context, text, vectorName string) ([]float32, error) {
	return nil, fmt.Errorf("qdrant service not available")
}

// IndexHealth returns the index health report.
func (s *Service) IndexHealth(ctx context.Context) (*IndexHealthReport, error) {
	return nil, fmt.Errorf("qdrant service not available")
}

// OperationCollectionInfo returns collection info for diagnostics.
func (s *Service) OperationCollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	return nil, fmt.Errorf("qdrant service not available")
}

// Search performs a dense vector search.
func (s *Service) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	return nil, fmt.Errorf("qdrant service not available")
}

// HybridSearch performs a hybrid (dense + sparse) vector search.
func (s *Service) HybridSearch(ctx context.Context, req HybridSearchRequest) ([]SearchResult, error) {
	return nil, fmt.Errorf("qdrant service not available")
}

// UpsertAsset inserts or updates a single vector asset.
func (s *Service) UpsertAsset(ctx context.Context, asset VectorAsset) error {
	return fmt.Errorf("qdrant service not available")
}

// UpsertAssets inserts or updates multiple vector assets in batch.
func (s *Service) UpsertAssets(ctx context.Context, assets []VectorAsset) error {
	return fmt.Errorf("qdrant service not available")
}

// IndexHealthReport is returned by IndexHealth.
type IndexHealthReport struct {
	OK              bool     `json:"ok"`
	Degraded        bool     `json:"degraded"`
	SQLiteAssets    int      `json:"sqlite_assets"`
	SQLiteIndexed   int      `json:"sqlite_indexed"`
	QdrantPoints    int      `json:"qdrant_points"`
	MissingInQdrant int      `json:"missing_in_qdrant"`
	OrphanInQdrant  int      `json:"orphan_in_qdrant"`
	PendingOutbox   int      `json:"pending_outbox"`
	DeadLetter      int      `json:"dead_letter"`
	DegradedSources []string `json:"degraded_sources"`
	DBTotal         int      `json:"db_total"`
	WithEmbedding   int      `json:"with_embedding"`
	DBToQdrantDelta int      `json:"db_to_qdrant_delta"`
	StaleQdrantIDs  []string `json:"stale_qdrant_ids"`
}

// CollectionInfo holds Qdrant collection info.
type CollectionInfo struct {
	PointsCount int `json:"points_count"`
}
