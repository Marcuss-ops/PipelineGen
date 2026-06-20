package vectorstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"go.uber.org/zap"
)

func (s *Service) EnsureCollection(ctx context.Context) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	return s.store.EnsureCollection(ctx)
}

func (s *Service) UpsertAsset(ctx context.Context, asset VectorAsset) error {
	if !s.enabled || s.store == nil {
		return nil
	}

	if errs := ValidateBeforeIndex(asset); len(errs) > 0 {
		var fields []string
		for _, e := range errs {
			fields = append(fields, e.Field+": "+e.Message)
		}
		s.log.Warn("asset failed validation, skipping upsert", zap.String("asset_id", asset.AssetID), zap.Strings("errors", fields))
		metrics.QdrantUpsertTotal.WithLabelValues("validation_error").Inc()
		return fmt.Errorf("validation failed for %s: %s", asset.AssetID, strings.Join(fields, "; "))
	}

	if asset.SparseBM25 == nil && asset.SearchText != "" && s.cfg.SparseVectorName != "" {
		asset.SparseBM25 = TokenizeBM25(asset.SearchText, 25000)
	}

	if len(asset.TextEmbedding) == 0 && len(asset.TranscriptEmbedding) == 0 &&
		len(asset.VisualEmbedding) == 0 && len(asset.AudioEmbedding) == 0 && asset.SparseBM25 == nil {
		return nil
	}

	if err := s.validateEmbeddingDims(asset); err != nil {
		metrics.QdrantUpsertTotal.WithLabelValues("validation_error").Inc()
		return fmt.Errorf("validate embeddings for %s: %w", asset.AssetID, err)
	}

	if err := s.retryQdrantCall(ctx, "upsert_asset", func() error { return s.store.UpsertAsset(ctx, asset) }); err != nil {
		metrics.QdrantUpsertTotal.WithLabelValues("error").Inc()
		metrics.QdrantErrorsTotal.WithLabelValues("upsert").Inc()
		return fmt.Errorf("upsert asset %s: %w", asset.AssetID, err)
	}

	metrics.QdrantUpsertTotal.WithLabelValues("ok").Inc()
	return nil
}

func (s *Service) UpsertAssets(ctx context.Context, assets []VectorAsset) error {
	if !s.enabled || s.store == nil || len(assets) == 0 {
		return nil
	}

	for i := range assets {
		if assets[i].SparseBM25 == nil && assets[i].SearchText != "" && s.cfg.SparseVectorName != "" {
			assets[i].SparseBM25 = TokenizeBM25(assets[i].SearchText, 25000)
		}
	}

	valid := make([]VectorAsset, 0, len(assets))
	for _, a := range assets {
		if len(a.TextEmbedding) == 0 && len(a.VisualEmbedding) == 0 &&
			len(a.AudioEmbedding) == 0 && a.SparseBM25 == nil {
			continue
		}
		valid = append(valid, a)
	}
	if len(valid) == 0 {
		return nil
	}

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
		err := s.retryQdrantCall(ctx, "batch_upsert", func() error { return s.store.UpsertAssets(ctx, chunk) })
		chunkElapsed := time.Since(chunkStart).Seconds()

		if err != nil {
			metrics.QdrantUpsertTotal.WithLabelValues("error").Add(float64(len(chunk)))
			metrics.QdrantErrorsTotal.WithLabelValues("batch_upsert").Inc()
			chunkErrors = append(chunkErrors, fmt.Errorf("chunk [%d:%d] (%d assets): %w", i, end, len(chunk), err))
			s.log.Error("batch upsert chunk failed", zap.Int("offset", i), zap.Int("chunk_size", len(chunk)), zap.Float64("elapsed_sec", chunkElapsed), zap.Error(err))
			continue
		}

		metrics.QdrantUpsertTotal.WithLabelValues("ok").Add(float64(len(chunk)))
		totalUpserted += len(chunk)
	}

	elapsed := time.Since(start).Seconds()
	if totalUpserted > 0 {
		s.log.Info("vectorstore batch upsert complete", zap.Int("total", totalUpserted), zap.Float64("elapsed_sec", elapsed))
	}
	if totalUpserted == 0 && len(chunkErrors) > 0 {
		s.log.Warn("all batch chunks failed", zap.Int("total_requested", len(valid)), zap.Int("failed_chunks", len(chunkErrors)))
	}
	if len(chunkErrors) > 0 {
		return fmt.Errorf("%d/%d chunks failed: first error: %w", len(chunkErrors), totalChunks, chunkErrors[0])
	}
	return nil
}

func (s *Service) validateEmbeddingDims(asset VectorAsset) error {
	if len(asset.TextEmbedding) > 0 && s.cfg.TextDimensions > 0 && len(asset.TextEmbedding) != s.cfg.TextDimensions {
		return fmt.Errorf("text embedding dim %d != expected %d", len(asset.TextEmbedding), s.cfg.TextDimensions)
	}
	if len(asset.VisualEmbedding) > 0 && s.cfg.VisualDimensions > 0 && len(asset.VisualEmbedding) != s.cfg.VisualDimensions {
		return fmt.Errorf("visual embedding dim %d != expected %d", len(asset.VisualEmbedding), s.cfg.VisualDimensions)
	}
	if len(asset.AudioEmbedding) > 0 && s.cfg.AudioDimensions > 0 && len(asset.AudioEmbedding) != s.cfg.AudioDimensions {
		return fmt.Errorf("audio embedding dim %d != expected %d", len(asset.AudioEmbedding), s.cfg.AudioDimensions)
	}
	if len(asset.TranscriptEmbedding) > 0 && s.cfg.TranscriptDimensions > 0 && len(asset.TranscriptEmbedding) != s.cfg.TranscriptDimensions {
		return fmt.Errorf("transcript embedding dim %d != expected %d", len(asset.TranscriptEmbedding), s.cfg.TranscriptDimensions)
	}
	return nil
}
