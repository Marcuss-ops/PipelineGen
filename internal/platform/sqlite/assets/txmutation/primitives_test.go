// Package txmutation (primitives_test.go) — unit tests for HardDeleteTx's
// canonical deletion sequence. Companion tests for the lifecycle gate
// (lifecycle_state=DELETED AND qdrant_point_state=absent) live in
// internal/platform/sqlite/admin/purge_test.go — they
// intentionally sit at the adapter layer because the gate is enforced
// there, not in the primitive.
//
// QDRANT-purge-child-keys (June 2026, PR 4) closure:
//
//	The previous HardDeleteTx loop ran `DELETE FROM <table> WHERE id = ?`
//	for every child. That was wrong for tables whose FK column to
//	media_assets is `asset_id` (NOT the PK `id`). The fix introduced
//	the explicit `childDelete{table, column}` map. These tests pin the
//	new behaviour against the production columns (asset_locations +
//	asset_processing + asset_versions all use asset_id).
//
//	asset_dedupe (legacy fossil) was dropped from the deletion map
//	intentionally — see the long-form rationale on
//	hardDeleteChildTables in primitives.go. These tests do not
//	recreate it.
package txmutation

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newInMemoryDB returns an in-memory SQLite with the canonical 4-table
// purge schema pre-loaded. Each call opens a FRESH database, so
// concurrent tests do not see each other's rows.
//
// Why SetMaxOpenConns(1)?
//
// mattn/go-sqlite3's `:memory:` creates a private per-connection
// database — every connection from the pool sees its OWN
// in-memory store. The standard `sql.DB` connection pool may hand
// out a different connection to each Exec/Query call, so a query
// issued after a sequence of writes can land on a connection that
// has never seen the test's CREATE TABLE statements. Limit the
// pool to ONE connection so every statement in a test sees the
// same in-memory DB. Detected by PR 4 rollback test (June 2026):
// the DROP TABLE outside the tx landed on conn B with the schema,
// but the verify queries after tx.Rollback hit conn C with NO
// schema → "no such table: media_assets".
//
// Schema:
//
//	media_assets(id TEXT PK, lifecycle_state TEXT, qdrant_point_state TEXT)
//	asset_locations(id INTEGER PK, asset_id TEXT)
//	asset_processing(id INTEGER PK, asset_id TEXT, step TEXT)
//	asset_versions(id INTEGER PK, asset_id TEXT, version_number INTEGER)
//
// Note: the production schema for these tables carries additional
// columns and constraints (CHECKs, FK references, indexes). Stripping
// them keeps the test fixture concise and does not change the
// HardDeleteTx behaviour because the primitive does not touch the
// extra columns. A future test that exercises a CHECK violation
// (e.g. for the rollback path) would need to add the production
// CREATE TABLE statements back.
func newInMemoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: sqlite: %v", err)
	}
	// Force every Query/Exec/BeginTx in the test to land on the SAME
	// connection. See the doc comment above for the rationale.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	schema := []string{
		`CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			lifecycle_state TEXT NOT NULL DEFAULT 'ready',
			qdrant_point_state TEXT NOT NULL DEFAULT 'absent',
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
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
    status TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE asset_locations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL
		)`,
		`CREATE TABLE asset_processing (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL,
			step TEXT NOT NULL DEFAULT 'download'
		)`,
		`CREATE TABLE asset_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL,
			version_number INTEGER NOT NULL DEFAULT 1
		)`,
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v\nstmt: %s", err, stmt)
		}
	}
	return db
}

// seedAssetWithChildren inserts 1 media_assets row + 1 row in each of
// the 3 child tables (asset_locations / asset_processing / asset_versions)
// with a single asset_id. Returns the id used so the test can re-query
// after the call to HardDeleteTx.
func seedAssetWithChildren(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	inserts := []string{
		`INSERT INTO media_assets (id) VALUES (?)`,
		`INSERT INTO asset_locations (asset_id) VALUES (?)`,
		`INSERT INTO asset_processing (asset_id, step) VALUES (?, 'download')`,
		`INSERT INTO asset_versions (asset_id, version_number) VALUES (?, 1)`,
	}
	args := [][]any{
		{id},
		{id},
		{id},
		{id},
	}
	for i, q := range inserts {
		if _, err := db.Exec(q, args[i]...); err != nil {
			t.Fatalf("seed row %d for id=%s: %v", i, id, err)
		}
	}
}

