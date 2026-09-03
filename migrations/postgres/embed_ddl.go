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
