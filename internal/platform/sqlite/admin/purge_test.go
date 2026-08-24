// Package admin (purge_test.go) — gate tests for PurgeService.HardDeleteClip.
//
// QDRANT-purge-child-keys (June 2026, PR 4) closure:
//
//	The HardDeleteClip gate (lifecycle_state='DELETED' AND
//	qdrant_point_state='absent') is enforced in the adapter layer,
//	NOT in txmutation.HardDeleteTx (per AGENTS.md layering rule —
//	the primitive is the physics, the adapter is the gate). This
//	file pins the gate's accept/reject matrix against the production
//	PurgeService constructor (`NewPurgeService(repo, log)`) so a
//	future refactor that bypasses the gate trips the test.
//
//	The companion admission-success test (TestHardDeleteClip_Gate_Success)
//	shows the full gate-passes-then-txmutation-deletes flow in one
//	end-to-end assertion: the parent row + the 3 child rows
//	(asset_locations / asset_processing / asset_versions) are all
//	gone after HardDeleteClip commits.
//
//	The (lifecycle_state, qdrant_point_state) permutations are
//	table-driven so adding a new edge case (e.g. lifecycle_state in
//	{ready, deleted} vs DELETED) is a single-row edit.
package admin

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
)

// newPurgeTestDB builds an in-memory SQLite with the minimum schema
// that PurgeService.HardDeleteClip queries + the txmutation primitive
// deletes:
//
//	media_assets(id PK, lifecycle_state, qdrant_point_state)
//	asset_locations(asset_id)  → PR 4 explicit FK column, not PK id
//	asset_processing(asset_id) → PR 4 explicit FK column, not PK id
//	asset_versions(asset_id)   → PR 4 explicit FK column
//
// The schema is intentionally minimal — the production schema
// carries additional columns and constraints, but HardDeleteClip's
// gate query (SELECT lifecycle_state, qdrant_point_state FROM
// media_assets WHERE id = ?) and txmutation.HardDeleteTx's DELETE
// statements don't depend on them.
func newPurgeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: sqlite: %v", err)
	}
	// Force every Query/Exec/BeginTx to land on the SAME connection.
	// mattn/go-sqlite3's `:memory:` creates a private per-connection
	// database, so without this limit a query might hit a connection
	// that has never seen the test's CREATE TABLE statements. The
	// primitives_test.go::newInMemoryDB carrier has the same fix with
	// a longer rationale doc; this one mirrors it for consistency.
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
	for _, s := range schema {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("create schema: %v\nstmt: %s", err, s)
		}
	}
	return db
}

