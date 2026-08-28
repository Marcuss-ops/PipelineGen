package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestMigrations_LedgerHasCanonicalIdentityAnd194197(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	columns := scanColumnNames(t, db, "schema_migrations")
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("fresh DB integrity = %q, want ok", integrity)
	}
	var foreignKeysEnabled int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeysEnabled); err != nil {
		t.Fatal(err)
	}
	if foreignKeysEnabled != 1 {
		t.Fatalf("fresh DB foreign_keys pragma = %d, want 1", foreignKeysEnabled)
	}
	for _, indexName := range []string{
		"idx_content_objects_integrity_status",
		"idx_media_assets_content_sha256",
		"idx_media_asset_sources_asset_id",
		"idx_media_asset_sources_content_sha256",
	} {
		var indexCount int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, indexName).Scan(&indexCount); err != nil {
			t.Fatal(err)
		}
		if indexCount != 1 {
			t.Fatalf("fresh DB missing index %q", indexName)
		}
	}

	for _, want := range []string{
		"version",
		"migration_id",
		"filename",
		"checksum",
		"checksum_sha256",
		"applied_at",
		"duration_ms",
		"app_git_sha",
	} {
		if _, ok := columns[want]; !ok {
			t.Fatalf("schema_migrations missing canonical column %q (present: %v)", want, columns)
		}
	}

	for _, migration := range []struct {
		version  int
		filename string
	}{
		{194, "194_content_objects.sql"},
		{197, "197_asset_content_link.sql"},
	} {
		content, err := os.ReadFile(filepath.Join("../../../migrations/sqlite", migration.filename))
		if err != nil {
			t.Fatal(err)
		}
		wantChecksum := sha256Hex(content)
		var migrationID, duration int
		var filename, checksum, checksumSHA, gitSHA string
		if err := db.QueryRow(`
			SELECT migration_id, filename, checksum, checksum_sha256, duration_ms, app_git_sha
			FROM schema_migrations WHERE version=?`, migration.version).
			Scan(&migrationID, &filename, &checksum, &checksumSHA, &duration, &gitSHA); err != nil {
			t.Fatalf("read ledger row %d: %v", migration.version, err)
		}
		if migrationID != migration.version || filename != migration.filename {
			t.Fatalf("ledger identity %d = id=%d filename=%q, want id=%d filename=%q", migration.version, migrationID, filename, migration.version, migration.filename)
		}
		if checksum != wantChecksum || checksumSHA != wantChecksum {
			t.Fatalf("ledger checksum %d = checksum=%q checksum_sha256=%q, want %q", migration.version, checksum, checksumSHA, wantChecksum)
		}
		if duration < 0 || strings.TrimSpace(gitSHA) == "" {
			t.Fatalf("ledger audit metadata %d = duration_ms=%d app_git_sha=%q", migration.version, duration, gitSHA)
		}
	}
}

func TestMigrations_LedgerExpandsLegacySchemaAndBackfillsIdentity(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		filename TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	legacyChecksum := strings.Repeat("a", 64)
	if _, err := db.Exec(`INSERT INTO schema_migrations(version, filename, checksum, applied_at) VALUES (194, '194_content_objects.sql', ?, '2026-08-12T00:00:00Z')`, legacyChecksum); err != nil {
		t.Fatal(err)
	}

	if err := ensureMigrationLedger(db); err != nil {
		t.Fatal(err)
	}
	var migrationID int
	var checksumSHA string
	if err := db.QueryRow(`SELECT migration_id, checksum_sha256 FROM schema_migrations WHERE version=194`).Scan(&migrationID, &checksumSHA); err != nil {
		t.Fatal(err)
	}
	if migrationID != 194 || checksumSHA != legacyChecksum {
		t.Fatalf("legacy row backfill = id=%d checksum_sha256=%q", migrationID, checksumSHA)
	}
	columns := scanColumnNames(t, db, "schema_migrations")
	for _, want := range []string{"migration_id", "checksum_sha256", "duration_ms", "app_git_sha"} {
		if _, ok := columns[want]; !ok {
			t.Fatalf("expanded ledger missing %q", want)
		}
	}
}

