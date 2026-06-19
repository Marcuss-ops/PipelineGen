package vectorstore

import (
	"context"
	"fmt"
	"time"

	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

func (s *Service) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if !s.enabled || s.store == nil {
		return nil, nil
	}

	start := time.Now()
	vectorName := req.VectorName
	if vectorName == "" {
		vectorName = "text"
	}

	results, err := s.retryQdrantCallValue(ctx, "search", func() ([]SearchResult, error) {
		return s.store.Search(ctx, req)
	})

	elapsed := time.Since(start).Seconds()
	if err != nil {
		metrics.QdrantSearchDuration.WithLabelValues(vectorName, "error").Observe(elapsed)
		metrics.QdrantSearchTotal.WithLabelValues(vectorName, "error").Inc()
		metrics.QdrantErrorsTotal.WithLabelValues("search").Inc()
	} else {
		metrics.QdrantSearchDuration.WithLabelValues(vectorName, "ok").Observe(elapsed)
		metrics.QdrantSearchTotal.WithLabelValues(vectorName, "ok").Inc()
		if len(results) == 0 {
			metrics.SearchNoResultsTotal.WithLabelValues(vectorName).Inc()
		}
	}

	return results, err
}

func (s *Service) HybridSearch(ctx context.Context, req HybridSearchRequest) ([]SearchResult, error) {
	if !s.enabled || s.store == nil {
		return nil, nil
	}

	if req.SparseVector == nil && req.QueryText != "" && s.cfg.SparseVectorName != "" {
		req.SparseVector = TokenizeBM25(req.QueryText, 25000)
	}

	start := time.Now()
	results, err := s.retryQdrantCallValue(ctx, "hybrid_search", func() ([]SearchResult, error) {
		return s.store.HybridSearch(ctx, req)
	})
	elapsed := time.Since(start).Seconds()

	vectorName := "hybrid"
	if err != nil {
		metrics.QdrantSearchDuration.WithLabelValues(vectorName, "error").Observe(elapsed)
		metrics.QdrantSearchTotal.WithLabelValues(vectorName, "error").Inc()
		metrics.QdrantErrorsTotal.WithLabelValues("hybrid_search").Inc()
	} else {
		metrics.QdrantSearchDuration.WithLabelValues(vectorName, "ok").Observe(elapsed)
		metrics.QdrantSearchTotal.WithLabelValues(vectorName, "ok").Inc()
		if len(results) == 0 {
			metrics.SearchNoResultsTotal.WithLabelValues(vectorName).Inc()
		}
	}

	return results, err
}

func (s *Service) DeleteAsset(ctx context.Context, assetID string) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	return s.store.DeleteAsset(ctx, assetID)
}

func (s *Service) CollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	if !s.enabled || s.store == nil {
		return nil, nil
	}
	return s.store.PhysicalCollectionInfo(ctx)
}

func (s *Service) OperationCollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	if !s.enabled || s.store == nil {
		return nil, nil
	}
	return s.store.OperationCollectionInfo(ctx)
}

func (s *Service) PhysicalCollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	if !s.enabled || s.store == nil {
		return nil, nil
	}
	return s.store.PhysicalCollectionInfo(ctx)
}

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

func (s *Service) CleanupStalePoints(ctx context.Context, validator func(assetID, driveFileID, driveLink string) (bool, error)) (int, error) {
	if !s.enabled || s.store == nil {
		return 0, nil
	}
	return s.store.CleanupStalePoints(ctx, validator)
}

func (s *Service) IndexHealth(ctx context.Context) (*IndexHealthReport, error) {
	if s.store == nil {
		return nil, fmt.Errorf("vector store not configured")
	}
	return s.store.IndexHealth(ctx)
}

func (s *Service) ListPointIDs(ctx context.Context, limit int) ([]string, error) {
	if !s.enabled || s.store == nil {
		return nil, nil
	}
	return s.store.ListPointIDs(ctx, limit)
}

// ScrollAssetIDsPage wraps Store.ScrollAssetIDsPage. No-op if disabled.
func (s *Service) ScrollAssetIDsPage(ctx context.Context, batchSize int, fn func([]string) error) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	return s.store.ScrollAssetIDsPage(ctx, batchSize, fn)
}

// DeletePoints wraps Store.DeletePoints. No-op if disabled.
func (s *Service) DeletePoints(ctx context.Context, assetIDs []string) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	return s.store.DeletePoints(ctx, assetIDs)
}
