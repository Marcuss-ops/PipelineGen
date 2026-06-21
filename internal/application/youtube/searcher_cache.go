package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"

	"go.uber.org/zap"
)

func (s *Service) getCachedSearch(ctx context.Context, key string) ([]asset.Asset, bool) {
	if s.cacheStore == nil {
		return nil, false
	}
	resultsJSON, err := s.cacheStore.GetSearchCache(ctx, key)
	if err != nil {
		return nil, false
	}
	var results []asset.Asset
	if err := json.Unmarshal([]byte(resultsJSON), &results); err != nil {
		return nil, false
	}
	return results, true
}

func (s *Service) setCachedSearch(ctx context.Context, key string, results []asset.Asset) {
	if s.cacheStore == nil {
		return
	}
	resultsJSON, err := json.Marshal(results)
	if err != nil {
		return
	}
	if err := s.cacheStore.UpsertSearchCache(ctx, key, string(resultsJSON)); err != nil {
		s.log.Warn("failed to cache youtube search results", zap.Error(err))
	}
}

func (s *Service) getCachedVideoMetadata(ctx context.Context, videoID string) (*VideoMetadata, bool) {
	if s.cacheStore == nil {
		return nil, false
	}
	metadataJSON, err := s.cacheStore.GetMetadataCache(ctx, videoID)
	if err != nil {
		return nil, false
	}

	var metadata VideoMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return nil, false
	}
	// Update metrics asynchronously to not block search response
	concurrent.SafeGoFunc("youtube-metadata-hit-update", videoID, func(id string) {
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = s.cacheStore.IncrementMetadataHits(bgCtx, id)
	})

	return &metadata, true
}

func (s *Service) setCachedVideoMetadata(ctx context.Context, videoID string, metadata *VideoMetadata) {
	if s.cacheStore == nil {
		return
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	if err := s.cacheStore.UpsertMetadataCache(ctx, videoID, string(metadataJSON)); err != nil {
		s.log.Warn("failed to cache youtube video metadata", zap.Error(err))
	}
}

// PrewarmHotVideoMetadataCache pre-warms the L1 in-memory cache with the top 20 hottest entries from the L2 SQLite cache.
func (s *Service) PrewarmHotVideoMetadataCache(ctx context.Context) error {
	if s.cacheStore == nil {
		return fmt.Errorf("cache store not available")
	}

	s.log.Info("Starting L1 cache pre-warm for top 20 YouTube video metadata queries")

	entries, err := s.cacheStore.ListHotMetadata(ctx, 20)
	if err != nil {
		return fmt.Errorf("querying hot metadata cache: %w", err)
	}

	count := 0
	for _, entry := range entries {
		var metadata VideoMetadata
		if err := json.Unmarshal([]byte(entry.MetadataJSON), &metadata); err != nil {
			s.log.Warn("Failed to unmarshal cached metadata JSON during pre-warm", zap.String("video_id", entry.VideoID), zap.Error(err))
			continue
		}

		// Store in L1 cache
		s.metadataL1.Store(entry.VideoID, metadataL1Entry{
			Metadata: &metadata,
			AddedAt:  time.Now(),
		})
		count++
	}

	s.log.Info("Successfully pre-warmed L1 cache", zap.Int("entries_loaded", count))
	return nil
}

// cachedSearchExpired returns true if the cached-at timestamp is older than the given duration.
func cachedSearchExpired(cachedAtStr string, maxAge time.Duration) bool {
	cachedAt, err := time.Parse("2006-01-02 15:04:05", cachedAtStr)
	if err != nil {
		cachedAt = timeutil.ParseRFC3339(cachedAtStr)
		if cachedAt.IsZero() {
			return true
		}
	}
	return time.Since(cachedAt) > maxAge
}
