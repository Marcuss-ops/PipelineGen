package vectorstore

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/pkg/metrics"
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
	// Initialize health gauge to 0 (unhealthy until first successful Health() call)
	metrics.QdrantHealthStatus.Set(0)

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
// If SparseBM25 is nil and SearchText is non-empty, auto-generates BM25 sparse vector.
func (s *Service) UpsertAsset(ctx context.Context, asset VectorAsset) error {
	if !s.enabled || s.store == nil {
		s.log.Debug("vectorstore disabled, skipping upsert",
			zap.String("asset_id", asset.AssetID))
		return nil
	}

	// Auto-generate BM25 sparse vector from SearchText if not already provided
	if asset.SparseBM25 == nil && asset.SearchText != "" && s.cfg.SparseVectorName != "" {
		asset.SparseBM25 = TokenizeBM25(asset.SearchText, 25000)
	}

	if len(asset.TextEmbedding) == 0 && len(asset.VisualEmbedding) == 0 && len(asset.AudioEmbedding) == 0 && asset.SparseBM25 == nil {
		s.log.Debug("no embeddings to upsert, skipping",
			zap.String("asset_id", asset.AssetID))
		return nil
	}

	if err := s.store.UpsertAsset(ctx, asset); err != nil {
		metrics.QdrantUpsertTotal.WithLabelValues("error").Inc()
		metrics.QdrantErrorsTotal.WithLabelValues("upsert").Inc()
		return fmt.Errorf("upsert asset %s: %w", asset.AssetID, err)
	}

	metrics.QdrantUpsertTotal.WithLabelValues("ok").Inc()

	s.log.Debug("vectorstore upserted asset",
		zap.String("asset_id", asset.AssetID),
		zap.Int("text_dim", len(asset.TextEmbedding)),
		zap.Int("visual_dim", len(asset.VisualEmbedding)),
		zap.Int("audio_dim", len(asset.AudioEmbedding)),
		zap.Bool("has_sparse", asset.SparseBM25 != nil))

	return nil
}

// UpsertAssets indexes multiple assets in a single batch operation.
// Automatically generates BM25 sparse vectors for each asset that has SearchText.
// Use for backfill, bulk import, or migration of large asset batches.
func (s *Service) UpsertAssets(ctx context.Context, assets []VectorAsset) error {
	if !s.enabled || s.store == nil || len(assets) == 0 {
		return nil
	}

	// Auto-generate BM25 sparse vectors for assets that have SearchText
	for i := range assets {
		if assets[i].SparseBM25 == nil && assets[i].SearchText != "" && s.cfg.SparseVectorName != "" {
			assets[i].SparseBM25 = TokenizeBM25(assets[i].SearchText, 25000)
		}
	}

	// Filter out assets with no embeddings (consistent with UpsertAsset behavior)
	valid := make([]VectorAsset, 0, len(assets))
	for _, a := range assets {
		if len(a.TextEmbedding) == 0 && len(a.VisualEmbedding) == 0 &&
			len(a.AudioEmbedding) == 0 && a.SparseBM25 == nil {
			s.log.Debug("skipping asset in batch upsert (no embeddings)",
				zap.String("asset_id", a.AssetID))
			continue
		}
		valid = append(valid, a)
	}
	if len(valid) == 0 {
		return nil
	}

	// Auto-chunk large batches to avoid oversized HTTP requests to Qdrant.
	// Default batchSize=500; set to 0 or negative to disable chunking.
	batchSize := s.cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 500
	}

	start := time.Now()
	totalUpserted := 0
	var chunkErrors []error
	totalChunks := (len(valid) + batchSize - 1) / batchSize

	for i := 0; i < len(valid); i += batchSize {
		end := i + batchSize
		if end > len(valid) {
			end = len(valid)
		}
		chunk := valid[i:end]

		chunkStart := time.Now()
		err := s.store.UpsertAssets(ctx, chunk)
		chunkElapsed := time.Since(chunkStart).Seconds()

		if err != nil {
			metrics.QdrantUpsertTotal.WithLabelValues("error").Add(float64(len(chunk)))
			metrics.QdrantErrorsTotal.WithLabelValues("batch_upsert").Inc()
			chunkErrors = append(chunkErrors, fmt.Errorf("chunk [%d:%d] (%d assets): %w", i, end, len(chunk), err))
			s.log.Error("batch upsert chunk failed",
				zap.Int("offset", i),
				zap.Int("chunk_size", len(chunk)),
				zap.Float64("elapsed_sec", chunkElapsed),
				zap.Error(err))
			continue
		}

		metrics.QdrantUpsertTotal.WithLabelValues("ok").Add(float64(len(chunk)))
		totalUpserted += len(chunk)

		s.log.Debug("vectorstore batch chunk upserted",
			zap.Int("offset", i),
			zap.Int("chunk_size", len(chunk)),
			zap.Float64("elapsed_sec", chunkElapsed))
	}

	elapsed := time.Since(start).Seconds()

	if totalUpserted > 0 {
		s.log.Info("vectorstore batch upsert complete",
			zap.Int("total", totalUpserted),
			zap.Int("original", len(assets)),
			zap.Float64("elapsed_sec", elapsed))
	}
	if totalUpserted == 0 && len(chunkErrors) > 0 {
		s.log.Warn("all batch chunks failed",
			zap.Int("total_requested", len(valid)),
			zap.Int("failed_chunks", len(chunkErrors)))
	}

	if len(chunkErrors) > 0 {
		return fmt.Errorf("%d/%d chunks failed: first error: %w", len(chunkErrors), totalChunks, chunkErrors[0])
	}

	return nil
}

