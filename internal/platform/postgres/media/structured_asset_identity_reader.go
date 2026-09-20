package media

import (
	"context"
	"database/sql"
	"fmt"
)

// StructuredAssetIdentityRow is the media_assets projection a structured
// identity audit needs to decide membership from canonical facts (structured
// metadata, IDs, content hashes) rather than from filenames or display names.
type StructuredAssetIdentityRow struct {
	ID             string
	MediaType      string
	DriveFileID    string
	YouTubeVideoID string
	StartMS        int64
	EndMS          int64
	ContentSHA256  string
	BinarySHA256   string
	MetadataJSON   string
}

// StructuredAssetIdentityReader answers structured-identity audits from the
// PostgreSQL media SSOT.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): this replaces the retired
// `SELECT ... FROM media_assets` taken from the operational SQLite handle by
// cmd/admin's asset audits. Those audits decide membership and duplicate groups
// from committed media rows, and media_assets is PostgreSQL-owned, so the
// SQLite read could only ever enumerate the mirror — which holds no committed
// media rows — and the audit reported "0 assets" while PostgreSQL held them.
//
// The projection is deliberately the FULL non-deleted row set: the audits
// filter in Go (metadata keys, catalog candidate links, hash grouping), so
// pushing a predicate down here would move the audit's identity rules into SQL
// and let the two definitions drift.
type StructuredAssetIdentityReader struct {
	db *sql.DB
}

// NewStructuredAssetIdentityReader binds the reader to the media SSOT handle. A
// nil handle returns nil so callers fail closed rather than reading a second
// engine (godlike/07 no-fake-availability).
func NewStructuredAssetIdentityReader(db *sql.DB) *StructuredAssetIdentityReader {
	if db == nil {
		return nil
	}
	return &StructuredAssetIdentityReader{db: db}
}

const structuredAssetIdentityQuery = `
	SELECT id,
	       COALESCE(media_type, ''),
	       COALESCE(drive_file_id, ''),
	       COALESCE(NULLIF(source_video_id, ''), COALESCE(youtube_video_id, '')),
	       COALESCE(start_ms, 0),
	       COALESCE(end_ms, 0),
	       COALESCE(content_sha256, ''),
	       COALESCE(binary_sha256, ''),
	       COALESCE(metadata_json, '{}')
	FROM media_assets
	WHERE COALESCE(lifecycle_state, '') <> 'DELETED'
	ORDER BY id
`

// ListStructuredAssetIdentities returns every non-deleted media asset's
// identity facts, ordered by id.
func (r *StructuredAssetIdentityReader) ListStructuredAssetIdentities(ctx context.Context) ([]StructuredAssetIdentityRow, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres structured asset identity: media reader unavailable")
	}
	rows, err := r.db.QueryContext(ctx, structuredAssetIdentityQuery)
	if err != nil {
		return nil, fmt.Errorf("postgres structured asset identity: query: %w", err)
	}
	defer rows.Close()

	var out []StructuredAssetIdentityRow
	for rows.Next() {
		var rec StructuredAssetIdentityRow
		if err := rows.Scan(&rec.ID, &rec.MediaType, &rec.DriveFileID, &rec.YouTubeVideoID,
			&rec.StartMS, &rec.EndMS, &rec.ContentSHA256, &rec.BinarySHA256, &rec.MetadataJSON); err != nil {
			return nil, fmt.Errorf("postgres structured asset identity: scan: %w", err)
		}
		if rec.MetadataJSON == "" {
			rec.MetadataJSON = "{}"
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres structured asset identity: iterate: %w", err)
	}
	return out, nil
}
