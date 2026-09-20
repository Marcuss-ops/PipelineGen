package media

import (
	"context"
	"database/sql"
	"fmt"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// MediaDriveRef is one media asset that carries a Drive identity pointer.
type MediaDriveRef struct {
	AssetID     string
	DriveFileID string
}

// MediaLocalPathRef is one media asset that carries a local file path.
type MediaLocalPathRef struct {
	AssetID   string
	LocalPath string
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