// TestHardDeleteTx_DeletesChildrenAndParent covers the canonical
// happy-path: a tx-bound HardDeleteTx removes the parent + every
// child in one commit. Post-commit queries confirm zero rows remain
// for the canonical id in all 4 tables.
func TestHardDeleteTx_DeletesChildrenAndParent(t *testing.T) {
	ctx := context.Background()
	db := newInMemoryDB(t)

	const id = "asset-success"
	seedAssetWithChildren(t, db, id)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op if commit fires

	if err := HardDeleteTx(ctx, tx, id); err != nil {
		t.Fatalf("HardDeleteTx returned error on the success path: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Post-commit verification: every table must hold 0 rows for the
	// seeded id. Loop ordering matches the deletion order so a failure
	// surfaces the FIRST broken step.
	want := []struct {
		table, query string
	}{
		{"media_assets", `SELECT COUNT(*) FROM media_assets WHERE id = ?`},
		{"asset_locations", `SELECT COUNT(*) FROM asset_locations WHERE asset_id = ?`},
		{"asset_processing", `SELECT COUNT(*) FROM asset_processing WHERE asset_id = ?`},
		{"asset_versions", `SELECT COUNT(*) FROM asset_versions WHERE asset_id = ?`},
	}
	for _, c := range want {
		var n int
		if err := db.QueryRow(c.query, id).Scan(&n); err != nil {
			t.Errorf("verify %s: %v", c.table, err)
			continue
		}
		if n != 0 {
			t.Errorf("table %s: expected 0 rows for id=%s after HardDeleteTx; got %d (PR 4 regression: childDelete map's column for this table is wrong)", c.table, id, n)
		}
	}
}

// TestHardDeleteTx_IdempotentOnNonExistentID covers the idempotency
// contract: HardDeleteTx on an id that doesn't exist in media_assets
// MUST return nil. This is the precondition for "re-run scripts after
// partial failure are safe" — see admin/purge.go doc on
// HardDeleteClip's ErrAssetNotReadyForPurge + the no-op guarantees.
func TestHardDeleteTx_IdempotentOnNonExistentID(t *testing.T) {
	ctx := context.Background()
	db := newInMemoryDB(t)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := HardDeleteTx(ctx, tx, "asset-does-not-exist"); err != nil {
		t.Errorf("HardDeleteTx on non-existent id must return nil (idempotent no-op); got error: %v", err)
	}
}

// TestHardDeleteTx_RollsBackOnChildError covers the failure
// contract: an error during the deletion loop MUST propagate out of
// HardDeleteTx, the tx-bound parent row MUST remain untouched, and
// the caller-deferred tx.Rollback() MUST restore any tx-bound
// (i.e. mid-loop-succeeded) child deletes.
//
// Strategy: drop one of the child tables OUTSIDE the tx (so SQLite's
// DDL auto-commit trap is not a factor — DDL inside a tx auto-commits
// in some SQLite modes, which would defeat the rollback assertion).
// The very first loop iteration (asset_locations, where `column =
// asset_id` is now correctly inlined) tries to DELETE FROM a
// non-existent table → `no such table: asset_locations` wraps up
// into HardDeleteTx's `delete %s: %w` return chain. The parent
// probe step succeeds (media_assets has the row), then the loop
// errors on the first child → no tx-bound DML has been committed.
// Caller's tx.Rollback() is then a literal no-op confirmed by the
// post-state verification (parent still present, surviving children
// still present).
func TestHardDeleteTx_RollsBackOnChildError(t *testing.T) {
	ctx := context.Background()
	db := newInMemoryDB(t)

	const id = "asset-rollback-test"
	seedAssetWithChildren(t, db, id)

	// Drop asset_locations OUTSIDE the tx — forces the first loop
	// iteration to fail with `no such table`. Doing this OUTSIDE
	// the tx sidesteps SQLite's DDL-in-tx auto-commit quirk
	// (DDL inside a tx auto-commits in some SQLite modes and
	// would leave the test in an ambiguous state for the
	// rollback assertion).
	if _, err := db.Exec(`DROP TABLE asset_locations`); err != nil {
		t.Fatalf("drop asset_locations: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := HardDeleteTx(ctx, tx, id); err == nil {
		t.Fatalf("HardDeleteTx must return error when a child table is missing (no such table); got nil — the loop should fail on the first iteration (asset_locations) BEFORE the parent probe advances to the parent DELETE step")
	}

	// Explicitly rollback the transaction here so we don't hold database locks
	// when querying the database in the verification loop below.
	_ = tx.Rollback()

	// After HardDeleteTx returned an error and the tx rollback ran, the post-state must reflect: parent present,
	// surviving children present (asset_processing + asset_versions).
	//
	// The asset_processing row's tx-bound delete was attempted
	// (after asset_locations failed, the loop returned at the FIRST
	// iteration error — so asset_processing was NOT touched by the
	// tx). Likewise asset_versions. Rolling back the tx discards any
	// partial state — but since the loop errored immediately, the
	// rollback is effectively a no-op. The assertion is the
	// observable end-state: parent + 2 surviving children present.
	want := []struct {
		table, query string
		wantN        int
	}{
		{"media_assets", `SELECT COUNT(*) FROM media_assets WHERE id = ?`, 1},
		{"asset_processing", `SELECT COUNT(*) FROM asset_processing WHERE asset_id = ?`, 1},
		{"asset_versions", `SELECT COUNT(*) FROM asset_versions WHERE asset_id = ?`, 1},
	}
	for _, c := range want {
		var n int
		if err := db.QueryRow(c.query, id).Scan(&n); err != nil {
			t.Errorf("post-rollback verify %s: %v", c.table, err)
			continue
		}
		if n != c.wantN {
			t.Errorf("post-rollback %s: expected %d rows for id=%s (HardDeleteTx must NOT have deleted this row); got %d", c.table, c.wantN, id, n)
		}
	}
}
