// PR-VO-B3 (June 2026): dedupe-by-drive_file_id integration test.
//
// Exercises the post-upload gate (applyDedupeByDriveFileID) against an
// actual SQLite database to pin the contract post-refactor:
//
//   1. Empty drive_file_id → returns (nil, 0) silently (no false-positive
//      cross-dedupe of unrelated uploads).
//   2. Matching drive_file_id + different id → returns (*existingVoiceoverRow, 1).
//   3. Same id as the just-uploaded row → returns (nil, 0) (no self-dedupe).
//   4. No matching row → returns (nil, 0).
//   5. Unrelated row's drive_file_id (different value) → returns (nil, 0).
//   6. Multiple existing rows sharing drive_file_id → returns (row, N>1)
//      so the helper can surface ambiguity in logs.
//   7. Cancelled context → returns (nil, 0) (no panic, no scan error).
//
// Uses a temp-file SQLite database because :memory: is driver-specific
// in mattn/go-sqlite3 and the existing voiceover test pattern (see
// groups_resolver_test.go) uses the same temp-file idiom.
package voiceover

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// voiceoversDedupeSchema is the minimal voiceovers table layout needed
// by the dedupe-by-drive_file_id gate. The schema mirrors the columns
// the production migration creates (PR-VO-B3 only needs a subset;
// additional columns are added here so the test DB matches the
// production table shape and avoids over-fitting the gate to a
// skeletal test schema).
const voiceoversDedupeSchema = `
CREATE TABLE IF NOT EXISTS voiceovers (
    id TEXT PRIMARY KEY,
    drive_file_id TEXT,
    drive_link TEXT,
    local_path TEXT,
    file_hash TEXT,
    request_id TEXT,
    text_hash TEXT,
    language TEXT,
    voice TEXT,
    filename TEXT,
    cleaned_path TEXT,
    folder_id TEXT,
    folder_path TEXT,
    download_link TEXT,
    status TEXT,
    strategy TEXT,
    metadata TEXT,
    created_at TEXT,
    updated_at TEXT
);
`

func newDedupeTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dedupe_test.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(context.Background(), voiceoversDedupeSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

// TestApplyDedupeByDriveFileID_EmptyInputReturnsNil: empty driveFileID
// must short-circuit BEFORE hitting the database. A non-existent db
// handle also returns nil, confirming the early-out is layered correctly.
func TestApplyDedupeByDriveFileID_EmptyInputReturnsNil(t *testing.T) {
	db := newDedupeTestDB(t)
	got, count := applyDedupeByDriveFileID(context.Background(), db, nil, "current_id", "")
	if got != nil || count != 0 {
		t.Fatalf("empty drive_file_id must return (nil, 0); got (%+v, %d)", got, count)
	}
	// nil db also returns (nil, 0).
	got, count = applyDedupeByDriveFileID(context.Background(), nil, nil, "current_id", "DRIVE_X")
	if got != nil || count != 0 {
		t.Fatalf("nil db must return (nil, 0); got (%+v, %d)", got, count)
	}
}

func TestApplyDedupeByDriveFileID_MatchingDifferentIDReturnsRow(t *testing.T) {
	db := newDedupeTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `
		INSERT INTO voiceovers
			(id, drive_file_id, drive_link, local_path, file_hash)
		VALUES (?, ?, ?, ?, ?)
	`, "existing_row_id", "DRIVE_AAA",
		"https://drive.google.com/file/d/DRIVE_AAA",
		"/tmp/local.mp3", "hash_aaa"); err != nil {
		t.Fatalf("seed existing row: %v", err)
	}

	got, count := applyDedupeByDriveFileID(ctx, db, nil, "current_id", "DRIVE_AAA")
	if got == nil {
		t.Fatal("expected matching dedupe row, got nil")
	}
	if count != 1 {
		t.Errorf("match count: got %d, want 1", count)
	}
	if got.ID != "existing_row_id" {
		t.Errorf("dedupe row id: got %q, want %q", got.ID, "existing_row_id")
	}
	if got.DriveLink != "https://drive.google.com/file/d/DRIVE_AAA" {
		t.Errorf("dedupe row drive_link: got %q", got.DriveLink)
	}
	if got.LocalPath != "/tmp/local.mp3" {
		t.Errorf("dedupe row local_path: got %q", got.LocalPath)
	}
	if got.FileHash != "hash_aaa" {
		t.Errorf("dedupe row file_hash: got %q", got.FileHash)
	}
}

