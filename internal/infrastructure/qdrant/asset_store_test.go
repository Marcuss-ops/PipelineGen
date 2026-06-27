// TODO 2 (June 2026): unit tests for SQLiteAssetStore.ListAssetsForReconcile.
//
// Coverage matrix (from the QDRANT-005B + QDRANT-002 plan):
//
//	1. Empty database          → []AssetData{}, nil error
//	2. One ACTIVE asset        → exactly that row
//	3. DELETED exclusion       → excluded by default filter;
//	                              excluded by filter=[ACTIVE];
//	                              included by filter=[DELETED]
//	4. Filter=[ACTIVE]         → only ACTIVE rows
//	5. Filter=[ACTIVE, ERROR]  → both states present, others excluded
//	6. Workspace read          → asset.WorkspaceID echoes column value
//	7. Content-hash resolution → JSON content_hash wins over file_hash;
//	                              file_hash is the fallback when JSON is absent
//	8. DB error propagation    → closed connection surfaces an error
//
// Test setup: each subtest creates a fresh on-disk SQLite DB in
// t.TempDir() and runs the canonical migrations so the schema is in
// lockstep with production. The DB handle is closed via t.Cleanup so
// each subtest is fully isolated. This pattern matches the existing
// asset_store_migrations_test.go smoke test in the same package.
package qdrant

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	_ "github.com/mattn/go-sqlite3"
)

// newReconcileTestStore creates a fresh SQLiteAssetStore backed by a
// per-test on-disk DB with the canonical schema applied via migrations.
// Seed SQLs (typically INSERT statements) are run after the migrations
// complete. Each subtest gets its own DB; t.TempDir + t.Cleanup handle
// cleanup so leftover FDs don't accumulate across the test binary.
func newReconcileTestStore(t *testing.T, seedSQLs ...string) *SQLiteAssetStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "reconcile_smoke.sqlite")
	migrationsDir, err := filepath.Abs("../../../migrations/sqlite")
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	if err := storage.RunMigrationsOnDB(dbPath, nil, migrationsDir); err != nil {
		t.Fatalf("RunMigrationsOnDB: %v", err)
	}
	db, err := storage.OpenSQLiteDB(dbPath, nil)
	if err != nil {
		t.Fatalf("OpenSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range seedSQLs {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed insert failed: %v\nsql=%s", err, q)
		}
	}
	return &SQLiteAssetStore{db: db.DB}
}

// TestListAssetsForReconcile_EmptyDB verifies case 1: an empty media_assets
// table produces an empty slice (not nil) and a nil error. The reconciler
// depends on len(result)==0 to print "scanned 0 rows" rather than crashing.
func TestListAssetsForReconcile_EmptyDB(t *testing.T) {
	s := newReconcileTestStore(t)
	got, err := s.ListAssetsForReconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListAssetsForReconcile(empty DB): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty DB: got %d rows, want 0 (%+v)", len(got), got)
	}
}

// TestListAssetsForReconcile_OneActive verifies case 2: a single ACTIVE
// row is returned and surfaced with the expected id + lifecycle_state.
func TestListAssetsForReconcile_OneActive(t *testing.T) {
	s := newReconcileTestStore(t, `
		INSERT INTO media_assets (id, media_type, lifecycle_state)
		VALUES ('a-active-1', 'video', 'ACTIVE')
	`)
	got, err := s.ListAssetsForReconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListAssetsForReconcile: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 (%+v)", len(got), got)
	}
	if got[0].ID != "a-active-1" {
		t.Errorf("row id = %q, want a-active-1", got[0].ID)
	}
	if got[0].LifecycleState != "ACTIVE" {
		t.Errorf("row lifecycle_state = %q, want ACTIVE", got[0].LifecycleState)
	}
}

