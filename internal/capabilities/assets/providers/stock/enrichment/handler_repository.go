// Package enrichment — handler_repository.go (PR-SPLIT-ENRICHMENT-HANDLER, August 2026).
//
// Canonical owner of the SQLiteAssetRepository concrete (type + ctor
// + 2 methods + compile-time pin). Extracted from the 596 LoC
// handler.go monolith per AGENTS.md Pattern 5 + godlike/06 SSOT
// one-canonical-owner-per-fact.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - SQLiteAssetRepository concrete lives ONLY in this file.
//   - AssetRepository port (the canonical narrow seam for
//     "read media_assets row by id + update metadata_json") lives
//     ONLY in handler.go — this concrete is the SOLE implementor.
//   - The 4 typed sentinels (WrapHandlerNotConfigured / WrapPersistFailed
//     / WrapChunkNotFound / WrapInvalidLLMResponse) live ONLY in errors.go.
//
// godlike/07 fail-closed contracts:
//   - NewSQLiteAssetRepository returns (nil, ErrEnrichmentHandlerNotConfigured)
//     when DB is nil. Composition root MUST propagate the error.
//   - GetByID returns (nil, WrapChunkNotFound(id)) on sql.ErrNoRows
//     (canonical terminal sentinel for "row not found"); other SQL
//     errors wrap WrapPersistFailed for SQL-side diagnostic.
//   - UpdateEnrichedMetadata is idempotent on retry (UPDATE is naturally
//     idempotent given the same EnrichedFields input).
//
// godlike/07 minimum-blast-radius: the 2 methods' signatures are
// STABLE — orchestrator's HandleJob calls them byte-equivalent
// pre/post split. The SELECT projection expanded from 7 to 10 columns
// (PR-011C) to include the 3 drive fields (drive_file_id + drive_path
// + file_hash) required for the v1 envelope; COALESCE-wrapped to
// NULL → "" mappings so legacy rows return empty strings (no
// nil-pointer panics).
package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// SQLiteAssetRepository is the PR-011A production concrete
// AssetRepository. Wraps *sql.DB and reads media_assets by id
// + updates metadata_json (PR-011B will call the update path).
//
// godlike/06 SSOT (one canonical owner per fact):
// SQLiteAssetRepository lives ONLY in this file. The composition
// root wires this concrete via fluent setter; future production
// concretes (e.g. a sharded repository for high-throughput
// deployments) MUST implement the same AssetRepository port and
// be injected via the same fluent setter.
type SQLiteAssetRepository struct {
	// DB is the canonical *sql.DB handle. The composition root
	// injects the same DB handle the broker uses (canonical
	// SSOT for "which DB does the system read from").
	DB              *sql.DB
	MetadataUpdater AssetMetadataUpdater
}

// NewSQLiteAssetRepository constructs the canonical concrete
// with fail-closed nil-DB gate per godlike/07 typed-error
// contract. Returns (nil, ErrEnrichmentHandlerNotConfigured)
// when DB is nil.
func NewSQLiteAssetRepository(db *sql.DB) (*SQLiteAssetRepository, error) {
	if db == nil {
		return nil, WrapHandlerNotConfigured("db")
	}
	return &SQLiteAssetRepository{DB: db}, nil
}

// SetMetadataUpdater injects the canonical MediaCommitter-owned mutation
// surface. There is deliberately no direct-SQL fallback.
func (r *SQLiteAssetRepository) SetMetadataUpdater(updater AssetMetadataUpdater) {
	if r != nil {
		r.MetadataUpdater = updater
	}
}

// GetByID reads the canonical media_assets row by id. Returns
// (nil, WrapChunkNotFound(id)) when the row is absent
// (sql.ErrNoRows) — the canonical terminal sentinel.
// Other SQL errors wrap WrapPersistFailed (SQL-side diagnostic).
//
// PR-011C: the SELECT projection expanded from 7 to 10 columns
// to include the 3 drive fields required for the v1 envelope:
// drive_file_id, drive_path, file_hash. The columns are
// COALESCE-wrapped to NULL → "" / 0 mappings so a legacy row
// written before the columns existed returns empty strings (not
// nil-pointer panics) — the emitter then either uses an empty
// drive_file_id (the canonical v1 envelope allows omitempty) or
// the idempotency_key derivation fails with
// ErrEnrichmentIdempotencyKeyConflict on an empty file_hash
// (terminal, surfaces as a producer-side state gap).
//
// source_url/title/source_provider/drive_file_id/file_hash are
// first-class columns; description/drive_path live in
// metadata_json and start/end are stored as the millisecond
// columns start_ms/end_ms (converted to seconds here, matching
// the canonical AssetRow.StartSec/EndSec float-seconds contract).
func (r *SQLiteAssetRepository) GetByID(ctx context.Context, id string) (*AssetRow, error) {
	if r == nil || r.DB == nil {
		return nil, WrapHandlerNotConfigured("repo")
	}
	row := r.DB.QueryRowContext(ctx, `
		SELECT id, COALESCE(source_url, ''), COALESCE(title, ''),
		       COALESCE(json_extract(COALESCE(metadata_json, '{}'), '$.description'), ''),
		       COALESCE(NULLIF(start_ms, 0), 0) / 1000.0,
		       COALESCE(NULLIF(end_ms, 0), 0) / 1000.0,
		       COALESCE(source_provider, ''),
		       COALESCE(drive_file_id, ''),
		       COALESCE(json_extract(COALESCE(metadata_json, '{}'), '$.drive_path'), ''),
		       COALESCE(legacy_file_md5, '')
		FROM media_assets
		WHERE id = ?
	`, id)
	var out AssetRow
	if err := row.Scan(&out.ID, &out.SourceURL, &out.Title, &out.Description, &out.StartSec, &out.EndSec, &out.SourceProvider, &out.DriveFileID, &out.DrivePath, &out.LegacyFileMD5); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, WrapChunkNotFound(id)
		}
		return nil, WrapPersistFailed(err)
	}
	return &out, nil
}

// UpdateEnrichedMetadata persists the EnrichedFields into
// media_assets.metadata_json. PR-011A: declared for future
// use; the handler does NOT yet call this method (the stub
// LLM client returns ErrEnrichmentLLMUnavailable before the
// call site is reached). PR-011B will replace the call site.
//
// godlike/07 minimum-blast-radius: the implementation is
// idempotent on retry (UPDATE is naturally idempotent given
// the same EnrichedFields input). The metadata_json column
// shape mirrors the PR-001..PR-009 wire-format (JSON
// encoding of the 6 LLM-only fields).
func (r *SQLiteAssetRepository) UpdateEnrichedMetadata(ctx context.Context, id string, fields EnrichedFields) error {
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

// Compile-time assertion: *SQLiteAssetRepository satisfies the
// AssetRepository port. Catches signature drift at compile time
// per AGENTS.md Pattern 0 / godlike/06 SSOT.
//
// Note: there is no compile-time pin for *EnrichmentHandler →
// jobs.Handler because the appjobs surface uses a HandlerFunc
// adapter (not a named interface) for broker registration. The
// adapter handles the signature conversion from
// `func(ctx, *jobs.Job, *jobs.JobTools) (map[string]any, error)`
// to the domain Handler type. The RegisterHandler call site
// validates the signature at registration time.
var _ AssetRepository = (*SQLiteAssetRepository)(nil)