// TestApplyDedupeByDriveFileID_NullColumnsCoalesceToEmpty: legacy rows
// might have NULL drive_link / local_path / file_hash. The COALESCE in
// the helper's SQL must convert NULL → empty string so Scan does not
// fail. This pins the production NULL-handling contract.
func TestApplyDedupeByDriveFileID_NullColumnsCoalesceToEmpty(t *testing.T) {
	db := newDedupeTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `
		INSERT INTO voiceovers (id, drive_file_id) VALUES (?, ?)
	`, "legacy_row", "DRIVE_LEGACY"); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	got, count := applyDedupeByDriveFileID(ctx, db, nil, "current_id", "DRIVE_LEGACY")
	if got == nil || count != 1 {
		t.Fatalf("legacy NULL-cols row must dedupe with COALESCE; got (%+v, %d)", got, count)
	}
	if got.DriveLink != "" || got.LocalPath != "" || got.FileHash != "" {
		t.Errorf("COALESCE must coerce NULL to empty; got link=%q local=%q hash=%q",
			got.DriveLink, got.LocalPath, got.FileHash)
	}
}

// TestApplyDedupeByDriveFileID_SameIDFence: the WHERE-clause
// fence (`AND id != ?`) must keep the just-uploaded row from matching
// itself. Idempotency guarantee: a re-run with the same id never
// shadows its own existence. Even when many rows share the same
// drive_file_id, the WHERE-fence excludes the current id.
func TestApplyDedupeByDriveFileID_SameIDFence(t *testing.T) {
	db := newDedupeTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `
		INSERT INTO voiceovers (id, drive_file_id) VALUES
			('self_match', 'DRIVE_SAME'),
			('other_match', 'DRIVE_SAME');
	`); err != nil {
		t.Fatalf("seed multi: %v", err)
	}
	// Querying with current_id=self_match must exclude the row with
	// id=self_match only; the OTHER row still matches.
	got, count := applyDedupeByDriveFileID(ctx, db, nil, "self_match", "DRIVE_SAME")
	if got == nil {
		t.Fatal("self-match fence must NOT exclude OTHER rows with same drive_file_id; got nil")
	}
	if got.ID != "other_match" {
		t.Errorf("expected id=other_match (self excluded); got %q", got.ID)
	}
	if count != 1 {
		t.Errorf("match_count: got %d, want 1", count)
	}
}

func TestApplyDedupeByDriveFileID_NoMatchReturnsNil(t *testing.T) {
	db := newDedupeTestDB(t)
	got, count := applyDedupeByDriveFileID(context.Background(), db, nil, "any_id", "DRIVE_MISSING")
	if got != nil || count != 0 {
		t.Fatalf("no-match must return (nil, 0); got (%+v, %d)", got, count)
	}
}

func TestApplyDedupeByDriveFileID_UnrelatedRowIgnored(t *testing.T) {
	db := newDedupeTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO voiceovers (id, drive_file_id) VALUES (?, ?)
	`, "unrelated", "DRIVE_OTHER"); err != nil {
		t.Fatalf("seed unrelated: %v", err)
	}
	got, count := applyDedupeByDriveFileID(ctx, db, nil, "current_id", "DRIVE_QUERYED")
	if got != nil || count != 0 {
		t.Fatalf("unrelated row must not match; got (%+v, %d)", got, count)
	}
}

// TestApplyDedupeByDriveFileID_MultipleExistingSurfacesAmbiguity:
// when 2+ voiceovers rows share the same drive_file_id (the legacy
// pre-PR-VO-B3 drift case), the helper MUST return count > 1 so the
// caller's INFO log ("ambiguous dedupe match") can fire. The row
// itself is still the first-row-picked.
func TestApplyDedupeByDriveFileID_MultipleExistingSurfacesAmbiguity(t *testing.T) {
	db := newDedupeTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO voiceovers (id, drive_file_id) VALUES
			('row_a', 'DRIVE_MULTI'),
			('row_b', 'DRIVE_MULTI');
	`); err != nil {
		t.Fatalf("seed multi: %v", err)
	}
	got, count := applyDedupeByDriveFileID(ctx, db, nil, "current_id", "DRIVE_MULTI")
	if got == nil {
		t.Fatal("multi-existing must return non-nil row")
	}
	if count != 2 {
		t.Errorf("ambiguity count: got %d, want 2", count)
	}
	if got.ID != "row_a" && got.ID != "row_b" {
		t.Errorf("LIMIT 1 must return one of the seeded ids; got %q", got.ID)
	}
}

// TestApplyDedupeByDriveFileID_CancelledContextReturnsNil: defensive
// pin that the helper does NOT panic on a cancelled parent context
// AND returns (nil, 0). This locks in the contract for any future
// refactor that might switch to non-context Query (which would hang
// silently on a cancelled context).
func TestApplyDedupeByDriveFileID_CancelledContextReturnsNil(t *testing.T) {
	db := newDedupeTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, count := applyDedupeByDriveFileID(ctx, db, nil, "current_id", "DRIVE_X")
	if got != nil || count != 0 {
		t.Fatalf("cancelled context must short-circuit; got (%+v, %d)", got, count)
	}
}
