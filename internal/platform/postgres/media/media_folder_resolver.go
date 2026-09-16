// Package media — media_folder_resolver.go: the parent-video Drive folder read
// behind finalization's ArtifactFolderResolver.
//
// MEDIA-SSOT P2-9 Phase 2: this replaces
// internal/app/wiring/assets/folders.go, which issued
//
//	SELECT COALESCE(NULLIF(folder_id, ''), drive_folder_id, '') FROM media_assets WHERE id = ?
//
// against the OPERATIONAL SQLite handle. PostgreSQL owns media_assets, so the
// resolver looked up the parent video's folder in a database that holds no
// committed media rows — a sidecar overlay would have published against an
// unresolved folder ID.
//
// FOLDER SEMANTICS DECISION (2026-09-16): the `drive_folder_id` fallback is
// DELETED, not ported. Measured basis:
//
//   - media_assets.drive_folder_id exists only in the legacy SQLite schema.
//     The PostgreSQL media SSOT has NO such column
//     (information_schema.columns: 0 rows), so a ported fallback could not
//     compile against the SSOT shape.
//   - Zero rows depend on it: the operational SQLite media_assets table holds
//     0 rows, so 0 rows had an empty folder_id with a populated drive_folder_id.
//   - `folder_id` is the canonical column and is the one the canonical committer
//     writes.
//
// `COALESCE(folder_id, ”)` is equivalent to the retired
// `COALESCE(NULLIF(folder_id, ”), drive_folder_id, ”)` once the fallback is
// removed: NULLIF turns ” into NULL and the final COALESCE maps it back to ”.
// Perpetuating a legacy fallback column inside the new SSOT is exactly the
// re-derivation this cutover removes; if a future row ever genuinely needs a
// separate folder source, that is a new column with its own migration, not a
// revival of this one.
//
// Removal condition: this file exists for as long as finalization needs to
// resolve a parent video's folder. It is a media read, not compatibility
// scaffolding — do not reintroduce an engine branch here.
package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// MediaFolderResolver resolves the canonical Drive folder id of a media asset.
// It satisfies finalization.ArtifactFolderResolver structurally so this package
// does not import the capability.
type MediaFolderResolver struct {
	db *sql.DB
}

// NewMediaFolderResolver returns a resolver bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path.
func NewMediaFolderResolver(db *sql.DB) *MediaFolderResolver {
	if db == nil {
		panic("media.NewMediaFolderResolver: db is required")
	}
	return &MediaFolderResolver{db: db}
}

// DB exposes the underlying handle so callers can assert the resolver lives on
// the media SSOT.
func (r *MediaFolderResolver) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// ResolveArtifactFolder returns the Drive folder id of the parent video.
//
// An empty return means "not resolved" and the caller keeps the legacy path
// (see the finalization.ArtifactFolderResolver contract) — that includes the
// unknown-asset case, which the retired SQLite statement also mapped to an empty
// string rather than an error.
func (r *MediaFolderResolver) ResolveArtifactFolder(ctx context.Context, parentVideoID string) (string, error) {
	if r == nil || r.db == nil {
		return "", fmt.Errorf("media folder resolver: media SSOT handle is not configured")
	}
	var folderID string
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(folder_id, '') FROM media_assets WHERE id = $1`,
		parentVideoID,
	).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve artifact folder %q: %w", parentVideoID, err)
	}
	return folderID, nil
}
