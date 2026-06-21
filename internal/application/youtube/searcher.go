package youtube

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"

	"go.uber.org/zap"
)

type searchL1Entry struct {
	Results []asset.Asset
	AddedAt time.Time
}

type metadataL1Entry struct {
	Metadata *VideoMetadata
	AddedAt  time.Time
}

// SearchLive performs a live YouTube search using the SearchRunnerPort.
// sort can be "views" for most viewed videos.
func (s *Service) SearchLive(ctx context.Context, query string, limit int, sort string) ([]asset.Asset, error) {
	// Parse limit from query if present (e.g., "query -15")
	if strings.Contains(query, " -") {
		parts := strings.Split(query, " -")
		if len(parts) > 1 {
			if l, err := strconv.Atoi(parts[len(parts)-1]); err == nil && l > 0 {
				limit = l
				query = strings.Join(parts[:len(parts)-1], " -")
			}
		}
	}

	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	cacheKey := fmt.Sprintf("%s|%d|%s", query, limit, sort)

	// 1. Check L1 memory cache
	if val, ok := s.searchL1.Load(cacheKey); ok {
		if entry, ok := val.(searchL1Entry); ok {
			if time.Since(entry.AddedAt) < 6*time.Hour {
				s.log.Info("Serving YouTube search results from L1 cache", zap.String("query", query))
				return entry.Results, nil
			}
		}
	}

	// 2. Check L2 SQLite cache
	if cached, ok := s.getCachedSearch(ctx, cacheKey); ok {
		s.log.Info("Serving YouTube search results from L2 SQLite cache", zap.String("query", query))
		s.searchL1.Store(cacheKey, searchL1Entry{Results: cached, AddedAt: time.Now()})
		return cached, nil
	}

	s.log.Info("Performing live YouTube search", zap.String("query", query), zap.Int("limit", limit), zap.String("sort", sort))

	// Delegate to the port (infrastructure layer)
	if s.searchRunner == nil {
		return nil, fmt.Errorf("youtube: search runner not wired")
	}

	rawResults, err := s.searchRunner.SearchLive(ctx, query, limit, sort)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Convert port DTOs to domain assets
	results := make([]asset.Asset, 0, len(rawResults))
	for _, r := range rawResults {
		metadata := map[string]any{
			"uploader": r.Uploader,
			"duration": r.Duration,
			"video_id": r.ID,
		}
		results = append(results, asset.Asset{
			ID:           "youtube_" + r.ID,
			Name:         r.Title,
			Source:       "youtube",
			SourceURL:    r.URL,
			ThumbnailURL: r.Thumbnail,
			Metadata:     metadata,
		})
	}

	// Cache the search results
	s.setCachedSearch(ctx, cacheKey, results)
	s.searchL1.Store(cacheKey, searchL1Entry{Results: results, AddedAt: time.Now()})

	return results, nil
}
