package vectorstore

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// Service provides high-level operations over the Store interface.
// It handles embedding generation, collection management, and
// coordinates between the embedding server and the ANN index.
type Service struct {
	store   Store
	cfg     Config
	log     *zap.Logger
	enabled bool

	// EmbeddingClient is used to generate text/visual embeddings on-the-fly.
	// Set via SetEmbeddingClient.
	embeddingURL string
}

// NewService creates a new vectorstore service backed by the given Store.
func NewService(store Store, cfg Config, log *zap.Logger) *Service {
	return &Service{
		store:   store,
		cfg:     cfg,
		log:     log,
		enabled: true,
	}
}

// SetEmbeddingURL configures the embedding server URL for on-demand embedding generation.
func (s *Service) SetEmbeddingURL(url string) {
	s.embeddingURL = url
}

// Enabled returns whether the vector store is active.
func (s *Service) Enabled() bool {
	return s.enabled
}

// SetEnabled controls whether the vector store is active (default: true).
func (s *Service) SetEnabled(enabled bool) {
	s.enabled = enabled
}

// EnsureCollection creates the collection if it doesn't exist.
func (s *Service) EnsureCollection(ctx context.Context) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	return s.store.EnsureCollection(ctx)
}

// UpsertAsset indexes an asset into the vector store.
func (s *Service) UpsertAsset(ctx context.Context, asset VectorAsset) error {
	if !s.enabled || s.store == nil {
		s.log.Debug("vectorstore disabled, skipping upsert",
			zap.String("asset_id", asset.AssetID))
		return nil
	}

	if len(asset.TextEmbedding) == 0 && len(asset.VisualEmbedding) == 0 {
		s.log.Debug("no embeddings to upsert, skipping",
			zap.String("asset_id", asset.AssetID))
		return nil
	}

	if err := s.store.UpsertAsset(ctx, asset); err != nil {
		return fmt.Errorf("upsert asset %s: %w", asset.AssetID, err)
	}

	s.log.Debug("vectorstore upserted asset",
		zap.String("asset_id", asset.AssetID),
		zap.Int("text_dim", len(asset.TextEmbedding)),
		zap.Int("visual_dim", len(asset.VisualEmbedding)))

	return nil
}

// Search performs vector search over the index.
func (s *Service) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if !s.enabled || s.store == nil {
		return nil, nil
	}
	return s.store.Search(ctx, req)
}

// DeleteAsset removes an asset from the vector store.
func (s *Service) DeleteAsset(ctx context.Context, assetID string) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	return s.store.DeleteAsset(ctx, assetID)
}

// Health checks the vector store connectivity.
func (s *Service) Health(ctx context.Context) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	return s.store.Health(ctx)
}

// Close releases resources.
func (s *Service) Close() error {
	if s.store != nil {
		return s.store.Close()
	}
	return nil
}