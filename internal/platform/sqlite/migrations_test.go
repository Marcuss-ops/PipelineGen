// Package storage — slim migrations_test.go.
//
// Phase 4 of the Largest-Files plan split the original monolithic
// migrations_test.go (1325 LOC, ~30 subtests across 9+ migration
// scenarios) into a per-migration sibling-file set. This file remains
// only as an index-of-tests entry-point so a developer can grep for
// the migration number and find the corresponding test file.
//
// Apply + idempotency + integrity + essentials + journal-mode baseline
// (originally the first 6 subtests of TestMigrations_Smoke) lives in:
//
//	migrations_smoke_test.go   ← TestMigrations_Smoke_Baseline
//
// Per-migration scenario tests:
//
//	migrations_099_test.go     ← Qdrant asset columns (migration 099)
//	migrations_152_test.go     ← Canonical metadata columns (migration 152)
//	migrations_153_test.go     ← asset_artifacts table + FK + indexes (migration 153)
//	migrations_154_test.go     ← script_localizations UNIQUE shape (migration 154)
//	migrations_155_test.go     ← translation fingerprint partial UNIQUE idx (migration 155)
//	migrations_156_test.go     ← asset_text_tracks source-track FK + text_hash (migration 156)
//	migrations_157_test.go     ← asset_state column + idx + round-trip (migration 157)
//	migrations_158_test.go     ← rights-extension columns (migration 158)
//
// The shared fixture layer (constants, applyFreshSmokeDB,
// openSmokeDB, contains, itoa) lives in:
//
//	migrations_helpers_test.go
//
// Pre-existing precedent (unanimously approved) for the per-migration
// split pattern:
//
//	migrations_092_093_test.go ← TestMigrations_092_093_FreshDB
//
// All files share `package storage`; mustReadIndexNames is supplied
// by migrations_092_093_test.go (line ~200). New migration scenarios
// MUST use the applyFreshSmokeDB helper rather than re-implementing
// the apply path.
//
// godlike/06 SSOT: each migration scenario test exercises the
// media-domain tables (jobs, scripts, media_assets, outbox_events)
// via scope = "primary". Other scopes are covered by per-scope boot
// tests in boot_test.go.
package sqlite

// Intentionally empty. All prior bodies now live across:
//   - migrations_smoke_test.go    (baseline apply + integrity + FK + essentials + WAL)
//   - migrations_099_test.go      (Qdrant asset columns)
//   - migrations_152_test.go      (canonical metadata columns)
//   - migrations_153_test.go      (asset_artifacts table + FK + indexes)
//   - migrations_154_test.go      (script_localizations UNIQUE shape)
//   - migrations_155_test.go      (translation fingerprint + partial UNIQUE idx)
//   - migrations_156_test.go      (asset_text_tracks source-track FK + text_hash)
//   - migrations_157_test.go      (asset_state column + idx + round-trip)
//   - migrations_158_test.go      (rights-extension columns)
//   - migrations_183_test.go      (lifecycle shadow reconciliation)
//   - migrations_helpers_test.go  (shared apply + introspection helpers)
//
// scanColumnNames is the small helper used by every per-migration
// scenario file to read PRAGMA table_info column names into a set; it
// is also exported from migrations_helpers_test.go so that future
// migration tests can reuse it without re-implementing the loop.
//
// SPDX-style alert: the slim file intentionally does NOT import
// testing — `go test` discovers the top-level TestMigrations_*xxx_*
// functions from the sibling files, not from this slim anchor.
