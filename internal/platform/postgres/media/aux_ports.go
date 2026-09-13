// Package media — aux_ports.go: the PostgreSQL implementations of the
// narrow engine-named write ports for the media aggregate's auxiliary
// surfaces: persistence.AssetLocationWriter (asset_locations) and
// persistence.AssetProcessingWriter (asset_processing).
//
// These live on PostgresMediaCommitter — the canonical media committer — for
// the same reason soft-delete does: the port must be resolvable from the
// committer the composition root actually wired, so
// persistence.CanonicalAssetLocationWriter / CanonicalAssetProcessingWriter
// can never hand a capability an implementation that writes a different
// database than the one that owns media_assets.
//
// asset_locations: the atomic CommitRequest path already upserts locations
// inside the commit transaction (committer.go::upsertLocations). This port
// covers the POINT-WISE case — a location attached after the media row
// exists — against the same table and the same (asset_id, location_kind)
// conflict target, so the two write shapes cannot diverge.
//
// asset_processing: rows transition repeatedly over the life of one asset
// (Start → Complete/Fail) and therefore cannot ride inside the single create
// transaction. The statement semantics mirror the SQLite
// imagesregistry.AssetStoreSQLite processing queries exactly
// (StartProcessing/CompleteProcessing/FailProcessing) so the cutover is a
// composition-root swap rather than a behavioral change; the difference is
// the engine: this one is the media SSOT.
package media

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Compile-time assertions: the canonical media committer IS the engine-named
// auxiliary writer family.
var (
	_ persistence.AssetLocationWriter   = (*PostgresMediaCommitter)(nil)
	_ persistence.AssetProcessingWriter = (*PostgresMediaCommitter)(nil)
)

// UpsertAssetLocation writes one asset_locations row on the media SSOT.
//
// Idempotent on the canonical (asset_id, location_kind) conflict target; the
// column projection and update set are the ones the atomic CommitRequest path
// (PostgresAssetCommitter.upsertLocations) uses, so a point-wise attach and a
// commit-time attach produce identical rows.
func (c *PostgresMediaCommitter) UpsertAssetLocation(ctx context.Context, loc *asset.Location) error {
	if c == nil || c.db == nil {
		return errors.New("media committer: asset location writer is unavailable")
	}
	if loc == nil || loc.AssetID == "" {
		return errors.New("media committer: asset location requires an asset id")
	}
	if loc.LocationKind == "" {
		return fmt.Errorf("media committer: asset location for %q requires a location kind", loc.AssetID)
	}
	now := time.Now().UTC()
	createdAt := loc.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := loc.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	createdStr := timeutil.FormatRFC3339(createdAt)
	updatedStr := timeutil.FormatRFC3339(updatedAt)

	_, err := c.db.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at, created_at_ts, updated_at_ts)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($11, '')::timestamptz, NULLIF($12, '')::timestamptz)
		ON CONFLICT (asset_id, location_kind) DO UPDATE SET
			uri = excluded.uri,
			external_id = excluded.external_id,
			web_view_link = excluded.web_view_link,
			download_url = excluded.download_url,
			mime_type = excluded.mime_type,
			file_size_bytes = excluded.file_size_bytes,
			legacy_file_md5 = excluded.legacy_file_md5,
			is_primary = excluded.is_primary,
			updated_at = excluded.updated_at,
			updated_at_ts = excluded.updated_at_ts
	`, loc.AssetID, string(loc.LocationKind), loc.URI, loc.ExternalID, loc.AccessURL, loc.DownloadURL,
		loc.MimeType, loc.FileSizeBytes, loc.LegacyFileMD5, pgBoolInt(loc.IsPrimary), createdStr, updatedStr)
	if err != nil {
		return fmt.Errorf("media committer: upsert asset location %s/%s: %w", loc.AssetID, loc.LocationKind, err)
	}
	return nil
}

// StartAssetProcessing transitions one processing step to running on the
// media SSOT. Mirrors SQLite AssetStoreSQLite.StartProcessing: the upsert
// increments attempt_count on retry, keeps the first started_at, clears any
// previous failure and resets completed_at.
func (c *PostgresMediaCommitter) StartAssetProcessing(ctx context.Context, assetID, step string) error {
	if err := c.validateProcessingStep(assetID, step); err != nil {
		return err
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO asset_processing (asset_id, step, status, started_at, attempt_count, created_at, updated_at, started_at_ts)
		VALUES ($1, $2, 'running', $3, 1, $4, $5, NULLIF($3, '')::timestamptz)
		ON CONFLICT (asset_id, step) DO UPDATE SET
			status = 'running',
			started_at = COALESCE(asset_processing.started_at, excluded.started_at),
			attempt_count = asset_processing.attempt_count + 1,
			error_message = '',
			completed_at = NULL,
			completed_at_ts = NULL,
			updated_at = excluded.updated_at
	`, assetID, step, now, now, now)
	if err != nil {
		return fmt.Errorf("media committer: start processing %s/%s: %w", assetID, step, err)
	}
	return nil
}

// CompleteAssetProcessing transitions one processing step to completed on the
// media SSOT. Mirrors SQLite AssetStoreSQLite.CompleteProcessing, including
// its no-op behavior when the step row does not exist.
func (c *PostgresMediaCommitter) CompleteAssetProcessing(ctx context.Context, assetID, step string) error {
	if err := c.validateProcessingStep(assetID, step); err != nil {
		return err
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := c.db.ExecContext(ctx, `
		UPDATE asset_processing
		SET status = 'completed', completed_at = $1, error_message = '', updated_at = $2,
		    completed_at_ts = NULLIF($1, '')::timestamptz
		WHERE asset_id = $3 AND step = $4
	`, now, now, assetID, step)
	if err != nil {
		return fmt.Errorf("media committer: complete processing %s/%s: %w", assetID, step, err)
	}
	return nil
}

// FailAssetProcessing transitions one processing step to failed on the media
// SSOT. Mirrors SQLite AssetStoreSQLite.FailProcessing, including its no-op
// behavior when the step row does not exist.
func (c *PostgresMediaCommitter) FailAssetProcessing(ctx context.Context, assetID, step, errMsg string) error {
	if err := c.validateProcessingStep(assetID, step); err != nil {
		return err
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := c.db.ExecContext(ctx, `
		UPDATE asset_processing
		SET status = 'failed', completed_at = $1, error_message = $2, updated_at = $3,
		    completed_at_ts = NULLIF($1, '')::timestamptz
		WHERE asset_id = $4 AND step = $5
	`, now, errMsg, now, assetID, step)
	if err != nil {
		return fmt.Errorf("media committer: fail processing %s/%s: %w", assetID, step, err)
	}
	return nil
}

// validateProcessingStep rejects the empty-identifier shapes before any SQL
// runs, so a wiring defect surfaces as a typed error rather than a constraint
// violation on the media SSOT.
func (c *PostgresMediaCommitter) validateProcessingStep(assetID, step string) error {
	if c == nil || c.db == nil {
		return errors.New("media committer: asset processing writer is unavailable")
	}
	if assetID == "" {
		return errors.New("media committer: asset processing requires an asset id")
	}
	if step == "" {
		return fmt.Errorf("media committer: asset processing for %q requires a step", assetID)
	}
	return nil
}
