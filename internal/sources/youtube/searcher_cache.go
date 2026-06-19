package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	concurrent "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
	timeutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"

	"go.uber.org/zap"
)

func (s *Service) getCachedSearch(ctx context.Context, key string) ([]assets.Asset, bool) {
	if s.clipsRepo == nil || s.clipsRepo.DB() == nil {
		return nil, false
	}
	var resultsJSON, cachedAtStr string
	err := s.clipsRepo.DB().QueryRowContext(ctx, "SELECT results_json, cached_at FROM youtube_search_cache WHERE cache_key = ?", key).Scan(&resultsJSON, &cachedAtStr)
	if err != nil {
		return nil, false
	}
	cachedAt, err := time.Parse("2006-01-02 15:04:05", cachedAtStr)
	if err != nil {
		cachedAt = timeutil.ParseRFC3339(cachedAtStr)
		if cachedAt.IsZero() {
			return nil, false
		}
	}
	// Expire after 6 hours
	if time.Since(cachedAt) > 6*time.Hour {
		return nil, false
	}

	var results []assets.Asset
	if err := json.Unmarshal([]byte(resultsJSON), &results); err != nil {
		return nil, false
	}
	return results, true
}

func (s *Service) setCachedSearch(ctx context.Context, key string, results []assets.Asset) {
	if s.clipsRepo == nil || s.clipsRepo.DB() == nil {
		return
	}
	resultsJSON, err := json.Marshal(results)
	if err != nil {
		return
	}
	_, err = s.clipsRepo.DB().ExecContext(ctx, "INSERT OR REPLACE INTO youtube_search_cache (cache_key, results_json, cached_at) VALUES (?, ?, datetime('now'))", key, string(resultsJSON))
	if err != nil {
		s.log.Warn("failed to cache youtube search results", zap.Error(err))
	}
}

func (s *Service) getCachedVideoMetadata(ctx context.Context, videoID string) (*VideoMetadata, bool) {
	if s.clipsRepo == nil || s.clipsRepo.DB() == nil {
		return nil, false
	}
	var metadataJSON, cachedAtStr string
	err := s.clipsRepo.DB().QueryRowContext(ctx, "SELECT metadata_json, cached_at FROM youtube_video_metadata_cache WHERE video_id = ?", videoID).Scan(&metadataJSON, &cachedAtStr)
	if err != nil {
		return nil, false
	}
	cachedAt, err := time.Parse("2006-01-02 15:04:05", cachedAtStr)
	if err != nil {
		cachedAt = timeutil.ParseRFC3339(cachedAtStr)
		if cachedAt.IsZero() {
			return nil, false
		}
	}
	// Expire after 7 days
	if time.Since(cachedAt) > 7*24*time.Hour {
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
		_, _ = s.clipsRepo.DB().ExecContext(bgCtx, `
			UPDATE youtube_video_metadata_cache
			SET hit_count = hit_count + 1, last_used = datetime('now')
			WHERE video_id = ?
		`, id)
	})

	return &metadata, true
}

func (s *Service) setCachedVideoMetadata(ctx context.Context, videoID string, metadata *VideoMetadata) {
	if s.clipsRepo == nil || s.clipsRepo.DB() == nil {
		return
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	query := `
		INSERT INTO youtube_video_metadata_cache (video_id, metadata_json, cached_at, last_used, hit_count) 
		VALUES (?, ?, datetime('now'), datetime('now'), 0)
		ON CONFLICT(video_id) DO UPDATE SET 
			metadata_json = excluded.metadata_json, 
			cached_at = excluded.cached_at,
			last_used = datetime('now')
	`
	_, err = s.clipsRepo.DB().ExecContext(ctx, query, videoID, string(metadataJSON))
	if err != nil {
		s.log.Warn("failed to cache youtube video metadata", zap.Error(err))
	}
}

// PrewarmHotVideoMetadataCache pre-warms the L1 in-memory cache with the top 20 hottest entries from the L2 SQLite cache.
func (s *Service) PrewarmHotVideoMetadataCache(ctx context.Context) error {
	if s.clipsRepo == nil || s.clipsRepo.DB() == nil {
		return fmt.Errorf("database not available")
	}

	s.log.Info("Starting L1 cache pre-warm for top 20 YouTube video metadata queries")

	rows, err := s.clipsRepo.DB().QueryContext(ctx, `
		SELECT video_id, metadata_json 
		FROM youtube_video_metadata_cache 
		ORDER BY hit_count DESC 
		LIMIT 20
	`)
	if err != nil {
		return fmt.Errorf("querying hot metadata cache: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var videoID, metadataJSON string
		if err := rows.Scan(&videoID, &metadataJSON); err != nil {
			s.log.Warn("Failed to scan cached metadata row during pre-warm", zap.Error(err))
			continue
		}

		var metadata VideoMetadata
		if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
			s.log.Warn("Failed to unmarshal cached metadata JSON during pre-warm", zap.String("video_id", videoID), zap.Error(err))
			continue
		}

		// Store in L1 cache
		s.metadataL1.Store(videoID, metadataL1Entry{
			Metadata: &metadata,
			AddedAt:  time.Now(),
		})
		count++
	}

	s.log.Info("Successfully pre-warmed L1 cache", zap.Int("entries_loaded", count))
	return nil
}
