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
