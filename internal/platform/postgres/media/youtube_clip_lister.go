// Package media — youtube_clip_lister.go: the narrow PostgreSQL read surface
// for "which media assets are YouTube clips carrying a non-empty title".
//
// MEDIA-SSOT P2-9 Phase 2: this is the PostgreSQL replacement for the
// `SELECT id FROM media_assets WHERE source = 'youtube' AND
// json_extract(metadata_json, '$.youtube_title') != ”` read that used to run on
// the operational SQLite store inside
// internal/capabilities/youtube/adapters/youtube_adapters_store.go
// (ClipStoreAdapter.ListYouTubeClipIDsForSearchText). PostgreSQL is the media
// SSOT, so the SQLite listing could only ever answer "empty" once the canonical
// writer held the rows — a search_text rebuild built on it silently found
// nothing. The predicate and the result set are deliberately identical to the
// retired statement so the migration is a pure engine swap:
//
//	SELECT id FROM media_assets
//	WHERE source = 'youtube'
//	  AND (NULLIF(metadata_json, '')::jsonb)->>'youtube_title' <> ''
//	ORDER BY id
//
// The `json_extract` accessor becomes the canonical PostgreSQL TEXT→jsonb idiom
// (NULLIF + ::jsonb) already used by dedup_group_reader.go. A missing OR empty
// title is excluded by both forms (SQL NULL and ” respectively), which is the
// behaviour the caller relied on.
//
// This reader answers over PostgreSQL ONLY. It has no SQLite branch and never
// falls back: PostgreSQL + pgvector is the single media read authority, so a
// closed media plane must surface as an error to the caller rather than as a
// silent read of a second engine.
//
// Removal condition: this surface exists only while the search_text rebuild job
// needs the YouTube-clip id listing. When that job moves to a wider media read
// model, delete this file — do not keep it as a compatibility shim.
package media

import (
	"context"
	"database/sql"
	"fmt"
)

// MediaYouTubeClipLister lists the asset ids of YouTube media rows that carry a
// non-empty metadata youtube_title.
type MediaYouTubeClipLister struct {
	db *sql.DB
}

// NewMediaYouTubeClipLister returns a lister bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path, so this panics in the same way NewMediaDriveFileLister does.
func NewMediaYouTubeClipLister(db *sql.DB) *MediaYouTubeClipLister {
	if db == nil {
		panic("media.NewMediaYouTubeClipLister: db is required")
	}
	return &MediaYouTubeClipLister{db: db}
}

// DB exposes the underlying handle so the composition root can assert it lives
// on the media SSOT.
func (l *MediaYouTubeClipLister) DB() *sql.DB {
	if l == nil {
		return nil
	}
	return l.db
}

// ListYouTubeClipIDsForSearchText returns the YouTube clip ids with a non-empty
// title, ordered deterministically. limit <= 0 means "no limit"; offset > 0
// skips that many rows. Both mirror the retired SQLite statement's shape.
func (l *MediaYouTubeClipLister) ListYouTubeClipIDsForSearchText(ctx context.Context, limit, offset int) ([]string, error) {
	if l == nil || l.db == nil {
		return nil, fmt.Errorf("media youtube clip lister: media SSOT handle is not configured")
	}
	query := `
		SELECT id FROM media_assets
		WHERE source = 'youtube'
		  AND COALESCE((NULLIF(metadata_json, '')::jsonb)->>'youtube_title', '') <> ''
		ORDER BY id
	`
	args := []any{}
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if offset > 0 {
		args = append(args, offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args))
	}
	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("media youtube clip lister: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("media youtube clip lister: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media youtube clip lister: iterate: %w", err)
	}
	return ids, nil
}

// ── absorbed from dedup_group_reader.go ──

// YouTubeIDDuplicateGroup is one YouTube CLIP identity shared by more than one
// live media asset.
//
// The identity is the window, never the video: a YouTube video legitimately
// produces dozens of clips that all carry the same youtube_video_id, so
// grouping by the video alone retires every clip but the newest on each sweep
// (observed 2026-11-26: 15 clips soft-deleted right after a successful run, and
// the same wipe had already destroyed the Ilary batch at 13:07 — OVL-06).
// The canonical clip identity is (video, start_ms, end_ms); policy_version is
// excluded on purpose so a re-extraction under a new policy is still one clip.
type YouTubeIDDuplicateGroup struct {
	YouTubeVideoID string
	StartMs        int64
	EndMs          int64
	Count          int
}

