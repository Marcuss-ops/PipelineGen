// Package migration re-exports the production PostgreSQL media-domain
// migration DDL as compile-time-embedded strings so integration tests can
// apply the canonical schema at runtime without shipping a hand-copied
// string constant.
//
// The package name is `migration` — mirroring the SQLite sibling bridge at
// migrations/sqlite/embed_ddl.go — so both schema families share one
// discoverable convention.
//
// SSOT (godlike/06): the bytes embedded here are the bytes that the
// canonical PostgreSQL migration runner applies to the media database.
// Drift between this package and the live database becomes a compile-time
// artefact (the test binary carries stale DDL until the .sql file changes),
// not a silent failure mode.
//
// Fail-closed (godlike/07): every embedded statement uses IF NOT EXISTS /
// OR REPLACE — re-applying on a populated database is a no-op, so
// concurrent application in test setup is safe.
//
// Test-only by convention: production code MUST NOT depend on this package.
// Production paths apply the migrations through the canonical runner or a
// deployment-managed migration step.
package migration

import _ "embed"

// MediaSchemaDDL is the verbatim CREATE TABLE + index DDL of
// migrations/postgres/001_media_schema.sql — the canonical transactional
// parity core (media_assets, asset_locations, outbox_events,
// media_asset_sources, registry_events, asset_text_tracks) written by
// PostgresMediaCommitter.
//
//go:embed 001_media_schema.sql
var MediaSchemaDDL string

// MediaVectorSurfacesDDL is the verbatim DDL of
// migrations/postgres/002_media_vector_surfaces.sql — the derived media
// surfaces (media_asset_features, media_embedding_families,
// media_embeddings + family validation trigger) written by the enrichment
// pipeline, never by producers.
//
//go:embed 002_media_vector_surfaces.sql
var MediaVectorSurfacesDDL string

// MediaHNSWIndexesDDL is the verbatim DDL of
// migrations/postgres/003_media_hnsw_indexes.sql — the production ANN
// surface: real per-family HNSW indexes over media_embeddings (semantic
// E5 768d + visual SigLIP 1152d) plus the canonical family registrations
// in media_embedding_families. POSTGRES-MEDIA-CUTOVER gates
// SEMANTIC_HNSW_INDEX=TRUE / VISUAL_HNSW_INDEX=TRUE are proven against
// these statements.
//
//go:embed 003_media_hnsw_indexes.sql
var MediaHNSWIndexesDDL string

// MediaTimestampsTimestamptzDDL is the verbatim DDL of
// migrations/postgres/004_media_timestamps_timestamptz.sql — the
// TIMESTAMPTZ expand phase for the media PostgreSQL SSOT (BASELINE_PLAN.md
// §5). It adds *_ts TIMESTAMPTZ mirrors for every hot-path TEXT timestamp,
// backfills from the legacy TEXT siblings, and creates btree/BRIN indexes
// on the new typed columns. The production dual-write lives in the
// committer (see internal/platform/postgres/media/committer.go).
//
//go:embed 004_media_timestamps_timestamptz.sql
var MediaTimestampsTimestamptzDDL string

// MediaAssetVersionsDDL is the verbatim DDL of
// migrations/postgres/006_media_asset_versions.sql — the sequential
// version history per media asset (asset_versions) written by the
// canonical AssetTxFinalizer.insertAssetVersion. Mirrors SQLite
// migration 105_asset_versions.sql for SSOT parity.
//
//go:embed 006_media_asset_versions.sql
var MediaAssetVersionsDDL string

// MediaClipFoldersDDL is the verbatim DDL of
// migrations/postgres/007_media_clip_folders.sql — the PostgreSQL projection
// required by catalog synchronization before the composition root wires the
// folder repository.
//
//go:embed 007_media_clip_folders.sql
var MediaClipFoldersDDL string

// MediaAssetProcessingDDL is the verbatim DDL of
// migrations/postgres/008_media_asset_processing.sql — the per-step
// pipeline progress surface (asset_processing) written by the canonical
// PostgresMediaCommitter through persistence.AssetProcessingWriter.
// Mirrors SQLite migration 058_asset_processing.sql for SSOT parity; the
// media-authoritative classification means this table belongs to the
// PostgreSQL media database, not the operational mirror.
//
//go:embed 008_media_asset_processing.sql
var MediaAssetProcessingDDL string

