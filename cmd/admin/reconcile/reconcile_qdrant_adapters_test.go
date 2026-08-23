// Package reconcile — reconcile_qdrant_adapters_test.go (Card 7.2,
// July 2026): runtime regression test asserting that the
// admin reindex path (`outbox.RepairAdapter.EnqueueReindex`
// with `force=true`) DOES route through the canonical outbox
// pipeline and DOES produce the canonical
// `asset.index.requested.v1` envelope with the
// `force=true` semantics (Card 7.1).
//
// godlike/06 SSOT contract being tested:
//   - Admin reindex is the SOLE production caller of the
//     force=true envelope builder
//     (`outboxevents.BuildReindexEnvelopeV1Force`).
//   - The envelope is enqueued via
//     `outboxevents.Repository.Enqueue` with
//     `event_type = EventAssetIndexRequested`.
//   - The event_key carries the literal `:force` suffix
//     so SQLite UNIQUE(event_key) does NOT collapse the
//     force reindex with a prior non-force reindex for
//     the same (assetID, schema, source) tuple.
//   - The payload JSON carries `"force": true` so the
//     worker (IndexingHandler.Handle) reads the flag and
//     bypasses the source_version supersede gate.
//
// The test uses a real in-memory SQLite outbox_events
// table (UNIQUE on event_key) to verify the end-to-end
// invariant — not just the builder output. The adapter
// is constructed via the canonical outbox.NewRepairAdapter
// constructor (outbox package, exported in
// PR-PKG-SIZE-CMD-ADMIN-1) so the test exercises the SAME
// code path the production reconciler / backfill commands
// use, with no test-only constructor in between.
//
// This is the canonical "admin reindex routes through
// outbox" regression pin. Any future refactor that
// bypasses the canonical outbox path (e.g. a direct
// media_assets + outbox_events INSERT inside cmd/admin
// that skips BuildReindexEnvelopeV1Force) will surface
// as a CI test failure: the payload will lack
// `"force":true` and the event_key will lack the `:force`
// suffix.
//
// PR-PKG-SIZE-CMD-ADMIN-1 (July 2026): this test was
// originally in cmd/admin/reconcile_qdrant_adapters_test.go
// and referenced `outboxRepairAdapter` directly via a
// package-local struct literal. After the cmd/admin
// pkg_size refactor moved the adapter to
// cmd/admin/internal/outbox/adapter.go (so both
// `package main` admin commands AND cmd/admin/reconcile
// could import it without a cross-package dependency
// cycle), the test was updated to use the canonical
// outbox.NewRepairAdapter constructor.
package reconcile

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3" // sqlite3 driver for in-memory outbox_events

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// setupAdminOutboxTable spins up a fresh in-memory SQLite
// database with the canonical outbox_events + media_assets
// tables. The schema mirrors the production migrations
// (092_create_outbox_events.sql + the media_assets write
// target that the reconciler lightly bumps via updated_at).
//
// Each test gets a fresh DB via ":memory:" so cross-test
// state pollution is impossible. The UNIQUE(event_key)
// constraint is the dedup vector: a force reindex that
// produces an event_key WITHOUT the :force suffix would
// collapse with any prior non-force reindex for the same
// (assetID, schema, source) tuple — the test would see
// 0 rows because the force row was silently swallowed.
func setupAdminOutboxTable(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Minimal production-shape schema:
	//   - outbox_events: dedup-relevant columns + UNIQUE(event_key)
	//   - media_assets:  the row the reconciler lightly bumps (updated_at)
	_, err = db.Exec(`
		CREATE TABLE outbox_events (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type      TEXT    NOT NULL,
			aggregate_id    TEXT    NOT NULL,
			aggregate_type  TEXT    NOT NULL,
			payload_json    TEXT    NOT NULL,
			event_key       TEXT    NOT NULL UNIQUE,
			status          TEXT    NOT NULL DEFAULT 'pending',
			created_at      TEXT    NOT NULL,
			updated_at      TEXT    NOT NULL
		);
		CREATE TABLE media_assets (
			id          TEXT PRIMARY KEY,
			updated_at  TEXT NOT NULL
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL DEFAULT '',
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',);
		INSERT INTO media_assets (id, updated_at)
		    VALUES ('test-asset-1', '2026-07-01T00:00:00Z');
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	return db
}

// TestOutboxRepairAdapter_EnqueueReindex_AdminForce_RoutesThroughOutbox
// is the canonical Card 7.2 regression test. It exercises
// the full admin reindex path:
//
//  1. Construct outbox.RepairAdapter via the canonical
//     outbox.NewRepairAdapter constructor (the cmd/admin glue
//     that bypasses outbox.Dispatcher to avoid the
//     ClipsRepository dependency cycle; see the type docstring
//     in cmd/admin/internal/outbox/adapter.go for the full
//     rationale).
//  2. Call EnqueueReindex(ctx, "test-asset-1",
//     "hash-admin-test", true) — force=true (admin route,
//     Card 7.1 invariant).
//  3. Assert outbox_events has EXACTLY 1 row (the force row).
//  4. Assert event_type is the canonical EventAssetIndexRequested
//     (NOT EventAssetIndexDeleteRequested — admin reindex
//     emits UPSERT, not DELETE).
//  5. Assert payload_json contains `"force":true` (Card 7.1
//     semantic — worker reads the flag at consume time).
//  6. Assert event_key ends with the literal ":force" suffix
//     (Card 7.1 dedup contract — force survives non-force).
//
// A regression that bypasses the canonical outbox route
// (e.g. a direct INSERT that skips BuildReindexEnvelopeV1Force)
// will fail assertion (5) or (6) — the payload will lack
// force:true or the event_key will lack the :force suffix.
func TestOutboxRepairAdapter_EnqueueReindex_AdminForce_RoutesThroughOutbox(t *testing.T) {
	db := setupAdminOutboxTable(t)
	repo := outboxevents.NewRepository(db)

	adapter := outbox.NewRepairAdapter(db, repo, "media_assets_v3")

	ctx := context.Background()
	if err := adapter.EnqueueReindex(ctx, "test-asset-1", "hash-admin-test", true); err != nil {
		t.Fatalf("EnqueueReindex(force=true) failed: %v", err)
	}

	// Single-row assertion + property extraction.
	var (
		count     int
		eventType string
		payload   string
		eventKey  string
	)
	err := db.QueryRow(`
		SELECT COUNT(*), MAX(event_type), MAX(payload_json), MAX(event_key)
		FROM outbox_events
	`).Scan(&count, &eventType, &payload, &eventKey)
	if err != nil {
		t.Fatalf("SELECT from outbox_events: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected exactly 1 outbox_events row, got %d (admin reindex MUST route through outbox — Card 7.2 invariant)", count)
	}
	if eventType != outboxevents.EventAssetIndexRequested {
		t.Fatalf("event_type = %q, want %q (admin reindex emits UPSERT, not DELETE)",
			eventType, outboxevents.EventAssetIndexRequested)
	}
	if !strings.Contains(payload, `"force":true`) {
		t.Fatalf("payload_json must contain \"force\":true for admin reindex (Card 7.1 worker contract), got: %s", payload)
	}
	if !strings.HasSuffix(eventKey, ":force") {
		t.Fatalf("event_key must end with literal \":force\" suffix (Card 7.1 dedup contract), got: %q", eventKey)
	}
}

// TestOutboxRepairAdapter_EnqueueReindex_FalseDoesNotForce is the
// regression pin for the standard reconciler --apply path
// (force=false). The payload MUST NOT carry `"force":true` and
// the event_key MUST NOT carry the `:force` suffix. This pins
// the symmetry of the force=true vs force=false routes — a
// future refactor that always uses force=true would fail this
// test and surface as a CI build failure.
//
// (Note: production reconcile --apply also passes force=true
// today per the operator's --apply intent. This test pins
// the FORCED-LITERAL-CONTRACT of the force=false variant
// so a future revert to force=false for the standard path
// stays faithful to the supersede gate.)
func TestOutboxRepairAdapter_EnqueueReindex_FalseDoesNotForce(t *testing.T) {
	db := setupAdminOutboxTable(t)
	repo := outboxevents.NewRepository(db)

	adapter := outbox.NewRepairAdapter(db, repo, "media_assets_v3")

	ctx := context.Background()
	if err := adapter.EnqueueReindex(ctx, "test-asset-2", "hash-admin-test-nf", false); err != nil {
		t.Fatalf("EnqueueReindex(force=false) failed: %v", err)
	}

	var (
		payload  string
		eventKey string
	)
	err := db.QueryRow(`
		SELECT payload_json, event_key
		FROM outbox_events
		WHERE aggregate_id = 'test-asset-2'
	`).Scan(&payload, &eventKey)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}

	if strings.Contains(payload, `"force":true`) {
		t.Fatalf("force=false payload must NOT carry \"force\":true, got: %s", payload)
	}
	if strings.HasSuffix(eventKey, ":force") {
		t.Fatalf("force=false event_key must NOT end with \":force\" suffix, got: %q", eventKey)
	}
}
