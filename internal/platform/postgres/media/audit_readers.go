// Package media — audit_readers.go: the media reads the operator audit
// commands depend on (clip-drive divergence, broken references, structured
// asset identity).
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): these readers replace the
// media_assets scans cmd/admin used to run on the operational SQLite handle.
// media_assets is PostgreSQL-owned and that mirror holds no committed media
// rows, so every one of those audits was grading an empty table. Each reader
// exposes ONLY the media-owned columns its command needs, so the audit cannot
// quietly re-acquire a second media engine; none of them write.
//
// The three readers share a file because they are one concern (read the media
// SSOT for an operator audit) and the package sits at its file-count budget.
package media

import (
	"context"
	"database/sql"
	"fmt"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// ClipAuditRow is the projection of a canonical YouTube clip row that the
// clip-drive audit compares against the live Drive tree.
type ClipAuditRow struct {
	ID           string
	DriveFileID  string
	DriveLink    string
	DownloadLink string
	FolderID     string
	FolderPath   string
}

// ClipDriveAuditReader answers the clip-drive audit's media reads from the
// PostgreSQL media SSOT.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): this replaces the retired
// SQLite reads in `cmd/admin/internal/audit/clip_drive_audit.go`. The audit
// compares committed media_assets rows against the live Google Drive tree, and
// media_assets is PostgreSQL-owned — reading it from the operational SQLite
// store could only ever report every Drive clip as missing. The audit is
// read-only (no --apply), so no write surface moves here.
//
// One query serves all three former reads (per-clip rows, the drive_file_id
// set, and the drive_link/download_link set): they share the same row scope
// (source='youtube' AND id LIKE 'yt_%') and only differ in which columns the
// caller consumes and whether a LIMIT applies. Collapsing them removes the
// possibility of the three scopes drifting apart.
type ClipDriveAuditReader struct {
	db *sql.DB
}

// NewClipDriveAuditReader binds the reader to the media SSOT handle. A nil
// handle returns nil so composition fails closed rather than silently reading
// a second engine (godlike/07 no-fake-availability).
func NewClipDriveAuditReader(db *sql.DB) *ClipDriveAuditReader {
	if db == nil {
		return nil
	}
	return &ClipDriveAuditReader{db: db}
}

const clipAuditSelectColumns = `
	SELECT id,
	       COALESCE(drive_file_id, ''),
	       COALESCE(drive_link, ''),
	       COALESCE(download_link, ''),
	       COALESCE(folder_id, ''),
	       COALESCE(folder_path, '')
	FROM media_assets
	WHERE source = 'youtube'
	  AND id LIKE 'yt_%'
	ORDER BY id
`

// ListYouTubeClipRows returns the canonical YouTube clip rows, bounded by
// limit when limit > 0. A non-positive limit means "no bound" — the audit's
// orphan/untracked comparison must see the full row set or a bounded run would
// mislabel reachable Drive files as orphans.
func (r *ClipDriveAuditReader) ListYouTubeClipRows(ctx context.Context, limit int) ([]ClipAuditRow, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres clip-drive audit: media reader unavailable")
	}
	query := clipAuditSelectColumns
	var args []any
	if limit > 0 {
		query += "\tLIMIT $1\n"
		args = append(args, limit)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres clip-drive audit: query: %w", err)
	}
	defer rows.Close()

	var out []ClipAuditRow
	for rows.Next() {
		var rec ClipAuditRow
		if err := rows.Scan(&rec.ID, &rec.DriveFileID, &rec.DriveLink, &rec.DownloadLink, &rec.FolderID, &rec.FolderPath); err != nil {
			return nil, fmt.Errorf("postgres clip-drive audit: scan: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres clip-drive audit: iterate: %w", err)
	}
	return out, nil
}

// MediaDriveRef is one media asset that carries a Drive identity pointer.
//
// The location field is typed `json:"-"` on purpose: it is a RUNTIME value
// copied out of the SSOT for one in-process comparison and never crosses a
// serialization boundary. The audit's report DTOs are separate types, so no
// consumer can read the location as data. That is the shape the media-identity
// invariant asks for (one owner per fact): the durable location lives in
// asset_locations, not in this row.
type MediaDriveRef struct {
	AssetID     string
	DriveFileID string `json:"-"`
}

// MediaLocalPathRef is one media asset that carries a local file path. See
// MediaDriveRef for why the path is confined to the process by its tag.
type MediaLocalPathRef struct {
	AssetID   string
	LocalPath string `json:"-"`
}

// MediaReferenceAuditReader answers the broken-reference audit's media reads
// from the PostgreSQL media SSOT.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): this replaces the
// media_assets rows that cmd/admin's broken-references audit used to sweep out
// of the operational SQLite handle (its `tablesWithColumn("drive_file_id")` and
// `tablesWithColumn("local_path")` loops, plus its Qdrant eligibility scan).
// media_assets is PostgreSQL-owned; the operational mirror holds no committed
// media rows, so every media reference looked "resolvable" simply because there
// were no rows to check.
//
// The non-media tables those loops sweep stay on the operational handle — this
// reader deliberately exposes ONLY the media-owned columns, so the audit cannot
// quietly re-acquire a second media engine.
type MediaReferenceAuditReader struct {
	db *sql.DB
}

// NewMediaReferenceAuditReader binds the reader to the media SSOT handle. A nil
// handle returns nil so callers fail closed rather than degrading onto the
// operational store (godlike/07 no-fake-availability).
func NewMediaReferenceAuditReader(db *sql.DB) *MediaReferenceAuditReader {
	if db == nil {
		return nil
	}
	return &MediaReferenceAuditReader{db: db}
}

// ListDriveFileRefs returns every non-deleted asset with a non-empty
// drive_file_id, ordered by id, so the audit can cross-check the reference
// against the live Drive listing without a second lookup.
func (r *MediaReferenceAuditReader) ListDriveFileRefs(ctx context.Context) ([]MediaDriveRef, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres media reference audit: media reader unavailable")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, drive_file_id
		FROM media_assets
		WHERE COALESCE(drive_file_id, '') <> ''
		  AND COALESCE(lifecycle_state, '') <> 'DELETED'
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("postgres media reference audit: query drive refs: %w", err)
	}
	defer rows.Close()

	var out []MediaDriveRef
	for rows.Next() {
		var rec MediaDriveRef
		if err := rows.Scan(&rec.AssetID, &rec.DriveFileID); err != nil {
			return nil, fmt.Errorf("postgres media reference audit: scan drive ref: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media reference audit: iterate drive refs: %w", err)
	}
	return out, nil
}

// ListLocalPaths returns every non-deleted asset with a non-empty local_path,
// ordered by id.
func (r *MediaReferenceAuditReader) ListLocalPaths(ctx context.Context) ([]MediaLocalPathRef, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres media reference audit: media reader unavailable")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, local_path
		FROM media_assets
		WHERE COALESCE(local_path, '') <> ''
		  AND COALESCE(lifecycle_state, '') <> 'DELETED'
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("postgres media reference audit: query local paths: %w", err)
	}
	defer rows.Close()

	var out []MediaLocalPathRef
	for rows.Next() {
		var rec MediaLocalPathRef
		if err := rows.Scan(&rec.AssetID, &rec.LocalPath); err != nil {
			return nil, fmt.Errorf("postgres media reference audit: scan local path: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media reference audit: iterate local paths: %w", err)
	}
	return out, nil
}

// ListSearchEligibleAssetIDs returns the ids of assets that satisfy the
// canonical search-index eligibility predicate (mediaregistry SSOT), ordered by
// id.
//
// godlike/06 SSOT: the predicate is capregistry.SearchIndexEligibilitySQL — the
// same constant the projection planes use — so the audit cannot grade a
// different boundary than the writer it is auditing.
func (r *MediaReferenceAuditReader) ListSearchEligibleAssetIDs(ctx context.Context) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres media reference audit: media reader unavailable")
	}
	query := `
		SELECT id FROM media_assets
		WHERE (` + capregistry.SearchIndexEligibilitySQL + `)
		  AND COALESCE(media_type, '') <> 'folder'
		ORDER BY id`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres media reference audit: query eligible assets: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres media reference audit: scan eligible asset: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media reference audit: iterate eligible assets: %w", err)
	}
	return out, nil
}

// StructuredAssetIdentityRow is the media_assets projection a structured
// identity audit needs to decide membership from canonical facts (structured
// metadata, IDs, content hashes) rather than from filenames or display names.
// The Drive pointer is tagged `json:"-"`: it is a runtime value read for one
// in-process identity decision and never crosses a serialization boundary. The
// audit's report DTOs are separate types, so the durable location stays owned by
// asset_locations rather than by this projection.
type StructuredAssetIdentityRow struct {
	ID             string
	MediaType      string
	DriveFileID    string `json:"-"`
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
