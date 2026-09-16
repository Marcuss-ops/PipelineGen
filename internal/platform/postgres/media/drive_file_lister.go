// Package media — drive_file_lister.go: the narrow PostgreSQL read surface for
// "which media assets still carry a Drive file id".
//
// MEDIA-SSOT P2-9 Phase 2: this is the PostgreSQL replacement for the
// `SELECT id FROM media_assets WHERE drive_file_id ...` read that used to run on
// the operational SQLite store inside
// internal/capabilities/assets/ingest/adapter_clip.go. The statement and the
// result set are deliberately IDENTICAL to the SQLite one (same predicate, same
// column) so the migration is a pure engine swap with no semantic drift:
//
//	SELECT id FROM media_assets
//	WHERE drive_file_id IS NOT NULL AND drive_file_id <> ''
//	  AND lifecycle_state <> 'DELETED'
//
// The only behavioural difference is a deterministic `ORDER BY id`, which the
// legacy statement left unspecified; callers already treated the result as an
// unordered set, so this removes non-determinism without changing meaning.
//
// This reader answers over PostgreSQL ONLY. It has no SQLite branch and never
// falls back: PostgreSQL + pgvector is the single media read authority, so a
// closed media plane must surface as an error to the caller rather than as a
// silent read of a second engine.
//
// Removal condition: this surface exists only for as long as a consumer needs
// the drive-file-id listing. When that consumer moves to a wider media read
// model, delete this file — do not keep it as a compatibility shim.
package media

import (
	"context"
	"database/sql"
	"fmt"
)

// MediaDriveFileLister lists the asset ids of media-SSOT rows that still carry
// a non-empty Drive file id and are not soft-deleted.
type MediaDriveFileLister struct {
	db *sql.DB
}

// NewMediaDriveFileLister returns a lister bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path, so this panics in the same way NewMediaSearcher does.
func NewMediaDriveFileLister(db *sql.DB) *MediaDriveFileLister {
	if db == nil {
		panic("media.NewMediaDriveFileLister: db is required")
	}
	return &MediaDriveFileLister{db: db}
}

// DB exposes the underlying handle so the composition root can assert it lives
// on the media SSOT (see the media write/read bridge probes).
func (l *MediaDriveFileLister) DB() *sql.DB {
	if l == nil {
		return nil
	}
	return l.db
}

// ListAssetIDsWithDriveFileID returns the media asset ids with a Drive file id.
//
// The predicate mirrors the retired SQLite statement exactly; `<>` is used
// rather than `IS DISTINCT FROM` on purpose, because `lifecycle_state <> 'DELETED'`
// is NULL — and therefore excludes the row — for a NULL lifecycle_state under
// both engines. Restating it as `IS DISTINCT FROM` would silently start
// including NULL-lifecycle rows and change the result set.
func (l *MediaDriveFileLister) ListAssetIDsWithDriveFileID(ctx context.Context) ([]string, error) {
	if l == nil || l.db == nil {
		return nil, fmt.Errorf("media drive file lister: media SSOT handle is not configured")
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT id FROM media_assets
		WHERE drive_file_id IS NOT NULL AND drive_file_id <> ''
		  AND lifecycle_state <> 'DELETED'
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("media drive file lister: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("media drive file lister: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media drive file lister: iterate: %w", err)
	}
	return ids, nil
}
