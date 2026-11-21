package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCachePlaneRepair299Fixture copies the real 299_cache_plane_repair.sql
// into a temporary migration dir so the reconcile reads the canonical file
// (and its pinned sha256) exactly as production does.
func writeCachePlaneRepair299Fixture(t *testing.T) string {
	t.Helper()
	targetDir := t.TempDir()
	content, err := os.ReadFile(filepath.Join("../../../migrations/sqlite", "299_cache_plane_repair.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "299_cache_plane_repair.sql"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	return targetDir
}

// seedCachePlaneRepair299Ledger creates a cache database whose ledger records
// 299 with the given checksum. createSchema controls the durable schema effect
// (vidrush_provider_cache) the repair requires.
func seedCachePlaneRepair299Ledger(t *testing.T, checksum string, createSchema bool) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cache-plane-299.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	schema := ""
	if createSchema {
		schema = "CREATE TABLE vidrush_provider_cache (cache_key TEXT PRIMARY KEY);"
	}
	_, err = db.Exec(`
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			migration_id INTEGER NOT NULL UNIQUE,
			filename TEXT NOT NULL,
			checksum TEXT NOT NULL,
			checksum_sha256 TEXT NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now')),
			duration_ms INTEGER NOT NULL DEFAULT 0,
			app_git_sha TEXT NOT NULL DEFAULT 'test'
		);
		` + schema + `
		INSERT INTO schema_migrations(version, migration_id, filename, checksum, checksum_sha256)
		VALUES (299, 299, '299_cache_plane_repair.sql', '` + checksum + `', '` + checksum + `');
	`)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	return dbPath
}

// TestMigrations_ReconcilesCachePlaneRepair299LegacyChecksum certifies the
// forward repair for the 299 file edited in place after it was applied: the
// ledger keeps the legacy hash, the file now hashes to the canonical one, and
// a cache database must boot instead of failing the checksum guard.
func TestMigrations_ReconcilesCachePlaneRepair299LegacyChecksum(t *testing.T) {
	targetDir := writeCachePlaneRepair299Fixture(t)
	dbPath := seedCachePlaneRepair299Ledger(t, legacyCachePlaneRepair299Checksum, true)

	if err := RunMigrationsOnDB(dbPath, nil, targetDir, "cache"); err != nil {
		t.Fatalf("legacy 299 ledger was not reconciled: %v", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var checksum, checksumSHA string
	if err := db.QueryRow(`SELECT checksum, checksum_sha256 FROM schema_migrations WHERE version = 299`).Scan(&checksum, &checksumSHA); err != nil {
		t.Fatal(err)
	}
	if checksum != canonicalCachePlaneRepair299Checksum || checksumSHA != canonicalCachePlaneRepair299Checksum {
		t.Fatalf("reconciled 299 ledger = checksum %q / sha256 %q, want %q", checksum, checksumSHA, canonicalCachePlaneRepair299Checksum)
	}
}

// TestMigrations_CachePlaneRepair299AlreadyCanonicalIsNoOp pins idempotence:
// the live cache database was repaired manually before this code existed, and
// booting it again must be a no-op, not a rewrite.
func TestMigrations_CachePlaneRepair299AlreadyCanonicalIsNoOp(t *testing.T) {
	targetDir := writeCachePlaneRepair299Fixture(t)
	dbPath := seedCachePlaneRepair299Ledger(t, canonicalCachePlaneRepair299Checksum, true)

	if err := RunMigrationsOnDB(dbPath, nil, targetDir, "cache"); err != nil {
		t.Fatalf("canonical 299 ledger should be a no-op: %v", err)
	}
}

// TestMigrations_RejectsUnknownCachePlaneRepair299Checksum keeps the rule
// fail-closed: an arbitrary hash at 299 is not promoted by the repair.
func TestMigrations_RejectsUnknownCachePlaneRepair299Checksum(t *testing.T) {
	targetDir := writeCachePlaneRepair299Fixture(t)
	dbPath := seedCachePlaneRepair299Ledger(t, strings.Repeat("a", 64), true)

	err := RunMigrationsOnDB(dbPath, nil, targetDir, "cache")
	if err == nil || !strings.Contains(err.Error(), "checksum is not authorized") {
		t.Fatalf("unknown 299 checksum error = %v, want checksum is not authorized", err)
	}
}

// TestMigrations_RejectsCachePlaneRepair299WithoutSchemaEffect refuses the
// repair when the durable schema the migration was supposed to create is
// absent: a fabricated ledger row must not be promoted by a hash alone.
func TestMigrations_RejectsCachePlaneRepair299WithoutSchemaEffect(t *testing.T) {
	targetDir := writeCachePlaneRepair299Fixture(t)
	dbPath := seedCachePlaneRepair299Ledger(t, legacyCachePlaneRepair299Checksum, false)

	err := RunMigrationsOnDB(dbPath, nil, targetDir, "cache")
	if err == nil || !strings.Contains(err.Error(), "vidrush_provider_cache") {
		t.Fatalf("missing schema effect error = %v, want vidrush_provider_cache absent", err)
	}
}