// MediaDuplicateGroupReader answers the clip-dedup sweeper's media_assets reads
// from the PostgreSQL media SSOT.
type MediaDuplicateGroupReader struct {
	db *sql.DB
}

// NewMediaDuplicateGroupReader returns a reader bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path.
func NewMediaDuplicateGroupReader(db *sql.DB) *MediaDuplicateGroupReader {
	if db == nil {
		panic("media.NewMediaDuplicateGroupReader: db is required")
	}
	return &MediaDuplicateGroupReader{db: db}
}

// DB exposes the underlying handle so callers can assert the reader lives on
// the media SSOT.
func (r *MediaDuplicateGroupReader) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// youtubeIDExpr is the canonical projection of the youtube_video_id metadata
// key. It is repeated rather than parameterised because PostgreSQL allows a
// parameter exactly once per execution; the three occurrences are the same
// expression by construction, and the live-PG pin
// (TestMediaDuplicateGroupReader_MatchesLegacySQLiteSemantics) fails if they
// ever drift apart.
const youtubeIDExpr = `(NULLIF(metadata_json, '')::jsonb)->>'youtube_video_id'`

// DuplicateYouTubeIDGroups returns up to limit youtube_video_id groups holding
// more than one live media asset. A limit <= 0 means no limit.
//
// An empty result is a normal case (no duplicates), not an error.
func (r *MediaDuplicateGroupReader) DuplicateYouTubeIDGroups(ctx context.Context, limit int) ([]YouTubeIDDuplicateGroup, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("media duplicate group reader: media SSOT handle is not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query := `
		SELECT ` + youtubeIDExpr + ` AS vid, COALESCE(start_ms, 0), COALESCE(end_ms, 0), COUNT(*) AS n
		FROM media_assets
		WHERE lifecycle_state <> 'DELETED'
		  AND ` + youtubeIDExpr + ` IS NOT NULL
		  AND ` + youtubeIDExpr + ` <> ''
		GROUP BY 1, 2, 3
		HAVING COUNT(*) > 1
		ORDER BY 1, 2, 3`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT $1`
		args = append(args, limit)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("media duplicate group reader: duplicate group query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var groups []YouTubeIDDuplicateGroup
	for rows.Next() {
		var g YouTubeIDDuplicateGroup
		if err := rows.Scan(&g.YouTubeVideoID, &g.StartMs, &g.EndMs, &g.Count); err != nil {
			return nil, fmt.Errorf("media duplicate group reader: duplicate group scan: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media duplicate group reader: duplicate group rows: %w", err)
	}
	return groups, nil
}

// DuplicateAssetIDsByYouTubeID returns the ids of the live media assets that
// share the same clip identity (videoID + source window), newest first,
// excluding excludeID.
//
// The window is part of the key: two clips of the same video are distinct
// assets, not duplicates (OVL-06). Only a re-extraction of the SAME window is a
// duplicate.
//
// Ordering is load-bearing, not cosmetic: the sweeper keeps the FIRST id and
// retires the rest, so the ordering is what makes "keep the newest copy" true.
func (r *MediaDuplicateGroupReader) DuplicateAssetIDsByYouTubeID(ctx context.Context, videoID string, startMs, endMs int64, excludeID string) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("media duplicate group reader: media SSOT handle is not configured")
	}
	if videoID == "" {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM media_assets
		WHERE `+youtubeIDExpr+` = $1
		  AND COALESCE(start_ms, 0) = $2
		  AND COALESCE(end_ms, 0) = $3
		  AND lifecycle_state <> 'DELETED'
		  AND id <> $4
		ORDER BY `+TimestampExpr("created_at")+` DESC, id DESC
	`, videoID, startMs, endMs, excludeID)
	if err != nil {
		return nil, fmt.Errorf("media duplicate group reader: duplicate id query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("media duplicate group reader: duplicate id scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media duplicate group reader: duplicate id rows: %w", err)
	}
	return ids, nil
}
