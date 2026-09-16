// Package enrichment — handler_repository.go (PR-SPLIT-ENRICHMENT-HANDLER, August 2026;
// POSTGRES-MEDIA-CUTOVER 2026-09-16).
//
// Canonical owner of the PostgreSQL-backed AssetRepository concrete (type +
// ctor + 2 methods + compile-time pin).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - PostgresAssetRepository concrete lives ONLY in this file.
//   - AssetRepository port (the canonical narrow seam for
//     "read media_assets row by id + update metadata_json") lives
//     ONLY in handler.go — this concrete is the SOLE production
//     implementor.
//   - The 4 typed sentinels (WrapHandlerNotConfigured / WrapPersistFailed
//     / WrapChunkNotFound / WrapInvalidLLMResponse) live ONLY in errors.go.
//
// MEDIA SSOT (September 2026 cutover). This concrete reads media_assets on the
// SAME PostgreSQL database that owns the canonical media committer. The
// previous SQLiteAssetRepository read the operational SQLite mirror, which is
// the Postgres-writer/SQLite-reader split-brain: a clip committed by the
// canonical committer was invisible to enrichment, and the operational store
// can legitimately hold zero media rows (measured on the live dev store), so
// the read silently reported WrapChunkNotFound for assets that exist. There is
// deliberately NO SQLite fallback — a media read outside the media SSOT is a
// defect, not a degradation mode (persistence.RequireMediaPostgres).
//
// godlike/07 fail-closed contracts:
//   - NewPostgresAssetRepository returns (nil, WrapHandlerNotConfigured)
//     when the media DB handle is nil. Composition root MUST propagate.
//   - GetByID returns (nil, WrapChunkNotFound(id)) on sql.ErrNoRows
//     (canonical terminal sentinel for "row not found"); other SQL
//     errors wrap WrapPersistFailed for SQL-side diagnostic.
//   - UpdateEnrichedMetadata is idempotent on retry (UPDATE is naturally
//     idempotent given the same EnrichedFields input).
//
// godlike/07 minimum-blast-radius: the 2 methods' signatures are
// STABLE — orchestrator's HandleJob calls them byte-equivalent
// pre/post cutover.
package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// PostgresAssetRepository is the production concrete AssetRepository.
// It wraps the PostgreSQL media-SSOT *sql.DB and reads media_assets by id
// (the metadata update already routes through the canonical committer via
// AssetMetadataUpdater).
//
// godlike/06 SSOT (one canonical owner per fact):
// PostgresAssetRepository lives ONLY in this file. The composition
// root wires this concrete with the same DB handle the canonical media
// committer owns.
type PostgresAssetRepository struct {
	// DB is the PostgreSQL media-SSOT *sql.DB handle.
	DB              *sql.DB
	MetadataUpdater AssetMetadataUpdater
}

// NewPostgresAssetRepository constructs the canonical concrete with a
// fail-closed nil-DB gate per godlike/07 typed-error contract.
// Returns (nil, WrapHandlerNotConfigured) when db is nil.
func NewPostgresAssetRepository(db *sql.DB) (*PostgresAssetRepository, error) {
	if db == nil {
		return nil, WrapHandlerNotConfigured("media postgres db")
	}
	return &PostgresAssetRepository{DB: db}, nil
}

// SetMetadataUpdater injects the canonical MediaCommitter-owned mutation
// surface. There is deliberately no direct-SQL fallback.
func (r *PostgresAssetRepository) SetMetadataUpdater(updater AssetMetadataUpdater) {
	if r != nil {
		r.MetadataUpdater = updater
	}
}

// pgMediaEnrichmentColumns is the SELECT projection the enrichment envelope
// needs. It is the SQLite mirror's column set, retargeted at the PostgreSQL
// media SSOT:
//
//	description / drive_path live in metadata_json (read with the jsonb ->>
//	operator); start/end are the millisecond columns start_ms/end_ms and are
//	converted to float seconds in Go (not in SQL) so no numeric/decimal
//	round-trip type is involved.
const pgMediaEnrichmentSelect = `
	SELECT id,
	       COALESCE(source_url, ''),
	       COALESCE(title, ''),
	       COALESCE((COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb ->> 'description'), ''),
	       COALESCE(start_ms, 0),
	       COALESCE(end_ms, 0),
	       COALESCE(source_provider, ''),
	       COALESCE(drive_file_id, ''),
	       COALESCE((COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb ->> 'drive_path'), ''),
	       COALESCE(legacy_file_md5, '')
	FROM media_assets
	WHERE id = $1`

// GetByID reads the canonical media_assets row by id on the media SSOT.
// Returns (nil, WrapChunkNotFound(id)) when the row is absent
// (sql.ErrNoRows) — the canonical terminal sentinel.
// Other SQL errors wrap WrapPersistFailed (SQL-side diagnostic).
//
// Columns are COALESCE-wrapped to NULL → "" / 0 mappings so a legacy row
// written before a column existed returns empty strings (not nil-pointer
// panics) — the emitter then either uses an empty drive_file_id (the canonical
// v1 envelope allows omitempty) or the idempotency_key derivation fails with
// ErrEnrichmentIdempotencyKeyConflict on an empty file hash (terminal,
// surfaces as a producer-side state gap).
func (r *PostgresAssetRepository) GetByID(ctx context.Context, id string) (*AssetRow, error) {
	if r == nil || r.DB == nil {
		return nil, WrapHandlerNotConfigured("repo")
	}
	var out AssetRow
	var startMS, endMS int64
	err := r.DB.QueryRowContext(ctx, pgMediaEnrichmentSelect, id).Scan(
		&out.ID, &out.SourceURL, &out.Title, &out.Description,
		&startMS, &endMS, &out.SourceProvider, &out.DriveFileID,
		&out.DrivePath, &out.LegacyFileMD5,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, WrapChunkNotFound(id)
		}
		return nil, WrapPersistFailed(err)
	}
	// Canonical AssetRow.StartSec/EndSec are float seconds.
	out.StartSec = float64(startMS) / 1000.0
	out.EndSec = float64(endMS) / 1000.0
	return &out, nil
}

// UpdateEnrichedMetadata persists the EnrichedFields into
// media_assets.metadata_json through the canonical committer-owned
// AssetMetadataUpdater (there is no direct-SQL mutation path).
//
// godlike/07 minimum-blast-radius: the implementation is
// idempotent on retry (UPDATE is naturally idempotent given
// the same EnrichedFields input).
func (r *PostgresAssetRepository) UpdateEnrichedMetadata(ctx context.Context, id string, fields EnrichedFields) error {
	if r == nil || r.DB == nil {
		return WrapHandlerNotConfigured("repo")
	}
	if r.MetadataUpdater == nil {
		return WrapHandlerNotConfigured("canonical metadata updater")
	}
	metaJSON, err := json.Marshal(fields)
	if err != nil {
		return WrapInvalidLLMResponse(err)
	}
	if err := r.MetadataUpdater.UpdateAssetMetadata(ctx, id, string(metaJSON)); err != nil {
		return WrapPersistFailed(err)
	}
	return nil
}

// Compile-time assertion: *PostgresAssetRepository satisfies the
// AssetRepository port. Catches signature drift at compile time
// per AGENTS.md Pattern 0 / godlike/06 SSOT.
var _ AssetRepository = (*PostgresAssetRepository)(nil)