// Search performs vector search over the index.
func (s *Service) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if !s.enabled || s.store == nil {
		return nil, nil
	}

	start := time.Now()
	vectorName := req.VectorName
	if vectorName == "" {
		vectorName = "text"
	}

	results, err := s.store.Search(ctx, req)

	elapsed := time.Since(start).Seconds()
	if err != nil {
		metrics.QdrantSearchDuration.WithLabelValues(vectorName, "error").Observe(elapsed)
		metrics.QdrantSearchTotal.WithLabelValues(vectorName, "error").Inc()
		metrics.QdrantErrorsTotal.WithLabelValues("search").Inc()
	} else {
		metrics.QdrantSearchDuration.WithLabelValues(vectorName, "ok").Observe(elapsed)
		metrics.QdrantSearchTotal.WithLabelValues(vectorName, "ok").Inc()
	}

	return results, err
}

// DeleteAsset removes an asset from the vector store.
func (s *Service) DeleteAsset(ctx context.Context, assetID string) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	return s.store.DeleteAsset(ctx, assetID)
}

// Health checks the vector store connectivity and updates health gauge.
func (s *Service) Health(ctx context.Context) error {
	if !s.enabled || s.store == nil {
		metrics.QdrantHealthStatus.Set(0)
		return nil
	}
	err := s.store.Health(ctx)
	if err != nil {
		metrics.QdrantHealthStatus.Set(0)
	} else {
		metrics.QdrantHealthStatus.Set(1)
	}
	return err
}

// RefreshCollectionMetrics queries Qdrant for collection info and updates Prometheus gauges.
// IMPORTANT: Nothing calls this automatically. The caller must invoke it periodically
// (e.g., every 60s via a system health check loop or background goroutine).
// See internal/app/init_core.go or internal/api/handlers/system/doctor.go for integration points.
func (s *Service) RefreshCollectionMetrics(ctx context.Context) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	info, err := s.store.CollectionInfo(ctx)
	if err != nil {
		metrics.QdrantErrorsTotal.WithLabelValues("collection_info").Inc()
		return fmt.Errorf("refresh collection metrics: %w", err)
	}
	metrics.QdrantCollectionSize.WithLabelValues(s.cfg.Collection).Set(float64(info.PointsCount))
	s.log.Debug("updated collection size metric",
		zap.String("collection", s.cfg.Collection),
		zap.Int64("points", info.PointsCount))
	return nil
}

// Close releases resources.
func (s *Service) Close() error {
	if s.store != nil {
		return s.store.Close()
	}
	return nil
}

// HybridSearch performs hybrid dense+sparse search with Qdrant prefetch + RRF fusion.
// The query text is tokenized into a BM25 sparse vector and combined with the dense embedding.
func (s *Service) HybridSearch(ctx context.Context, req HybridSearchRequest) ([]SearchResult, error) {
	if !s.enabled || s.store == nil {
		return nil, nil
	}

	// Auto-tokenize query text to BM25 sparse vector if not provided
	if req.SparseVector == nil && req.QueryText != "" && s.cfg.SparseVectorName != "" {
		req.SparseVector = TokenizeBM25(req.QueryText, 25000)
	}

	start := time.Now()
	results, err := s.store.HybridSearch(ctx, req)
	elapsed := time.Since(start).Seconds()

	vectorName := "hybrid"
	if err != nil {
		metrics.QdrantSearchDuration.WithLabelValues(vectorName, "error").Observe(elapsed)
		metrics.QdrantSearchTotal.WithLabelValues(vectorName, "error").Inc()
		metrics.QdrantErrorsTotal.WithLabelValues("hybrid_search").Inc()
	} else {
		metrics.QdrantSearchDuration.WithLabelValues(vectorName, "ok").Observe(elapsed)
		metrics.QdrantSearchTotal.WithLabelValues(vectorName, "ok").Inc()
	}

	return results, err
}