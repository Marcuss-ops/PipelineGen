// Package cache provides YouTube-specific L2 SQLite cache operations extracted
// from the root youtube package during PR5 (June 2026).
//
// Design: this package owns ONLY the L2 (SQLite) persistence layer.
// It accepts a *sql.DB directly; callers handle JSON serialization.
package cache

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

// Deps holds the cache service dependencies (max 2 fields).
type Deps struct {
	DB  *sql.DB
	Log *zap.Logger
}

// Service owns L2 SQLite cache operations for YouTube search, metadata,
// segments, and category classification.
type Service struct {
	db  *sql.DB
	log *zap.Logger
}

// NewService is the canonical constructor.
func NewService(deps Deps) *Service {
	return &Service{db: deps.DB, log: deps.Log}
}

// ── Search results cache ───────────────────────────────────────────────────

// GetSearch retrieves cached search results as raw JSON. Returns (json, true) on hit.
func (s *Service) GetSearch(ctx context.Context, key string) (string, bool) {
	if s.db == nil {
		return "", false
	}
	var jsonStr, cachedAtStr string
	err := s.db.QueryRowContext(ctx,
		"SELECT results_json, cached_at FROM youtube_search_cache WHERE cache_key = ?", key,
	).Scan(&jsonStr, &cachedAtStr)
	if err != nil {
		return "", false
	}
	cachedAt, err := time.Parse("2006-01-02 15:04:05", cachedAtStr)
	if err != nil {
		return "", false
	}
	if time.Since(cachedAt) > 6*time.Hour {
		return "", false
	}
	return jsonStr, true
}

// SetSearch persists search results as raw JSON.
func (s *Service) SetSearch(ctx context.Context, key, resultsJSON string) {
	if s.db == nil {
		return
	}
	_, err := s.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO youtube_search_cache (cache_key, results_json, cached_at) VALUES (?, ?, datetime('now'))",
		key, resultsJSON)
	if err != nil {
		s.log.Warn("failed to cache youtube search results", zap.Error(err))
	}
}

// ── Video metadata cache ───────────────────────────────────────────────────

// GetVideoMeta retrieves cached video metadata as raw JSON. Returns (json, true) on hit.
func (s *Service) GetVideoMeta(ctx context.Context, videoID string) (string, bool) {
	if s.db == nil {
		return "", false
	}
	var jsonStr, cachedAtStr string
	err := s.db.QueryRowContext(ctx,
		"SELECT metadata_json, cached_at FROM youtube_video_metadata_cache WHERE video_id = ?", videoID,
	).Scan(&jsonStr, &cachedAtStr)
	if err != nil {
		return "", false
	}
	cachedAt, err := time.Parse("2006-01-02 15:04:05", cachedAtStr)
	if err != nil {
		return "", false
	}
	if time.Since(cachedAt) > 7*24*time.Hour {
		return "", false
	}
	return jsonStr, true
}

// SetVideoMeta persists video metadata as raw JSON.
func (s *Service) SetVideoMeta(ctx context.Context, videoID, metadataJSON string) {
	if s.db == nil {
		return
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO youtube_video_metadata_cache (video_id, metadata_json, cached_at, last_used, hit_count)
		VALUES (?, ?, datetime('now'), datetime('now'), 0)
		ON CONFLICT(video_id) DO UPDATE SET
			metadata_json = excluded.metadata_json,
			cached_at = excluded.cached_at,
			last_used = datetime('now')
	`, videoID, metadataJSON)
	if err != nil {
		s.log.Warn("failed to cache youtube video metadata", zap.Error(err))
	}
}

// BumpMetaHits increments the hit counter for a cached metadata entry (fire-and-forget).
func (s *Service) BumpMetaHits(ctx context.Context, videoID string) {
	if s.db == nil {
		return
	}
	_, _ = s.db.ExecContext(ctx,
		`UPDATE youtube_video_metadata_cache SET hit_count = hit_count + 1, last_used = datetime('now') WHERE video_id = ?`,
		videoID)
}

// PrewarmMeta returns the top N (video_id, metadata_json) rows from the hot cache.
func (s *Service) PrewarmMeta(ctx context.Context, limit int) ([]youtubeports.VideoMetaRow, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT video_id, metadata_json FROM youtube_video_metadata_cache ORDER BY hit_count DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query hot metadata cache: %w", err)
	}
	defer rows.Close()
	var out []youtubeports.VideoMetaRow
	for rows.Next() {
		var r youtubeports.VideoMetaRow
		if err := rows.Scan(&r.VideoID, &r.MetadataJSON); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// ── Segments cache ─────────────────────────────────────────────────────────

// GetSegments retrieves cached segments as raw JSON.
func (s *Service) GetSegments(ctx context.Context, videoID string) (string, bool) {
	if s.db == nil {
		return "", false
	}
	var jsonStr string
	err := s.db.QueryRowContext(ctx,
		"SELECT segments_json FROM youtube_segments_cache WHERE video_id = ?", videoID,
	).Scan(&jsonStr)
	if err != nil {
		return "", false
	}
	return jsonStr, true
}

// SetSegments persists segments as raw JSON.
func (s *Service) SetSegments(ctx context.Context, videoID, segmentsJSON string) {
	if s.db == nil {
		return
	}
	_, _ = s.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO youtube_segments_cache (video_id, segments_json, cached_at) VALUES (?, ?, datetime('now'))",
		videoID, segmentsJSON)
}

// ── Category cache ─────────────────────────────────────────────────────────

// GetCategory retrieves the cached Ollama-classified category for a video title.
func (s *Service) GetCategory(ctx context.Context, videoTitle string) (string, bool) {
	if s.db == nil {
		return "", false
	}
	var category string
	err := s.db.QueryRowContext(ctx,
		"SELECT category FROM youtube_category_cache WHERE video_title = ?", videoTitle,
	).Scan(&category)
	if err != nil {
		return "", false
	}
	return category, true
}

// SetCategory persists a category classification.
func (s *Service) SetCategory(ctx context.Context, videoTitle, category string) {
	if s.db == nil {
		return
	}
	_, _ = s.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO youtube_category_cache (video_title, category, cached_at) VALUES (?, ?, datetime('now'))",
		videoTitle, category)
}