// MediaDropAssetFacesDDL is the verbatim DDL of
// migrations/postgres/009_drop_media_asset_faces.sql — the contract
// retirement of the face descriptors (has_faces / face_count /
// largest_face_ratio) from media_asset_features.
//
// It is applied AFTER MediaVectorSurfacesDDL because 002 uses
// CREATE TABLE IF NOT EXISTS: on a database that already carries the
// retired columns, 002 is a no-op and only this DROP converges the shape.
// The face endpoint those columns depended on was never served by any
// service in this deployment, so the columns made the whole features row
// unproducible.
//
//go:embed 009_drop_media_asset_faces.sql
var MediaDropAssetFacesDDL string

// MediaLegacyFileMd5ConvergeDDL is the verbatim DDL of
// migrations/postgres/012_media_legacy_file_md5_converge.sql — the
// idempotent convergence twin of the deployment-managed one-shots 010/011
// (legacy_file_md5 → content_sha256). It must run LAST: every guard probes
// the tables that 001/006 create, and it converges databases where the
// one-shots already ran. Self-bootstrap inclusion is what makes a fresh
// database reach the schema the writer family expects.
//
//go:embed 012_media_legacy_file_md5_converge.sql
var MediaLegacyFileMd5ConvergeDDL string

// MediaThumbURLContractDDL is the verbatim DDL of
// migrations/postgres/013_media_thumb_url_contract.sql — the CONTRACT phase
// of the media_assets thumbnail mirror pair (thumb_url vs thumbnail_url).
// The backfill copies the mirror into the canonical column where it is empty,
// then the mirror column is dropped (guarded, re-runnable). It must run after
// 001_media_schema.sql, which still declares the mirror for historical
// parity.
//
//go:embed 013_media_thumb_url_contract.sql
var MediaThumbURLContractDDL string

// MediaSelfBootstrapDDL is the canonical ORDERED self-bootstrap sequence for
// the media PostgreSQL domain (migration-museum audit point 9: with
// CREATE TABLE IF NOT EXISTS everywhere, application order is part of the
// contract, and hand-copied per-site lists are how a runner silently applies
// only a prefix of it). Every self-bootstrapping call site iterates THIS list
// instead of spelling its own:
//
//   - internal/platform/postgres/media/testmain_test.go::applyMediaMigrations
//   - internal/platform/postgres/media/backfill.go::RunMediaBackfill
//   - cmd/admin/internal/backfill/media_enrichment.go (previously applied only
//     001–003+009, leaving a fresh database without the 004 _ts columns that
//     the dual-writers write)
//
// Order contract (pinned by embed_ddl_contract_test.go):
//   - 002 before 009: 002's CREATE TABLE IF NOT EXISTS cannot converge a
//     database that already carries the retired face columns; only 009's
//     DROP converges the shape, so 009 must come last.
//   - 005_media_booleans.sql is a deliberate skeleton and stays UNWIRED
//     (BASELINE_PLAN.md §7) until the owning table is mutated.
//   - 010/011 are deliberate absences: deployment-managed one-shots
//     (010 is NOT idempotent — DROP/RENAME have no IF EXISTS — and cannot
//     live in a re-runnable self-bootstrap list; 011 follows the same
//     policy). Production applies them in the deployment migration step.
//   - 012/013 are idempotent CONVERGENCE migrations and run LAST: each one
//     probes columns of the tables 001/006 create (legacy_file_md5,
//     thumb_url), so it must be applied after them.
var MediaSelfBootstrapDDL = []string{
	MediaSchemaDDL,
	MediaVectorSurfacesDDL,
	MediaHNSWIndexesDDL,
	MediaTimestampsTimestamptzDDL,
	MediaAssetVersionsDDL,
	MediaClipFoldersDDL,
	MediaAssetProcessingDDL,
	MediaDropAssetFacesDDL,
	MediaLegacyFileMd5ConvergeDDL,
	MediaThumbURLContractDDL,
}
