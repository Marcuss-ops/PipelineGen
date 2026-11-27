package sqlite

// GetMigrationStatusForPlane is what `admin db migrate` prints and what its
// post-apply check trusts. It must report the work the runner will actually do:
// out-of-scope files never get a ledger row on this plane, and the versions
// satisfied by an applied consolidated baseline are not pending either.
//
// Regression source: the unscoped GetMigrationStatus reported 255 pending
// migrations on a fully migrated live primary database, because every
// observability/cache-only file and every baseline-covered version looked
// "missing".

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestGetMigrationStatusForPlaneIgnoresOutOfScopeFiles(t *testing.T) {
	dir := writeLedgerFixture(t, map[string]string{
		"001_shared.sql": "-- database: all\nCREATE TABLE shared (id INTEGER PRIMARY KEY);\n",
	})
	dbPath := filepath.Join(t.TempDir(), "status-scope.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, dir, "cache"); err != nil {
		t.Fatalf("RunMigrationsOnDB: %v", err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// A cache-only migration added after the database was migrated must be
	// pending on cache and invisible to the primary scope.
	if err := os.WriteFile(filepath.Join(dir, "002_cache_only.sql"),
		[]byte("-- database: cache\nCREATE TABLE cache_only (id INTEGER PRIMARY KEY);\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cacheReport, err := GetMigrationStatusForPlane(db, dir, "cache")
	if err != nil {
		t.Fatalf("cache status: %v", err)
	}
	if cacheReport.PendingN != 1 || cacheReport.Pending[0].Version != 2 {
		t.Fatalf("cache status pending = %+v, want exactly version 2", cacheReport.Pending)
	}

	primaryReport, err := GetMigrationStatusForPlane(db, dir, "primary")
	if err != nil {
		t.Fatalf("primary status: %v", err)
	}
	if primaryReport.PendingN != 0 {
		t.Fatalf("primary must not report the cache-only file as pending, got %+v", primaryReport.Pending)
	}
	if len(primaryReport.Applied) != 1 || primaryReport.Applied[0].Version != 1 {
		t.Fatalf("primary applied = %+v, want exactly version 1", primaryReport.Applied)
	}
}

func TestGetMigrationStatusForPlaneTreatsBaselineCoveredVersionsAsSatisfied(t *testing.T) {
	dir := writeLedgerFixture(t, map[string]string{
		"000_baseline_268.sql": "-- 000_baseline_268.sql\n-- database: all\n-- baseline-covers: 1\n\nCREATE TABLE IF NOT EXISTS legacy_thing (id INTEGER PRIMARY KEY);\n",
		"001_shared.sql":       "-- database: all\nCREATE TABLE shared (id INTEGER PRIMARY KEY);\n",
	})
	dbPath := filepath.Join(t.TempDir(), "status-baseline.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, dir, "cache"); err != nil {
		t.Fatalf("RunMigrationsOnDB: %v", err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	report, err := GetMigrationStatusForPlane(db, dir, "cache")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if report.PendingN != 0 {
		t.Fatalf("version 1 is covered by the applied baseline and must not be pending, got %+v", report.Pending)
	}
	if len(report.Applied) != 1 || report.Applied[0].Version != 0 {
		t.Fatalf("applied = %+v, want only the baseline sentinel", report.Applied)
	}
}