// TestListAssetsForReconcile_DeletedFiltering covers case 3 — the three
// lifecycle-filter shapes that exercise the SELECT's WHERE clause:
//
//   - default filter excludes DELETED;
//   - explicit [ACTIVE] filter excludes DELETED;
//   - explicit [DELETED] filter includes DELETED and excludes ACTIVE.
//
// This is the single most important selectivity case for the
// reconciler: a missing DELETED exclusion would produce spurious
// "missing in Qdrant" false-positives on every reconciled run.
func TestListAssetsForReconcile_DeletedFiltering(t *testing.T) {
	s := newReconcileTestStore(t,
		`INSERT INTO media_assets (id, media_type, lifecycle_state) VALUES ('a-active', 'video', 'ACTIVE')`,
		`INSERT INTO media_assets (id, media_type, lifecycle_state) VALUES ('a-deleted', 'video', 'DELETED')`,
	)
	ctx := context.Background()

	// 1) Default scan (no filter): DELETED must be excluded.
	gotDefault, err := s.ListAssetsForReconcile(ctx, nil)
	if err != nil {
		t.Fatalf("default scan: %v", err)
	}
	if len(gotDefault) != 1 || gotDefault[0].ID != "a-active" {
		t.Fatalf("default scan must exclude DELETED; got %+v", gotDefault)
	}

	// 2) Filter=[ACTIVE]: DELETED must be excluded.
	gotActive, err := s.ListAssetsForReconcile(ctx, []string{"ACTIVE"})
	if err != nil {
		t.Fatalf("filter=[ACTIVE]: %v", err)
	}
	if len(gotActive) != 1 || gotActive[0].ID != "a-active" {
		t.Fatalf("filter=[ACTIVE] must exclude DELETED; got %+v", gotActive)
	}

	// 3) Filter=[DELETED]: only DELETED returned.
	gotDeleted, err := s.ListAssetsForReconcile(ctx, []string{"DELETED"})
	if err != nil {
		t.Fatalf("filter=[DELETED]: %v", err)
	}
	if len(gotDeleted) != 1 || gotDeleted[0].ID != "a-deleted" {
		t.Fatalf("filter=[DELETED] must include only DELETED; got %+v", gotDeleted)
	}
}

// TestListAssetsForReconcile_FilterActiveOnly verifies case 4: a single-state
// filter restricts the scan to only matching rows, in id-ascending order.
func TestListAssetsForReconcile_FilterActiveOnly(t *testing.T) {
	s := newReconcileTestStore(t,
		`INSERT INTO media_assets (id, media_type, lifecycle_state) VALUES ('a1', 'video', 'ACTIVE')`,
		`INSERT INTO media_assets (id, media_type, lifecycle_state) VALUES ('a2', 'image', 'STAGING')`,
		`INSERT INTO media_assets (id, media_type, lifecycle_state) VALUES ('a3', 'image', 'ACTIVE')`,
	)
	got, err := s.ListAssetsForReconcile(context.Background(), []string{"ACTIVE"})
	if err != nil {
		t.Fatalf("ListAssetsForReconcile(filter=ACTIVE): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 ACTIVE (a1, a3); got %+v", len(got), got)
	}
	if got[0].ID != "a1" || got[1].ID != "a3" {
		t.Errorf("id ordering (want a1, a3 ASC): %+v", got)
	}
	for _, a := range got {
		if a.LifecycleState != "ACTIVE" {
			t.Errorf("non-ACTIVE leaked: %+v", a)
		}
	}
}

// TestListAssetsForReconcile_FilterMultipleStates covers case 5: two states
// in the IN-list. Verifies the placeholder-N concatenation logic and that
// STAGING (not in the list) is excluded.
func TestListAssetsForReconcile_FilterMultipleStates(t *testing.T) {
	s := newReconcileTestStore(t,
		`INSERT INTO media_assets (id, media_type, lifecycle_state) VALUES ('a1', 'video', 'ACTIVE')`,
		`INSERT INTO media_assets (id, media_type, lifecycle_state) VALUES ('a2', 'image', 'ERROR')`,
		`INSERT INTO media_assets (id, media_type, lifecycle_state) VALUES ('a3', 'image', 'STAGING')`,
	)
	got, err := s.ListAssetsForReconcile(context.Background(), []string{"ACTIVE", "ERROR"})
	if err != nil {
		t.Fatalf("ListAssetsForReconcile(filter=[ACTIVE,ERROR]): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 (a1 ACTIVE + a2 ERROR); got %+v", len(got), got)
	}
	wantIDs := map[string]bool{"a1": true, "a2": true}
	for _, a := range got {
		if !wantIDs[a.ID] {
			t.Errorf("unexpected id leaked: %q", a.ID)
		}
	}
}

