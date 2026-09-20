package media

import (
	"context"
	"database/sql"
	"fmt"
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