// seedAssetAndChildren inserts a media_assets row with the supplied
// lifecycle/qdrant states + 1 row in each child table keyed by the
// same id. Returns nothing — failure aborts the test via t.Fatalf.
func seedAssetAndChildren(t *testing.T, db *sql.DB, id, lifecycle, qps string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO media_assets (id, lifecycle_state, qdrant_point_state) VALUES (?, ?, ?)`,
		id, lifecycle, qps,
	); err != nil {
		t.Fatalf("insert parent (%s, %s): %v", lifecycle, qps, err)
	}
	for _, q := range []string{
		`INSERT INTO asset_locations (asset_id) VALUES (?)`,
		`INSERT INTO asset_processing (asset_id, step) VALUES (?, 'download')`,
		`INSERT INTO asset_versions (asset_id, version_number) VALUES (?, 1)`,
	} {
		if _, err := db.Exec(q, id); err != nil {
			t.Fatalf("insert child: %v", err)
		}
	}
}

// TestHardDeleteClip_Gate_Rejects covers the gate's reject path:
// the (lifecycle_state, qdrant_point_state) pair MUST be exactly
// ('DELETED', 'absent') for the gate to admit a physical delete.
// ANY deviation (including case-sensitive mismatches — the gate uses
// `==` against the canonical uppercase 'DELETED' and lowercase
// 'absent') MUST be rejected with ErrAssetNotReadyForPurge, and
// the parent row must remain in the DB.
//
// Table-driven so adding a new edge case (e.g. lifecycle='deleted'
// lowercase, the legacy alias) is a single row edit. Production
// expectation per cmd/admin/qdrant_readiness.go and PurgeService.HardDeleteClip
// gate audit: only the canonical pair passes.
func TestHardDeleteClip_Gate_Rejects(t *testing.T) {
	cases := []struct {
		name      string
		lifecycle string
		qps       string // qdrant_point_state
	}{
		{"lifecycle=ready / qdrant=absent", "ready", "absent"},
		{"lifecycle=DELETED / qdrant=present (worker not done)", "DELETED", "present"},
		{"lifecycle=staging / qdrant=absent", "staging", "absent"},
		{"lifecycle=deleted (lowercase legacy) / qdrant=absent", "deleted", "absent"},
		{"lifecycle=DELETED / qps='' (empty)", "DELETED", ""},
		{"lifecycle='' / qps='absent'", "", "absent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := newPurgeTestDB(t)
			const id = "asset-gate-test"
			seedAssetAndChildren(t, db, id, tc.lifecycle, tc.qps)

			// Build the production adapter: *assets.ClipsRepository +
			// *PurgeService. NewClipsRepository constructs the
			// canonical AssetStoreSQLite wrapper; NewPurgeService does
			// the nil-check + the canonical SetLogger-on-txmutation
			// binding.
			repo := assets.NewClipsRepository(db, zap.NewNop())
			svc, err := NewPurgeService(repo, zap.NewNop())
			if err != nil {
				t.Fatalf("NewPurgeService: %v", err)
			}

			// Call the production surface. Expect
			// ErrAssetNotReadyForPurge — exact match via
			// errors.Is so a wrapped error from fmt.Errorf("%w", ...)
			// is still recognised.
			if err := svc.HardDeleteClip(ctx, id); !errors.Is(err, ErrAssetNotReadyForPurge) {
				t.Errorf("HardDeleteClip gate must reject (%s, %s); want ErrAssetNotReadyForPurge, got %v", tc.lifecycle, tc.qps, err)
			}

			// The gate rejected, so the parent row MUST still be in
			// the DB (and the children, since txmutation was not
			// invoked). This is the "no orphan row" invariant the
			// user's review called out: a wrongly-permissive gate
			// would be silent, not noisy.
			var n int
			if err := db.QueryRow(
				`SELECT COUNT(*) FROM media_assets WHERE id = ?`, id,
			).Scan(&n); err != nil {
				t.Fatalf("post-reject parent probe: %v", err)
			}
			if n != 1 {
				t.Errorf("post-reject parent row: expected 1, got %d (gate falsely admitted (DELETED, absent) replacement — production should reject)", n)
			}
		})
	}
}

// TestHardDeleteClip_Gate_Success covers the gate's admit path:
// a row whose (lifecycle_state, qdrant_point_state) is exactly
// ('DELETED', 'absent') MUST be physically removed by
// HardDeleteClip — the gate admits, the tx is opened, txmutation
// deletes the parent + 3 children, the tx commits, and a post-state
// query confirms zero rows in all 4 tables.
//
// This test intentionally works against the SAME production
// constructor (NewPurgeService(repo, log)) that cmd/admin uses, so
// a refactor that bypasses the gate (e.g. by spawning a ParallelGo
// that calls HardDeleteTx directly) fails the assertion.
func TestHardDeleteClip_Gate_Success(t *testing.T) {
	ctx := context.Background()
	db := newPurgeTestDB(t)

	const id = "asset-gate-success"
	// Pre-condition: gate passes (DELETED + absent). Use the
	// canonical pair exactly — the gate's == is case-sensitive
	// and only matches the worker-set values.
	seedAssetAndChildren(t, db, id, "DELETED", "absent")

	repo := assets.NewClipsRepository(db, zap.NewNop())
	svc, err := NewPurgeService(repo, zap.NewNop())
	if err != nil {
		t.Fatalf("NewPurgeService: %v", err)
	}

	if err := svc.HardDeleteClip(ctx, id); err != nil {
		t.Fatalf("HardDeleteClip gate must admit (DELETED, absent); got error: %v", err)
	}

	// Post-state: zero rows for this id in all 4 tables. Verifies
	// the txmutation primitive ran after the gate admitted. If
	// the gaet admitted but txmutation failed silently, the
	// parent probe would still show 1 — a clear regression signal.
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
			t.Errorf("post-success verify %s: %v", c.table, err)
			continue
		}
		if n != 0 {
			t.Errorf("post-success %s: expected 0 rows for id=%s after gate-admit + txmutation-deletes; got %d (gate admitted but txmutation dropped a child table — PR 4 regression)", c.table, id, n)
		}
	}
}
