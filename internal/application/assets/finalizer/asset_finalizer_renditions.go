// Package finalizer — asset_finalizer_renditions.go (split from
// asset_finalizer_tx.go, July 2026): helper SQL for the
// asset_renditions + per-rendition asset_locations rows.
//
// Owns:
//
//  1. func (s *AssetTxFinalizer) upsertRenditionLocation — persist
//     one rendition as TWO coupled rows inside the caller's tx:
//     a) asset_locations (location_kind = the canonical physical
//     location kind, is_primary=0). The database contract permits only
//     local, drive, and object_storage; rendition kind belongs in
//     asset_renditions.kind, not in location_kind.
//     b) asset_renditions (ON CONFLICT(asset_id, kind) DO UPDATE),
//     the canonical storage for technical variant metadata
//     (container/codec/width/height/fps/bitrate/sha256/
//     size_bytes).
//
// The asset_locations row carries the URI; the asset_renditions
// row carries the codec + dimensions. They are joined by FK
// (asset_renditions.location_id → asset_locations.id). Because
// LastInsertId is unreliable on an ON CONFLICT path, the
// location_id is re-read via QueryRowContext on the unique key.
//
// Caller-owned-tx discipline (godlike/06 SSOT, non-negotiable
// architectural rule): same as sibling helpers. Does NOT own
// BeginTx.
//
// Skip semantics (godlike/07 fail-closed): a rendition with
// empty URI is a no-op (no asset_locations or asset_renditions
// row written) — the caller can supply empty renditions to
// opt-out per-rendition persistence without leaking partial
// state into the canonical tables.
//
// Mechanical split from asset_finalizer_tx.go. Zero behavior
// change. The receiver (s *AssetTxFinalizer) is unchanged so the
// orchestrator can call this helper as `s.upsertRenditionLocation(...)`
// without any wiring change.
package finalizer

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// upsertRenditionLocation persists a single rendition as an
// asset_locations row and a matching asset_renditions row. The
// location is NOT marked as primary — the primary location is the
// one carried by PublishedArtifact.Location (written by
// asset_finalizer_locations.go).
func (s *AssetTxFinalizer) upsertRenditionLocation(
	ctx context.Context,
	tx finalization.Transaction,
	a *finalization.PublishedArtifact,
	r *finalization.AssetRenditionLocation,
	nowStr string,
) error {
	if r.URI == "" {
		return nil
	}

	// location_kind is a physical-location enum owned by the schema. Do
	// not append rendition kind here: asset_renditions.kind is the
	// canonical discriminator and asset_locations accepts only the three
	// physical kinds below.
	locationKind := r.Provider
	if locationKind != "drive" && locationKind != "object_storage" {
		locationKind = "local"
	}

	// 1. Upsert the rendition's location.
	_, err := tx.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
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
		r.URI,
		r.FileID,
		r.WebViewLink,
		r.DownloadLink,
		r.MimeType,
		r.SizeBytes,
		r.FileHash,
		nowStr,
		nowStr,
	)
	if err != nil {
		return fmt.Errorf("asset finalizer: upsert rendition location %s/%s: %w", a.ArtifactID, r.Kind, err)
	}

	// 2. Resolve the location_id after upsert. LastInsertId is unreliable
	// when the row already existed, so we re-read by the unique key.
	var locationID int64
	row := tx.QueryRowContext(ctx,
		`SELECT id FROM asset_locations WHERE asset_id = ? AND location_kind = ?`,
		a.ArtifactID, locationKind,
	)
	if err := row.Scan(&locationID); err != nil {
		return fmt.Errorf("asset finalizer: resolve location_id for %s/%s: %w", a.ArtifactID, r.Kind, err)
	}

	// 3. Upsert the asset_renditions row on (asset_id, kind).
	_, err = tx.ExecContext(ctx, `
		INSERT INTO asset_renditions
			(id, asset_id, location_id, kind, container, codec, width, height,
			 fps, bitrate, sha256, size_bytes, created_at, updated_at)
		VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id, kind) DO UPDATE SET
			location_id = excluded.location_id,
			container = excluded.container,
			codec = excluded.codec,
			width = excluded.width,
			height = excluded.height,
			fps = excluded.fps,
			bitrate = excluded.bitrate,
			sha256 = excluded.sha256,
			size_bytes = excluded.size_bytes,
			updated_at = excluded.updated_at
	`,
		a.ArtifactID,
		locationID,
		r.Kind,
		r.Container,
		r.Codec,
		r.Width,
		r.Height,
		r.FPS,
		r.Bitrate,
		r.FileHash,
		r.SizeBytes,
		nowStr,
		nowStr,
	)
	if err != nil {
		return fmt.Errorf("asset finalizer: upsert rendition %s/%s: %w", a.ArtifactID, r.Kind, err)
	}
	return nil
}