func TestMigrations_LegacyLedgerCertifiesCurrent194And197Files(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	fixtureDir := copyMigrationSubset(t, targetDir, 0, map[int]bool{194: true, 197: true})
	dbPath := filepath.Join(t.TempDir(), "legacy-current.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		filename TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	for _, migration := range []struct {
		version  int
		filename string
	}{
		{194, "194_content_objects.sql"},
		{197, "197_asset_content_link.sql"},
	} {
		content, err := os.ReadFile(filepath.Join(targetDir, migration.filename))
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, filename, checksum, applied_at) VALUES (?, ?, ?, '2026-08-12T00:00:00Z')`, migration.version, migration.filename, sha256Hex(content)); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	db.Close()

	if err := RunMigrationsOnDB(dbPath, nil, fixtureDir, "primary"); err != nil {
		t.Fatalf("legacy ledger failed current-file certification: %v", err)
	}
}

func TestMigrations_LedgerRejectsMigrationIDMismatch(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		migration_id INTEGER,
		filename TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version, migration_id, filename, checksum, applied_at) VALUES (194, 999, '194_content_objects.sql', ?, '2026-08-12T00:00:00Z')`, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if err := ensureMigrationLedger(db); err == nil || !strings.Contains(err.Error(), "migration_id mismatch") {
		t.Fatalf("migration_id validation error = %v, want migration_id mismatch", err)
	}
}

