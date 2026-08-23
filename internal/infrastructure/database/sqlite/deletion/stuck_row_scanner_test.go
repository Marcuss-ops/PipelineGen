// Package deletion — stuck_row_scanner_test.go (Blocco 3.2 commit 2/2, June 2026)
//
// Adapter tests for production stuck_row_scanner.go. Uses an
// in-memory SQLite + the same minimal-media_assets fixture
// pattern as the test suites committed in 42a2e5aa (Blocco 3.2
// commit 1/2) so the adapter exercises the actual SQL against a
// realistic media_assets schema.
package deletion

import (
	"database/sql"
	"strconv"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// memoryDB opens a fresh :memory: SQLite. Each test that calls
// it gets an isolated DB.
func memoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite3 memory: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// minimalMediaAssetsFixture mirrors the production schema's
// columns the scanner touches. The production schema has
// additional columns the scanner does NOT consult, so this
// minimal shape is sufficient to exercise the SQL.
func minimalMediaAssetsFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS media_assets (
			id              TEXT PRIMARY KEY,
			lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
			created_at      TEXT NOT NULL DEFAULT '',
			updated_at      TEXT NOT NULL DEFAULT '',
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
    status TEXT NOT NULL DEFAULT '')
	`)
	if err != nil {
		t.Fatalf("create media_assets fixture: %v", err)
	}
}

// seedRow inserts a media_assets row with the given state +
// updated_at.
func seedRow(t *testing.T, db *sql.DB, id, state, updatedAt string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO media_assets (id, lifecycle_state, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		id, state, updatedAt, updatedAt)
	if err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// TestScanner_HappyPathReturnsStuckRowsASCOrderedByUpdatedAt pins
// the canonical "rows past threshold, ASC by updated_at, capped at
// batchSize" behaviour with mixed states.
func TestScanner_HappyPathReturnsStuckRowsASCOrderedByUpdatedAt(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	now := time.Now().UTC()

	// Threshold = 60min. Rows older than 60min are stuck.
	veryOld := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	oldish := now.Add(-90 * time.Minute).Format(time.RFC3339Nano)
	recent := now.Add(-10 * time.Minute).Format(time.RFC3339Nano) // not stuck

	seedRow(t, db, "a-drive-old", "DELETE_REQUESTED", veryOld)
	seedRow(t, db, "a-drive-mid", "DRIVE_DELETE_PENDING", oldish)
	seedRow(t, db, "a-index-old", "INDEX_DELETE_PENDING", veryOld)
	seedRow(t, db, "a-recent-drive", "DELETE_REQUESTED", recent)
	seedRow(t, db, "a-active-notstuck", "ACTIVE", veryOld)   // not deletion chain → skipped
	seedRow(t, db, "a-deleted-terminal", "DELETED", veryOld) // terminal → skipped

	scanner := NewScanner(db, 100)
	got, err := scanner.ListStuckRows(now, 60*time.Minute)
	if err != nil {
		t.Fatalf("ListStuckRows: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 stuck rows (DELETE_REQUESTED + DRIVE_DELETE_PENDING + INDEX_DELETE_PENDING past threshold); got %d: %+v", len(got), got)
	}

	// Expect ASC order: veryOld rows come first, then oldish.
	wantOrder := []string{
		"a-drive-old", "a-index-old", // both veryOld (same RFC3339Nano formatting); tied order may depend on insertion
		"a-drive-mid", // oldish
	}
	if got[0].AssetID != wantOrder[0] || got[1].AssetID != wantOrder[1] {
		t.Errorf("first two slots: want [%s %s] got [%s %s]",
			wantOrder[0], wantOrder[1], got[0].AssetID, got[1].AssetID)
	}
	if got[2].AssetID != wantOrder[2] {
		t.Errorf("third slot: want %s got %s", wantOrder[2], got[2].AssetID)
	}

	// Each row's State field carries the literal DB column value.
	wantState := map[string]string{
		"a-drive-old": "DELETE_REQUESTED",
		"a-index-old": "INDEX_DELETE_PENDING",
		"a-drive-mid": "DRIVE_DELETE_PENDING",
	}
	for _, r := range got {
		if wantState[r.AssetID] != r.State {
			t.Errorf("%s: State want %s got %s", r.AssetID, wantState[r.AssetID], r.State)
		}
	}
}

// TestScanner_HonorsBatchLimit pins the LIMIT clause behavior.
func TestScanner_HonorsBatchLimit(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	now := time.Now().UTC()
	veryOld := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)

	// 5 stuck rows + batch limit = 3.
	for i := 0; i < 5; i++ {
		seedRow(t, db, fmtID(i), "DELETE_REQUESTED", veryOld)
	}
	scanner := NewScanner(db, 3)
	got, err := scanner.ListStuckRows(now, 60*time.Minute)
	if err != nil {
		t.Fatalf("ListStuckRows: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("batch limit: want 3 rows returned; got %d", len(got))
	}
}

// TestScanner_ThresholdBoundary covers the EXCLUSIVE boundary:
// a row EXACTLY at now-threshold is NOT considered stuck (the
// query is `updated_at < threshold`, not `<=`).
func TestScanner_ThresholdBoundary(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	const threshold = 60 * time.Minute

	// At the EXACT threshold boundary: not picked (strict <).
	atBoundary := now.Add(-threshold).Format(time.RFC3339Nano)
	// Just BEFORE: picked (older than threshold).
	beforeBoundary := now.Add(-threshold - 1*time.Second).Format(time.RFC3339Nano)
	// Just AFTER: not picked (more recent than threshold).
	afterBoundary := now.Add(-threshold + 1*time.Second).Format(time.RFC3339Nano)

	seedRow(t, db, "at-boundary", "DELETE_REQUESTED", atBoundary)
	seedRow(t, db, "before-boundary", "DELETE_REQUESTED", beforeBoundary)
	seedRow(t, db, "after-boundary", "DELETE_REQUESTED", afterBoundary)

	scanner := NewScanner(db, 100)
	got, err := scanner.ListStuckRows(now, threshold)
	if err != nil {
		t.Fatalf("ListStuckRows: %v", err)
	}
	// Only before-boundary should be picked.
	if len(got) != 1 {
		t.Fatalf("want 1 row (before-boundary only); got %d: %+v", len(got), got)
	}
	if got[0].AssetID != "before-boundary" {
		t.Errorf("want before-boundary; got %s", got[0].AssetID)
	}
}

// TestScanner_ReconciliationSatisfiedIsEmptyRows confirms:
// once the row terminal-stamps to DELETED (a hypothetical
// post-reconciler state), the scan no longer surfaces it.
func TestScanner_ReconciliationSatisfiedIsEmptyRows(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	now := time.Now().UTC()
	veryOld := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)

	// All rows in non-deletion-chain states (post-chain; canonical
	// DELETED / readmit-back-into-ACTIVE case).
	seedRow(t, db, "a-deleted", "DELETED", veryOld)
	seedRow(t, db, "a-active", "ACTIVE", veryOld)
	seedRow(t, db, "a-error", "ERROR", veryOld)

	scanner := NewScanner(db, 100)
	got, err := scanner.ListStuckRows(now, 60*time.Minute)
	if err != nil {
		t.Fatalf("ListStuckRows: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("post-chain rows must NOT surface; got %d: %+v", len(got), got)
	}
}

// TestScanner_UpdatedAtZeroIsErrorOnMalformedRow pins a fail-loud
// behaviour for malformed updated_at values. The production
// dispatcher (commit 42a2e5aa) stamps updated_at on every
// lifecycle_state flip, so a zero parse indicates schema drift
// or a hand-edited row — both warrant fail-close.
func TestScanner_UpdatedAtZeroIsErrorOnMalformedRow(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	now := time.Now().UTC()

	// Hand-edit: write a row with empty updated_at (the table
	// default is the empty string, which is what a manual
	// INSERT-with-NULL or a test fixture gets).
	_, err := db.Exec(`INSERT INTO media_assets (id, lifecycle_state, created_at, updated_at) VALUES (?, ?, ?, '')`,
		"a-malformed", "DELETE_REQUESTED", "2020-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("seed malformed row: %v", err)
	}

	scanner := NewScanner(db, 100)
	_, err = scanner.ListStuckRows(now, 60*time.Minute)
	if err == nil {
		t.Fatal("zero-timestamp row must surface as an error (fail-loud); got nil")
	}
}

// fmtID is a small string-format helper for the batch-test seeds.
func fmtID(i int) string {
	return "a-batch-" + strconv.Itoa(i)
}
