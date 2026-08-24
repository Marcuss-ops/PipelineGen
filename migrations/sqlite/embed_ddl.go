// Package migration re-exports selected production-migration DDL as
// compile-time-embedded strings so SQLite integration tests can
// apply the canonical schema at runtime without shipping a hand-
// copied string constant.
//
// The package name is `migration` — not `sqlembed`, `ddl`, or any
// other thematic name — because Go allows exactly ONE non-test
// package declaration per directory, and the sibling test file
// `add_media_assets_origin_provider_test.go` already declares the
// external test package `migration_test` (its canonical name for
// the SQLite migrations metadata). Forcing a different non-test
// package here would trigger Go's "found packages ... and ..."
// build error. Repackaging under `migration` keeps the directory
// consistent with the existing test's framing and lets the
// external-test / non-test co-exist correctly.
//
// Why a dedicated bridge package (instead of //go:embed at the test
// file's call site): Go's //go:embed directive forbids `..` in the
// pattern path. The target migrations live at migrations/sqlite/ —
// a directory four levels above the publish_drive integration test.
// The directive must therefore sit in a Go file that lives in (or
// below) migrations/sqlite/, and the bridge is the smallest viable
// home.
//
// SSOT (godlike/06): the bytes embedded here are the bytes that
// the production migration runner applies to data/media/media.db.sqlite.
// Drift between this package and the live database becomes a
// compile-time artefact (the test binary will carry stale DDL
// until the .sql file is changed), not a silent failure mode.
//
// Fail-closed (godlike/07): every embedded statement uses
// `IF NOT EXISTS` — re-applying on a populated in-memory DB is
// a no-op, so concurrent application in the test setup is safe.
//
// Test-only by convention: production code MUST NOT depend on
// this package. The bytes are provided to mirror the canonical
// schema for tests; production paths use the canonical
// migration runner (internal/infrastructure/database).
package migration

// The `embed` import is a compile-time requirement of the
// //go:embed directives below. Use `_ "embed"` (blank) because
// the embed package's API surface (embed.FS) is NOT referenced
// anywhere in this file — the directives produce string variables
// directly, and the import is consumed by the compiler to register
// the directive's behaviour.
import _ "embed"

// ArtifactStagesDDL is the verbatim CREATE TABLE + index DDL of
// migrations/sqlite/147_artifact_stages.sql (FASE 3 spina dorsale
// durable staging + publication saga, July 2026). The artifact_outbox
// produced by Repository.InsertWithOutbox is staged into a row in
// this table, drained by publish_drive.Handler, and finalized by
// the CANONICAL finalizer. Drift between this string and the live
// migration is the most dangerous failure mode Push 3.1h tests
// against — see internal/platform/sqlite/artifact_stages/
// for the producer / consumer contracts.
//
//go:embed 147_artifact_stages.sql
var ArtifactStagesDDL string

// OutboxEventsDDL is the verbatim CREATE TABLE + index DDL of
// migrations/sqlite/092_create_outbox_events.sql (former-
// obligation migration that finally declared the canonical
// outbox_events table — the migration header comment documents
// the ad-hoc-bootstrap history). The handler's drain path is
// anchored to the same DDL outboxevents.Pool.Start would feed
// from in production; embedding this file keeps the integration
// test self-contained (no dependency on the outboxevents.NewPool
// orchestration).
//
//go:embed 092_create_outbox_events.sql
var OutboxEventsDDL string
