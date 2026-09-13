package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// FindClipIDByName returns the id of the most recent non-deleted asset with the
// exact name, or "" when none exists. PostgreSQL counterpart of the retired
// SQLite ClipsRepository.FindByName dedupe lookup.
func (s *MediaSearcher) FindClipIDByName(ctx context.Context, name string) (string, error) {
	needle := strings.TrimSpace(name)
	if needle == "" {
		return "", nil
	}
	return s.queryClipID(ctx, `
		SELECT id FROM media_assets
		WHERE name = $1 AND lifecycle_state <> 'DELETED'
		ORDER BY created_at DESC LIMIT 1
	`, needle)
}

// FindClipIDByYouTubeVideoID returns the id of the most recent non-deleted
// asset registered from the given YouTube video id. When hasSegment is true the
// match is further restricted to the exact [startSec, endSec) segment via the
// canonical start_ms/end_ms columns (integer-millisecond comparison, matching
// the legacy json_extract + ROUND semantics).
func (s *MediaSearcher) FindClipIDByYouTubeVideoID(ctx context.Context, videoID string, hasSegment bool, startSec, endSec float64) (string, error) {
	vid := strings.TrimSpace(videoID)
	if vid == "" {
		return "", nil
	}
	if hasSegment {
		return s.queryClipID(ctx, `
			SELECT id FROM media_assets
			WHERE (youtube_video_id = $1 OR source_video_id = $1)
			  AND lifecycle_state <> 'DELETED'
			  AND start_ms = $2 AND end_ms = $3
			ORDER BY created_at DESC LIMIT 1
		`, vid, int64(startSec*1000), int64(endSec*1000))
	}
	return s.queryClipID(ctx, `
		SELECT id FROM media_assets
		WHERE (youtube_video_id = $1 OR source_video_id = $1)
		  AND lifecycle_state <> 'DELETED'
		ORDER BY created_at DESC LIMIT 1
	`, vid)
}

// FindClipIDBySourceURL returns the id of the most recent non-deleted asset
// registered with the given external URL (source_url or youtube_url).
func (s *MediaSearcher) FindClipIDBySourceURL(ctx context.Context, url string) (string, error) {
	needle := strings.TrimSpace(url)
	if needle == "" {
		return "", nil
	}
	return s.queryClipID(ctx, `
		SELECT id FROM media_assets
		WHERE (source_url = $1 OR youtube_url = $1)
		  AND lifecycle_state <> 'DELETED'
		ORDER BY created_at DESC LIMIT 1
	`, needle)
}

func (s *MediaSearcher) queryClipID(ctx context.Context, query string, args ...any) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("postgres media: media read repository not wired")
	}
	var id string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("postgres media: dedupe lookup: %w", err)
	}
	return id, nil
}
