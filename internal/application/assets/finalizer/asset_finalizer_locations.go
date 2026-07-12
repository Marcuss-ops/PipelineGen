// Package finalizer — asset_finalizer_locations.go (split from
// asset_finalizer_tx.go, July 2026): helper SQL for the canonical
// asset_locations table — primary location row.
//
// Owns:
//
//  1. func (s *AssetTxFinalizer) upsertAssetLocation — UPSERT the
//     primary asset_locations row carrying artifact.Location
//     (the main storage URI for this asset). is_primary=1 marks
//     this row as the canonical storage location.
//
// Per-rendition locations (a SEPARATE asset_locations row per
// rendition, with is_primary=0 and a distinct location_kind of
// shape "<provider>_<kind>") live in
// asset_finalizer_renditions.go — they are persisted with their
// matching asset_renditions row so the rendition's location_id
// can resolve through the FK.
//
// Caller-owned-tx discipline (godlike/06 SSOT, non-negotiable
// architectural rule): same as sibling helpers — uses
// finalization.Transaction. Does NOT own BeginTx.
//
// Location-kind default (godlike/07 fail-closed): falls back to
// "drive" when Location.Provider is empty so the
// (asset_id, location_kind) UNIQUE constraint never encounters
// an empty-string kind. The default matches the production
// storage backend in PipelineGen; tests may override via the
// PublishedArtifact.Location.Provider field.
//
// Mechanical split from asset_finalizer_tx.go. Zero behavior
// change. The receiver (s *AssetTxFinalizer) is unchanged so the
// orchestrator can call this helper as `s.upsertAssetLocation(...)`
// without any wiring change.
package finalizer

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// upsertAssetLocation inserts or updates the canonical
// asset_locations row (primary location, is_primary=1).
func (s *AssetTxFinalizer) upsertAssetLocation(
	ctx context.Context,
	tx finalization.Transaction,
	a *finalization.PublishedArtifact,
	nowStr string,
) error {
	locationKind := a.Location.Provider
	if locationKind == "" {
		locationKind = "drive"
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(asset_id, location_kind) DO UPDATE SET
			uri = excluded.uri,
			external_id = excluded.external_id,
			web_view_link = excluded.web_view_link,
			download_url = excluded.download_url,
			mime_type = excluded.mime_type,
			file_size_bytes = excluded.file_size_bytes,
			file_hash = excluded.file_hash,
			is_primary = excluded.is_primary,
			updated_at = excluded.updated_at
	`,
		a.ArtifactID,
		locationKind,
		a.Location.FileID,
		a.Location.FileID,
		a.Location.WebViewLink,
		a.Location.DownloadLink,
		a.MIMEType,
		a.SizeBytes,
		a.SHA256,
		nowStr,
		nowStr,
	)
	if err != nil {
		return fmt.Errorf("asset finalizer: upsert location for %s: %w", a.ArtifactID, err)
	}
	return nil
}
