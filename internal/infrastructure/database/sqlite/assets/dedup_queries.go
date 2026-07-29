// Package assets — dedup helpers (Wave A: moved from
// internal/kernel/asset/dedup.go).
//
// These queries are used by the new clip registration endpoints
// (register-from-youtube, upload-video) to avoid creating duplicate
// MediaAsset records for the same source content. All queries
// exclude soft-deleted clips via lifecycle_state column.
//
// Wave C / Phase 3 fix: dropped the unused `internal/domain/asset`
// import — the SQL receivers migrated to Local infra and do not
// reference any domain symbol directly.
package assets

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// FindByYouTubeVideoID returns the ID of the most recent non-deleted
// clip registered from the given YouTube video ID. The match is
// exact on metadata_json.youtube_video_id.
//
// Returns ("", nil) if no matching clip exists — this is a normal
// case for first-time registration, not an error.
//
// Pass hasSegment=true to further restrict the match to clips
// extracted from a specific segment (startSec, endSec). When
// hasSegment is false, the function returns the most recent clip
// for the video regardless of any stored start/end. Segment
// boundaries are compared as integer milliseconds to avoid
// float-stringification bugs (e.g. "0" vs "0.0").
func (s *AssetStoreSQLite) FindByYouTubeVideoID(ctx context.Context, videoID string, hasSegment bool, startSec, endSec float64) (string, error) {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return "", nil
	}
	var (
		query string
		args  []any
	)
	if hasSegment {
		startMS := int64(startSec * 1000)
		endMS := int64(endSec * 1000)
		query = `SELECT id FROM media_assets
			WHERE json_extract(COALESCE(metadata_json,'{}'), '$.youtube_video_id') = ?
			AND ` + SoftDeleteFilter() + `
			AND CAST(ROUND(COALESCE(CAST(json_extract(metadata_json, '$.start') AS REAL), 0) * 1000) AS INTEGER) = ?
			AND CAST(ROUND(COALESCE(CAST(json_extract(metadata_json, '$.end')   AS REAL), 0) * 1000) AS INTEGER) = ?
			ORDER BY created_at DESC LIMIT 1`
		args = []any{videoID, startMS, endMS}
	} else {
		query = `SELECT id FROM media_assets
			WHERE json_extract(COALESCE(metadata_json,'{}'), '$.youtube_video_id') = ?
			AND ` + SoftDeleteFilter() + `
			ORDER BY created_at DESC LIMIT 1`
		args = []any{videoID}
	}

	var id string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("FindByYouTubeVideoID: %w", err)
	}
	return id, nil
}

// FindByFileHash returns the ID of the most recent non-deleted clip
// with the given MD5 file_hash. Used by upload-video to skip
// re-registration of an identical file.
func (s *AssetStoreSQLite) FindByFileHash(ctx context.Context, fileHash string) (string, error) {
	fileHash = strings.TrimSpace(fileHash)
	if fileHash == "" {
		return "", nil
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM media_assets
		WHERE file_hash = ?
		AND `+SoftDeleteFilter()+`
		ORDER BY created_at DESC LIMIT 1`, fileHash).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("FindByFileHash: %w", err)
	}
	return id, nil
}

// FindBySourceURL returns the ID of the most recent non-deleted
// clip registered with the given external URL (metadata_json
// .source_url or .youtube_url).
func (s *AssetStoreSQLite) FindBySourceURL(ctx context.Context, url string) (string, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return "", nil
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM media_assets
		WHERE (json_extract(COALESCE(metadata_json,'{}'), '$.source_url') = ?
			OR json_extract(COALESCE(metadata_json,'{}'), '$.youtube_url') = ?)
		AND `+SoftDeleteFilter()+`
		ORDER BY created_at DESC LIMIT 1`, url, url).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("FindBySourceURL: %w", err)
	}
	return id, nil
}

// FindByName returns the ID of the most recent non-deleted clip
// with the given name. Used for name collision warnings during
// registration.
func (s *AssetStoreSQLite) FindByName(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM media_assets
		WHERE name = ?
		AND `+SoftDeleteFilter()+`
		ORDER BY created_at DESC LIMIT 1`, name).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("FindByName: %w", err)
	}
	return id, nil
}

// FindDuplicatesByYouTubeID returns clip IDs (most recent first)
// that share the same youtube_video_id as the given one. Used by
// the post-hoc dedup sweeper to find candidates for merging/cleanup.
//
// Excludes the given excludeID from results.
func (s *AssetStoreSQLite) FindDuplicatesByYouTubeID(ctx context.Context, videoID string, excludeID string) ([]string, error) {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM media_assets
		WHERE json_extract(COALESCE(metadata_json,'{}'), '$.youtube_video_id') = ?
		AND `+SoftDeleteFilter()+`
		AND id != ?
		ORDER BY created_at DESC`,
		videoID, excludeID)
	if err != nil {
		return nil, fmt.Errorf("FindDuplicatesByYouTubeID: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			s.log.Warn("FindDuplicatesByYouTubeID scan failed", zap.Error(err))
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