func TestMigrations_LedgerRejectsChecksumAndFilenameMutation(t *testing.T) {
	dir := writeLedgerFixture(t, map[string]string{
		"001_first.sql": "CREATE TABLE first_table (id INTEGER PRIMARY KEY);\n",
	})
	dbPath := filepath.Join(t.TempDir(), "checksum.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, dir, "primary"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "001_first.sql"), []byte("CREATE TABLE first_table (id INTEGER PRIMARY KEY, changed TEXT);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsOnDB(dbPath, nil, dir, "primary"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("checksum mutation error = %v, want checksum mismatch", err)
	}
	if err := os.Remove(filepath.Join(dir, "001_first.sql")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "001_renamed.sql"), []byte("CREATE TABLE first_table (id INTEGER PRIMARY KEY);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsOnDB(dbPath, nil, dir, "primary"); err == nil || !strings.Contains(err.Error(), "identity/checksum mismatch") {
		t.Fatalf("filename mutation error = %v, want identity/checksum mismatch", err)
	}
}

func TestMigrations_LedgerRejectsOutOfScopeAppliedVersion(t *testing.T) {
	dir := writeLedgerFixture(t, map[string]string{
		"001_primary_only.sql": "-- database: primary\nCREATE TABLE primary_table (id INTEGER PRIMARY KEY);\n",
	})
	dbPath := filepath.Join(t.TempDir(), "scope.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, dir, "primary"); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsOnDB(dbPath, nil, dir, "observability"); err == nil || !strings.Contains(err.Error(), "out-of-scope") {
		t.Fatalf("out-of-scope ledger error = %v, want out-of-scope", err)
	}
}

func TestMigrations_ReconcilesHistoricalRunResourceReports238To239(t *testing.T) {
	targetDir := t.TempDir()
	canonicalPath := filepath.Join("../../../migrations/sqlite", "239_run_resource_reports.sql")
	content, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "239_run_resource_reports.sql"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "historical-238.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE run_resource_reports (run_id TEXT PRIMARY KEY);
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
		INSERT INTO schema_migrations(version, migration_id, filename, checksum, checksum_sha256)
		VALUES (238, 238, '238_run_resource_reports.sql', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa');
	`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	if err := RunMigrationsOnDB(dbPath, nil, targetDir, "observability"); err != nil {
		t.Fatalf("historical 238 ledger was not reconciled: %v", err)
	}
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, migrationID int
	var filename, checksum string
	if err := db.QueryRow(`SELECT version, migration_id, filename, checksum FROM schema_migrations`).Scan(&version, &migrationID, &filename, &checksum); err != nil {
		t.Fatal(err)
	}
	wantChecksum := sha256Hex(content)
	if version != 239 || migrationID != 239 || filename != "239_run_resource_reports.sql" || checksum != wantChecksum {
		t.Fatalf("reconciled ledger = version=%d id=%d filename=%q checksum=%q", version, migrationID, filename, checksum)
	}
}

func TestMigrations_LedgerRejectsAppliedGap(t *testing.T) {
	dir := writeLedgerFixture(t, map[string]string{
		"001_first.sql":  "CREATE TABLE first_table (id INTEGER PRIMARY KEY);\n",
		"002_second.sql": "CREATE TABLE second_table (id INTEGER PRIMARY KEY);\n",
		"003_third.sql":  "CREATE TABLE third_table (id INTEGER PRIMARY KEY);\n",
	})
	dbPath := filepath.Join(t.TempDir(), "gap.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, dir, "primary"); err != nil {
		t.Fatal(err)
	}
	db := openMigrationTestDB(t, dbPath)
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version=2`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if err := RunMigrationsOnDB(dbPath, nil, dir, "primary"); err == nil || !strings.Contains(err.Error(), "migration ledger gap") {
		t.Fatalf("applied gap error = %v, want migration ledger gap", err)
	}
}

func TestMigrations_InterruptedMigrationRollsBackAndIsSafeToRetry(t *testing.T) {
	dir := writeLedgerFixture(t, map[string]string{
		"001_first.sql":  "CREATE TABLE first_table (id INTEGER PRIMARY KEY);\n",
		"002_broken.sql": "CREATE TABLE partial_table (id INTEGER PRIMARY KEY);\nTHIS IS NOT VALID SQL;\n",
	})
	dbPath := filepath.Join(t.TempDir(), "interrupted.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, dir, "primary"); err == nil {
		t.Fatal("interrupted migration unexpectedly succeeded")
	}
	db := openMigrationTestDB(t, dbPath)
	assertMigrationTestTable(t, db, "first_table", true)
	assertMigrationTestTable(t, db, "partial_table", false)
	var versionTwo int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=2`).Scan(&versionTwo); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if versionTwo != 0 {
		db.Close()
		t.Fatalf("failed migration ledger rows=%d, want 0", versionTwo)
	}
	db.Close()

	if err := os.WriteFile(filepath.Join(dir, "002_broken.sql"), []byte("CREATE TABLE partial_table (id INTEGER PRIMARY KEY);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsOnDB(dbPath, nil, dir, "primary"); err != nil {
		t.Fatalf("retry after repairing migration: %v", err)
	}
	db = openMigrationTestDB(t, dbPath)
	defer db.Close()
	assertMigrationTestTable(t, db, "partial_table", true)
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=2`).Scan(&versionTwo); err != nil {
		t.Fatal(err)
	}
	if versionTwo != 1 {
		t.Fatalf("retried migration ledger rows=%d, want 1", versionTwo)
	}
}

func TestMigrations_194197PreserveDataAndRestoreIntegrity(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	pre194Dir := copyMigrationSubset(t, targetDir, 193, map[int]bool{194: true, 195: true, 196: true, 197: true})
	dbPath := filepath.Join(t.TempDir(), "production-copy.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, pre194Dir, "primary"); err != nil {
		t.Fatal(err)
	}

	// Seed a realistic pre-migration copy before 194/197 exist. These rows
	// must survive both the ALTER TABLE and the new registry-table creation.
	db := openMigrationTestDB(t, dbPath)
	if _, err := db.Exec(`INSERT INTO media_assets (id, lifecycle_state, index_state) VALUES ('phase1-existing-asset', 'ACTIVE', 'NOT_INDEXABLE')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO asset_text_tracks (asset_id, language_code, text_kind, text_content) VALUES ('phase1-existing-asset', 'en', 'transcript', 'preserved transcript')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	var beforeAssets, beforeTracks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets`).Scan(&beforeAssets); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_text_tracks`).Scan(&beforeTracks); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	backupPath := filepath.Join(t.TempDir(), "backup.sqlite")
	copySQLiteFile(t, dbPath, backupPath)
	if err := RunMigrationsOnDB(dbPath, nil, targetDir, "primary"); err != nil {
		t.Fatal(err)
	}
	db = openMigrationTestDB(t, dbPath)
	var afterAssets, afterTracks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets`).Scan(&afterAssets); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_text_tracks`).Scan(&afterTracks); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if beforeAssets != afterAssets || beforeTracks != afterTracks {
		db.Close()
		t.Fatalf("counts changed after 194/197: assets %d->%d tracks %d->%d", beforeAssets, afterAssets, beforeTracks, afterTracks)
	}
	var transcript string
	if err := db.QueryRow(`SELECT text_content FROM asset_text_tracks WHERE asset_id='phase1-existing-asset'`).Scan(&transcript); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if transcript != "preserved transcript" {
		db.Close()
		t.Fatalf("preserved transcript = %q", transcript)
	}
	db.Close()

	// Reapplication must be a no-op and retain the same rows.
	if err := RunMigrationsOnDB(dbPath, nil, targetDir, "primary"); err != nil {
		t.Fatal(err)
	}
	db = openMigrationTestDB(t, dbPath)
	var reappliedAssets, reappliedTracks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets`).Scan(&reappliedAssets); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_text_tracks`).Scan(&reappliedTracks); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if reappliedAssets != beforeAssets || reappliedTracks != beforeTracks {
		db.Close()
		t.Fatalf("counts changed after reapply: assets %d->%d tracks %d->%d", beforeAssets, reappliedAssets, beforeTracks, reappliedTracks)
	}
	db.Close()

	// Restore the pre-194 copy elsewhere, apply the same migrations, and
	// certify physical integrity, preserved rows, and both ledger entries.
	if err := RunMigrationsOnDB(backupPath, nil, targetDir, "primary"); err != nil {
		t.Fatal(err)
	}
	db = openMigrationTestDB(t, backupPath)
	defer db.Close()
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("restored DB integrity = %q, want ok", integrity)
	}
	var restoredAssets, restoredTracks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets`).Scan(&restoredAssets); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_text_tracks`).Scan(&restoredTracks); err != nil {
		t.Fatal(err)
	}
	if restoredAssets != beforeAssets || restoredTracks != beforeTracks {
		t.Fatalf("restored DB counts = assets %d tracks %d, want %d/%d", restoredAssets, restoredTracks, beforeAssets, beforeTracks)
	}
	var restoredTranscript string
	if err := db.QueryRow(`SELECT text_content FROM asset_text_tracks WHERE asset_id='phase1-existing-asset'`).Scan(&restoredTranscript); err != nil {
		t.Fatal(err)
	}
	if restoredTranscript != "preserved transcript" {
		t.Fatalf("restored transcript = %q", restoredTranscript)
	}
	var restoredLifecycle, restoredIndexState string
	if err := db.QueryRow(`SELECT lifecycle_state, index_state FROM media_assets WHERE id='phase1-existing-asset'`).Scan(&restoredLifecycle, &restoredIndexState); err != nil {
		t.Fatal(err)
	}
	if restoredLifecycle != "ACTIVE" || restoredIndexState != "NOT_INDEXABLE" {
		t.Fatalf("restored asset state = lifecycle=%q index=%q", restoredLifecycle, restoredIndexState)
	}
	for _, version := range []int{194, 197} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=? AND migration_id=? AND checksum=checksum_sha256`, version, version).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("restored DB missing certified ledger row %d", version)
		}
	}
}

func copyMigrationSubset(t *testing.T, sourceDir string, maxVersion int, include map[int]bool) string {
	t.Helper()
	destination := t.TempDir()
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		version, err := parseMigrationVersion(entry.Name())
		if err != nil || (version > maxVersion && !include[version]) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(sourceDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(destination, entry.Name()), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return destination
}

func writeLedgerFixture(t *testing.T, migrations map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for filename, content := range migrations {
		if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func openMigrationTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func assertMigrationTestTable(t *testing.T, db *sql.DB, table string, want bool) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if (count == 1) != want {
		t.Fatalf("table %s presence=%v, want %v", table, count == 1, want)
	}
}

func copySQLiteFile(t *testing.T, source, destination string) {
	t.Helper()
	_ = os.Remove(destination)
	db := openMigrationTestDB(t, source)
	defer db.Close()
	if _, err := db.Exec(`VACUUM INTO ?`, destination); err != nil {
		t.Fatalf("VACUUM INTO %s: %v", destination, err)
	}
}
