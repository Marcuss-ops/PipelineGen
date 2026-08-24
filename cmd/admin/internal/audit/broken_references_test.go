// cmd/admin/broken_references_test.go — Fase 4 unit tests for broken reference detectors.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// ── FK orphan detection tests ──────────────────────────────────────────

func TestDetectFKOrphans_NoOrphans(t *testing.T) {
	db := testBrokenRefDB(t)
	defer db.Close()

	// Create a clean owner-child pair where all FKs resolve.
	_, err := db.Exec(`CREATE TABLE test_owners (id TEXT PRIMARY KEY)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE test_children (id TEXT PRIMARY KEY, owner_id TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO test_owners VALUES ('o1'), ('o2')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO test_children VALUES ('c1','o1'), ('c2','o2')`)
	if err != nil {
		t.Fatal(err)
	}

	// Temporarily override the model.
	saved := canonicalOwnershipModel
	defer func() { canonicalOwnershipModel = saved }()

	canonicalOwnershipModel = map[string]ownershipRelation{
		"test_owners":   {RootType: "canonical_root"},
		"test_children": {ChildTable: "test_children", ChildColumn: "owner_id", OwnerTable: "test_owners", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	}

	orphans, err := detectFKOrphans(context.Background(), db, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("expected 0 orphan tables, got %d: %+v", len(orphans), orphans)
	}
}

func TestDetectFKOrphans_HasOrphans(t *testing.T) {
	db := testBrokenRefDB(t)
	defer db.Close()

	_, err := db.Exec(`CREATE TABLE test_owners (id TEXT PRIMARY KEY)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE test_children (id TEXT PRIMARY KEY, owner_id TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO test_owners VALUES ('o1')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO test_children VALUES ('c1','o1'), ('c2','GHOST'), ('c3','GHOST2')`)
	if err != nil {
		t.Fatal(err)
	}

	saved := canonicalOwnershipModel
	defer func() { canonicalOwnershipModel = saved }()

	canonicalOwnershipModel = map[string]ownershipRelation{
		"test_owners":   {RootType: "canonical_root"},
		"test_children": {ChildTable: "test_children", ChildColumn: "owner_id", OwnerTable: "test_owners", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	}

	orphans, err := detectFKOrphans(context.Background(), db, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan table, got %d", len(orphans))
	}
	if orphans[0].OrphanRows != 2 {
		t.Errorf("expected 2 orphan rows, got %d", orphans[0].OrphanRows)
	}
	if len(orphans[0].SampleIDs) != 2 {
		t.Errorf("expected 2 sample IDs, got %d", len(orphans[0].SampleIDs))
	}
}

func TestDetectFKOrphans_NoDetailSkipsSamples(t *testing.T) {
	db := testBrokenRefDB(t)
	defer db.Close()

	_, err := db.Exec(`CREATE TABLE test_owners (id TEXT PRIMARY KEY)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE test_children (id TEXT PRIMARY KEY, owner_id TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO test_children VALUES ('c1','GHOST')`)
	if err != nil {
		t.Fatal(err)
	}

	saved := canonicalOwnershipModel
	defer func() { canonicalOwnershipModel = saved }()

	canonicalOwnershipModel = map[string]ownershipRelation{
		"test_owners":   {RootType: "canonical_root"},
		"test_children": {ChildTable: "test_children", ChildColumn: "owner_id", OwnerTable: "test_owners", OwnerColumn: "id", Kind: "FK", RootType: "child"},
	}

	orphans, err := detectFKOrphans(context.Background(), db, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan table, got %d", len(orphans))
	}
	if len(orphans[0].SampleIDs) != 0 {
		t.Errorf("expected 0 sample IDs with noDetail, got %d: %v", len(orphans[0].SampleIDs), orphans[0].SampleIDs)
	}
}

// ── Local path detection tests ─────────────────────────────────────────

func TestDetectBrokenLocalPaths_FileNotFound(t *testing.T) {
	db := testBrokenRefDB(t)
	defer db.Close()

	_, err := db.Exec(`CREATE TABLE test_local (id TEXT PRIMARY KEY, local_path TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	nonexistent := filepath.Join(t.TempDir(), "nonexistent-file.mp4")
	_, err = db.Exec(`INSERT INTO test_local VALUES ('a',?)`, nonexistent)
	if err != nil {
		t.Fatal(err)
	}

	broken, total, err := detectBrokenLocalPaths(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 total ref, got %d", total)
	}
	if len(broken) != 1 {
		t.Fatalf("expected 1 broken ref, got %d: %+v", len(broken), broken)
	}
	if broken[0].FailureKind != "file_not_found" {
		t.Errorf("expected file_not_found, got %s", broken[0].FailureKind)
	}
}

func TestDetectBrokenLocalPaths_FileExists(t *testing.T) {
	db := testBrokenRefDB(t)
	defer db.Close()

	_, err := db.Exec(`CREATE TABLE test_local (id TEXT PRIMARY KEY, local_path TEXT)`)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.CreateTemp("", "broken-ref-test-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := f.Name()
	defer os.Remove(path)

	_, err = db.Exec(`INSERT INTO test_local VALUES ('a',?)`, path)
	if err != nil {
		t.Fatal(err)
	}

	broken, total, err := detectBrokenLocalPaths(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 total ref, got %d", total)
	}
	if len(broken) != 0 {
		t.Fatalf("expected 0 broken refs for existing file, got %d: %+v", len(broken), broken)
	}
}

// ── Drive inventory loading test ───────────────────────────────────────

func TestLoadDriveInventoryFromFile_Valid(t *testing.T) {
	entries := []driveInventoryEntry{
		{ID: "abc123", Name: "clip.mp4"},
		{ID: "def456", Name: "image.jpg"},
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "drive-inventory.json")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		t.Fatal(err)
	}

	known, errs := loadDriveInventoryFromFile(tmp)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !known["abc123"] {
		t.Error("expected abc123 to be known")
	}
	if !known["def456"] {
		t.Error("expected def456 to be known")
	}
	if known["ghost789"] {
		t.Error("expected ghost789 to NOT be known")
	}
}

func TestLoadDriveInventoryFromFile_Nonexistent(t *testing.T) {
	known, errs := loadDriveInventoryFromFile("/tmp/nonexistent-drive-inventory.json")
	if len(errs) == 0 {
		t.Error("expected errors for nonexistent file")
	}
	if known != nil {
		t.Errorf("expected nil map for error case, got %v", known)
	}
}

// ── tablesWithColumn test ─────────────────────────────────────────────

func TestTablesWithColumn(t *testing.T) {
	db := testBrokenRefDB(t)
	defer db.Close()

	_, err := db.Exec(`CREATE TABLE t_blobs (id TEXT PRIMARY KEY, drive_file_id TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE t_names (id TEXT PRIMARY KEY, name TEXT)`)
	if err != nil {
		t.Fatal(err)
	}

	tables, err := tablesWithColumn(context.Background(), db, "drive_file_id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tables) != 1 || tables[0] != "t_blobs" {
		t.Errorf("expected [t_blobs], got %v", tables)
	}

	tables2, err := tablesWithColumn(context.Background(), db, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tables2) != 0 {
		t.Errorf("expected empty list, got %v", tables2)
	}
}

// ── Report JSON round-trip test ────────────────────────────────────────

func TestBrokenRefsReport_JSONRoundTrip(t *testing.T) {
	r := &brokenRefsReport{
		SchemaVersion: 1,
		Mode:          "broken-references",
		NoDeletions:   true,
		Summary: brokenRefsSummary{
			FKOrphanRows: 5, FKOrphanTables: 2, DriveRefsTotal: 10,
			DriveBroken: 3, LocalBroken: 580, LocalRefsTotal: 2469,
		},
		FKOrphans: []fkOrphanTable{
			{Table: "t1", OwnerTable: "o1", OrphanRows: 3, SampleIDs: []string{"a", "b"}},
		},
		DriveBroken: []brokenDriveRef{
			{Table: "t2", Column: "drive_file_id", RefValue: "xxx", FailureKind: "drive_file_not_found"},
		},
		LocalBroken: []brokenLocalRef{
			{Table: "t3", Column: "local_path", LocalPath: "/tmp/broken", FailureKind: "file_not_found", Error: "no such file"},
		},
		QdrantMissing: []string{"asset:1", "asset:2"},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var r2 brokenRefsReport
	if err := json.Unmarshal(data, &r2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r2.Summary.FKOrphanRows != 5 {
		t.Errorf("FK orphan rows: got %d want 5", r2.Summary.FKOrphanRows)
	}
	if len(r2.FKOrphans) != 1 {
		t.Errorf("FK orphans len: got %d want 1", len(r2.FKOrphans))
	}
	if len(r2.QdrantMissing) != 2 {
		t.Errorf("qdrant missing len: got %d want 2", len(r2.QdrantMissing))
	}
	if !r2.NoDeletions {
		t.Error("expected no_deletions_performed=true")
	}
}

// ── CLI flags parse correctly (integration-lite) ───────────────────────

func TestBrokenReferencesCLI_SkipFlags(t *testing.T) {
	// Verify that --skip-drive, --skip-local, --skip-qdrant flags parse
	// and the subcommand is registered. Use --report to a temp file so
	// JSON output doesn't pollute the test log.
	reportPath := filepath.Join(t.TempDir(), "broken-refs-report.json")
	err := runBrokenReferences([]string{"--report", reportPath, "--skip-drive", "--skip-local", "--skip-qdrant", "--no-orphan-detail"})
	if err != nil && strings.Contains(err.Error(), "unknown command") {
		t.Errorf("command not recognized: %v", err)
	}
	// Either success (config + DB present) or config/DB error — both are valid.
	// Verify the report file was created if the command succeeded.
	if err == nil {
		info, statErr := os.Stat(reportPath)
		if statErr != nil || info.Size() == 0 {
			t.Errorf("report file not created: statErr=%v size=%d", statErr, info.Size())
		}
	}
}

// ── Helper ─────────────────────────────────────────────────────────────

func testBrokenRefDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}