// TestListAssetsForReconcile_WorkspaceFilter covers case 6: workspace_id
// is READ correctly from media_assets into AssetData.WorkspaceID (the
// reconciler uses that field in scanner.go::classifyPair for
// workspace_mismatch detection against the Qdrant payload).
//
// Note: ListAssetsForReconcile does NOT filter by workspace — the
// signature only accepts includeLifecycleStates. Workspace filtering is
// enforced at the matching-application layer (e.g. search_adapter.go
// adds a `workspace_id == X` clause), not the SQLite scan. This test
// verifies hydration, which is what the function actually does.
func TestListAssetsForReconcile_WorkspaceFilter(t *testing.T) {
	s := newReconcileTestStore(t,
		`INSERT INTO media_assets (id, media_type, lifecycle_state, workspace_id) VALUES ('a1', 'video', 'ACTIVE', 'ws-1')`,
		`INSERT INTO media_assets (id, media_type, lifecycle_state, workspace_id) VALUES ('a2', 'video', 'ACTIVE', 'ws-2')`,
		`INSERT INTO media_assets (id, media_type, lifecycle_state, workspace_id) VALUES ('a3', 'video', 'ACTIVE', '')`,
	)
	got, err := s.ListAssetsForReconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListAssetsForReconcile: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d, want 3; got %+v", len(got), got)
	}
	byID := map[string]string{}
	for _, a := range got {
		byID[a.ID] = a.WorkspaceID
	}
	if byID["a1"] != "ws-1" {
		t.Errorf("a1 workspace = %q, want ws-1", byID["a1"])
	}
	if byID["a2"] != "ws-2" {
		t.Errorf("a2 workspace = %q, want ws-2", byID["a2"])
	}
	if byID["a3"] != "" {
		t.Errorf("a3 workspace = %q, want empty (COALESCE default)", byID["a3"])
	}
}

// TestListAssetsForReconcile_ContentHashFromMetadata covers case 7. The
// content_hash resolution order is:
//  1. JSON_EXTRACT(metadata_json, '$.content_hash') — preferred (canonical);
//  2. file_hash column — fallback for older rows;
//  3. empty string — when neither is present.
func TestListAssetsForReconcile_ContentHashFromMetadata(t *testing.T) {
	s := newReconcileTestStore(t,
		// metadata wins over file_hash (the most important semantic).
		`INSERT INTO media_assets (id, media_type, lifecycle_state, metadata_json, file_hash)
		 VALUES ('a-meta-wins', 'video', 'ACTIVE',
		         '{"content_hash":"meta-hash-primary","x":"v"}',
		         'filehash-override')`,
		// file_hash is the fallback when metadata has no content_hash.
		`INSERT INTO media_assets (id, media_type, lifecycle_state, file_hash)
		 VALUES ('a-filehash-only', 'video', 'ACTIVE', 'fallback-hash-xyz')`,
		// metadata-only path (no file_hash column set).
		`INSERT INTO media_assets (id, media_type, lifecycle_state, metadata_json)
		 VALUES ('a-meta-only', 'image', 'ACTIVE', '{"content_hash":"meta-only-hash"}')`,
		// Neither: empty contentHash expected.
		`INSERT INTO media_assets (id, media_type, lifecycle_state)
		 VALUES ('a-empty', 'audio', 'ACTIVE')`,
	)
	got, err := s.ListAssetsForReconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListAssetsForReconcile: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d, want 4; got %+v", len(got), got)
	}
	byID := map[string]string{}
	for _, a := range got {
		byID[a.ID] = a.ContentHash
	}
	cases := []struct {
		id, want string
	}{
		{"a-meta-wins", "meta-hash-primary"},
		{"a-filehash-only", "fallback-hash-xyz"},
		{"a-meta-only", "meta-only-hash"},
		{"a-empty", ""},
	}
	for _, c := range cases {
		if got := byID[c.id]; got != c.want {
			t.Errorf("%s content_hash = %q, want %q", c.id, got, c.want)
		}
	}
}

// TestListAssetsForReconcile_DBError covers case 8: a closed DB connection
// surfaces an error rather than silently returning an empty slice. This
// is the canary for "fail-loud on infrastructure failures" — the
// reconciler MUST notice a broken SQLite scan instead of reporting
// "complete scan, 0 assets" (which would falsely pass the gate).
func TestListAssetsForReconcile_DBError(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: %v", err)
	}
	// Close before scanning so the next QueryContext returns an error.
	if err := db.Close(); err != nil {
		t.Fatalf("close :memory: %v", err)
	}
	s := &SQLiteAssetStore{db: db}
	got, err := s.ListAssetsForReconcile(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error from closed connection, got nil (and %d rows)", len(got))
	}
}
