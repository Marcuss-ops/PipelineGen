// Package search provides YouTube search and video metadata retrieval, with
// L1 (in-memory sync.Map) and L2 cache-port backed caching.
// Extracted from the root youtube package during PR5 Phase 2 (June 2026).
//
// Design: SearchDeps accepts max 3 fields. The L1 caches live on the Service
// struct. SearchLive and GetVideoInfo consult L1 → L2 → live runner, then
// populate both caches on miss.
package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	ports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

// SearchDeps holds dependencies for the search service (max 8 fields).
// PR5 Phase 2 target: ≤8 fields — currently 3.
type SearchDeps struct {
	SearchRunner ports.SearchRunnerPort
	Cache        ports.CachePort
	Log          *zap.Logger
}

// Service performs live YouTube search and video metadata retrieval with
// two-tier caching (L1 in-memory + L2 SQLite).
type SearchService struct {
	searchRunner ports.SearchRunnerPort
	cache        ports.CachePort
	log          *zap.Logger

	searchL1   sync.Map // map[string]searchL1Entry — live search results
	metadataL1 sync.Map // map[string]metadataL1Entry — video metadata
}

// ── L1 cache entry types ──────────────────────────────────────────────────

type searchL1Entry struct {
	Results []asset.Asset
	AddedAt time.Time
}

type metadataL1Entry struct {
	Metadata *ports.DownloaderMetadata
	AddedAt  time.Time
}

// NewService is the canonical constructor.
func NewSearchService(deps SearchDeps) *SearchService {
	return &SearchService{
		searchRunner: deps.SearchRunner,
		cache:        deps.Cache,
		log:          deps.Log,
	}
}

// ── Public API ────────────────────────────────────────────────────────────

// SearchLive performs a live YouTube search using the SearchRunnerPort.
// sort can be "views" for most viewed videos.
func (s *SearchService) SearchLive(ctx context.Context, query string, limit int, sort string) ([]asset.Asset, error) {
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
		return nil, fmt.Errorf("youtube/search: search runner not wired")
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

// GetVideoInfo retrieves full metadata for a YouTube video without downloading it.
// Uses SearchRunnerPort when wired; returns an error when not.
func (s *SearchService) GetVideoInfo(ctx context.Context, videoURL string) (*ports.DownloaderMetadata, error) {
	if videoURL == "" {
		return nil, fmt.Errorf("url is required")
	}

	videoID := ""
	if id, err := extractVideoIDFromURL(videoURL); err == nil {
		videoID = id
	}

	// 1. Check L1 Cache
	if videoID != "" {
		if val, ok := s.metadataL1.Load(videoID); ok {
			if entry, ok := val.(metadataL1Entry); ok {
				if time.Since(entry.AddedAt) < 7*24*time.Hour {
					s.log.Info("Serving YouTube video metadata from L1 cache", zap.String("videoID", videoID))
					return entry.Metadata, nil
				}
			}
		}
	}

	// 2. Check L2 Cache
	if videoID != "" {
		if cached, ok := s.getCachedVideoMetadata(ctx, videoID); ok {
			s.log.Info("Serving YouTube video metadata from L2 SQLite cache", zap.String("videoID", videoID))
			s.metadataL1.Store(videoID, metadataL1Entry{Metadata: cached, AddedAt: time.Now()})
			return cached, nil
		}
	}

	s.log.Info("Retrieving YouTube video info", zap.String("url", videoURL))

	// Delegate to the port (infrastructure layer)
	if s.searchRunner == nil {
		return nil, fmt.Errorf("youtube/search: search runner not wired")
	}

	info, err := s.searchRunner.GetVideoInfo(ctx, videoURL)
	if err != nil {
		return nil, err
	}

	// Preserve original URL and derive ThumbnailURL from last thumbnail (back-compat).
	info.URL = videoURL
	if info.ThumbnailURL == "" && len(info.Thumbnails) > 0 {
		info.ThumbnailURL = info.Thumbnails[len(info.Thumbnails)-1].URL
	}

	// Cache the video metadata
	if info.ID != "" {
		s.setCachedVideoMetadata(ctx, info.ID, info)
		s.metadataL1.Store(info.ID, metadataL1Entry{Metadata: info, AddedAt: time.Now()})
	}

	return info, nil
}

// PrewarmHotVideoMetadataCache pre-warms the L1 in-memory cache with the
// top 20 hottest entries from the L2 SQLite cache.
func (s *SearchService) PrewarmHotVideoMetadataCache(ctx context.Context) error {
	if s.cache == nil {
		return fmt.Errorf("cache service not available")
	}
	rows, err := s.cache.PrewarmMeta(ctx, 20)
	if err != nil {
		return err
	}
	for _, row := range rows {
		var metadata ports.DownloaderMetadata
		if err := json.Unmarshal([]byte(row.MetadataJSON), &metadata); err != nil {
			continue
		}
		s.metadataL1.Store(row.VideoID, metadataL1Entry{Metadata: &metadata, AddedAt: time.Now()})
	}
	s.log.Info("Successfully pre-warmed L1 cache", zap.Int("entries_loaded", len(rows)))
	return nil
}

// ── Private: L2 cache helpers ─────────────────────────────────────────────

func (s *SearchService) getCachedSearch(ctx context.Context, key string) ([]asset.Asset, bool) {
	if s.cache == nil {
		return nil, false
	}
	jsonStr, ok := s.cache.GetSearch(ctx, key)
	if !ok {
		return nil, false
	}
	var results []asset.Asset
	if err := json.Unmarshal([]byte(jsonStr), &results); err != nil {
		return nil, false
	}
	return results, true
}

func (s *SearchService) setCachedSearch(ctx context.Context, key string, results []asset.Asset) {
	if s.cache == nil {
		return
	}
	resultsJSON, err := json.Marshal(results)
	if err != nil {
		return
	}
	s.cache.SetSearch(ctx, key, string(resultsJSON))
}

func (s *SearchService) getCachedVideoMetadata(ctx context.Context, videoID string) (*ports.DownloaderMetadata, bool) {
	if s.cache == nil {
		return nil, false
	}
	jsonStr, ok := s.cache.GetVideoMeta(ctx, videoID)
	if !ok {
		return nil, false
	}
	var metadata ports.DownloaderMetadata
	if err := json.Unmarshal([]byte(jsonStr), &metadata); err != nil {
		return nil, false
	}
	concurrent.SafeGoFunc("youtube-search-metadata-hit-update", videoID, func(id string) {
		// AGENTS.md §7 post-write save ctx — YouTube search metadata-hit
		// bump is a post-write operation detached from the search ctx;
		// the cache write must complete even if the search request was
		// cancelled by the operator.
		s.cache.BumpMetaHits(context.WithoutCancel(ctx), id)
	})
	return &metadata, true
}

func (s *SearchService) setCachedVideoMetadata(ctx context.Context, videoID string, metadata *ports.DownloaderMetadata) {
	if s.cache == nil {
		return
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	s.cache.SetVideoMeta(ctx, videoID, string(metadataJSON))
}

// ── Private: URL helpers ──────────────────────────────────────────────────

// extractVideoIDFromURL extracts the YouTube video ID from a URL.
func extractVideoIDFromURL(url string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("empty URL")
	}
	for _, prefix := range []string{"https://www.youtube.com/watch?v=", "http://www.youtube.com/watch?v=", "https://youtube.com/watch?v="} {
		if len(url) > len(prefix) && url[:len(prefix)] == prefix {
			id := url[len(prefix):]
			if idx := indexOf(id, '&'); idx >= 0 {
				id = id[:idx]
			}
			if id != "" {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("could not extract video ID from URL")
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
