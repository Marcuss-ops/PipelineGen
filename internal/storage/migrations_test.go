package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestSchemaMigrationsTableCreation ensures the ledger table can be created
// and that RunMigrations on an empty DB creates the table.
func TestSchemaMigrationsTableCreation(t *testing.T) {
	db := NewTestDB(t, &TestDBOpts{InMemory: true})
	sdb := &SQLiteDB{DB: db, log: zap.NewNop()}

	_, err := sdb.Exec(schemaMigrationsTable)
	require.NoError(t, err)

	var name string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&name)
	require.NoError(t, err)
	assert.Equal(t, "schema_migrations", name)
}

// TestParseMigrationVersion validates version parsing from filenames.
func TestParseMigrationVersion(t *testing.T) {
	tests := []struct {
		filename    string
		wantVersion int
		wantErr     bool
	}{
		{"001_velox_core.sql", 1, false},
		{"050_asset_versions.sql", 50, false},
		{"001_initial.sql", 1, false},
		{"999_future.sql", 999, false},
		{"not-a-migration.sql", 0, true},
		{"abc_migration.sql", 0, true},
		{"0_invalid.sql", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		version, err := parseMigrationVersion(tt.filename)
		if tt.wantErr {
			assert.Error(t, err, "filename=%q expected error", tt.filename)
		} else {
			assert.NoError(t, err, "filename=%q unexpected error", tt.filename)
			assert.Equal(t, tt.wantVersion, version, "filename=%q version mismatch", tt.filename)
		}
	}
}

// TestRunMigrationsOnEmptyDB applies real migrations to an empty in-memory DB
// and verifies tables were created.
func TestRunMigrationsOnEmptyDB(t *testing.T) {
	tmpDir := setupTestMigrationDir(t)
	defer os.RemoveAll(tmpDir)

	db := NewTestDB(t, &TestDBOpts{InMemory: true})
	sdb := &SQLiteDB{DB: db, log: zap.NewNop()}

	err := sdb.RunMigrations(zap.NewNop(), tmpDir)
	require.NoError(t, err)

	expectedTables := []string{
		"schema_migrations",
		"jobs",
		"job_events",
		"scripts",
	}
	for _, table := range expectedTables {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?",
			table,
		).Scan(&name)
		assert.NoError(t, err, "expected table %q to exist", table)
		assert.Equal(t, table, name)
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "expected exactly 1 migration recorded")
}

// TestRunMigrationsChecksumMismatch ensures modifying an already-applied
// migration is detected and rejected.
func TestRunMigrationsChecksumMismatch(t *testing.T) {
	tmpDir := setupTestMigrationDir(t)
	defer os.RemoveAll(tmpDir)

	db := NewTestDB(t, &TestDBOpts{InMemory: true})
	sdb := &SQLiteDB{DB: db, log: zap.NewNop()}

	// First run — succeeds
	err := sdb.RunMigrations(zap.NewNop(), tmpDir)
	require.NoError(t, err)

	// Tamper with the migration file
	firstFile := filepath.Join(tmpDir, "001_velox_core.sql")
	original, err := os.ReadFile(firstFile)
	require.NoError(t, err)
	err = os.WriteFile(firstFile, append([]byte("/* tampered */\n"), original...), 0644)
	require.NoError(t, err)

	// Second run — must fail
	err = sdb.RunMigrations(zap.NewNop(), tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
}

// TestRunMigrationsIdempotent verifies re-running migrations is a no-op.
func TestRunMigrationsIdempotent(t *testing.T) {
	tmpDir := setupTestMigrationDir(t)
	defer os.RemoveAll(tmpDir)

	db := NewTestDB(t, &TestDBOpts{InMemory: true})
	sdb := &SQLiteDB{DB: db, log: zap.NewNop()}

	require.NoError(t, sdb.RunMigrations(zap.NewNop(), tmpDir))

	var firstCount int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&firstCount))

	require.NoError(t, sdb.RunMigrations(zap.NewNop(), tmpDir))

	var secondCount int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&secondCount))
	assert.Equal(t, firstCount, secondCount, "re-running should not add records")
}

// TestRunMigrationsWithPreExistingSchemaMigrations ensures a pre-seeded
// schema_migrations table with an unrelated version survives migration.
func TestRunMigrationsWithPreExistingSchemaMigrations(t *testing.T) {
	tmpDir := setupTestMigrationDir(t)
	defer os.RemoveAll(tmpDir)

	db := NewTestDB(t, &TestDBOpts{InMemory: true})

	// Pre-create schema_migrations with an unrelated version
	_, err := db.Exec(schemaMigrationsTable)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO schema_migrations (version, filename, checksum) VALUES (999, '999_preexisting.sql', 'deadbeef')")
	require.NoError(t, err)

	sdb := &SQLiteDB{DB: db, log: zap.NewNop()}
	err = sdb.RunMigrations(zap.NewNop(), tmpDir)
	require.NoError(t, err)

	var preExistingVersion int
	err = db.QueryRow("SELECT version FROM schema_migrations WHERE version=999").Scan(&preExistingVersion)
	require.NoError(t, err)
	assert.Equal(t, 999, preExistingVersion)
}

// TestRunMigrationsNoDir verifies missing directory returns a clean error.
func TestRunMigrationsNoDir(t *testing.T) {
	db := NewTestDB(t, &TestDBOpts{InMemory: true})
	sdb := &SQLiteDB{DB: db, log: zap.NewNop()}

	err := sdb.RunMigrations(zap.NewNop(), "/nonexistent/path/xyz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read migrations dir")
}

// TestDuplicateVersionsRejected verifies that two files with the same
// version number are rejected.
func TestDuplicateVersionsRejected(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "migration-dup-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "001_first.sql"),
		[]byte("CREATE TABLE IF NOT EXISTS foo (id INTEGER);"),
		0644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "001_second.sql"),
		[]byte("CREATE TABLE IF NOT EXISTS bar (id INTEGER);"),
		0644,
	))

	db := NewTestDB(t, &TestDBOpts{InMemory: true})
	sdb := &SQLiteDB{DB: db, log: zap.NewNop()}

	err = sdb.RunMigrations(zap.NewNop(), tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate migration version")
}

// TestGapDetectionNoError verifies gaps are logged as warnings,
// not errors. The runner proceeds normally.
func TestGapDetectionNoError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "migration-gap-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "001_first.sql"),
		[]byte("CREATE TABLE IF NOT EXISTS foo (id INTEGER);"),
		0644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "005_skip.sql"),
		[]byte("CREATE TABLE IF NOT EXISTS bar (id INTEGER);"),
		0644,
	))

	db := NewTestDB(t, &TestDBOpts{InMemory: true})
	sdb := &SQLiteDB{DB: db, log: zap.NewNop()}

	// Gaps are warnings, not errors — RunMigrations should succeed
	err = sdb.RunMigrations(zap.NewNop(), tmpDir)
	require.NoError(t, err, "gaps should not cause migration failure")
}

// TestGapDetectionContiguous verifies contiguous versions 001→002 pass.
func TestGapDetectionContiguous(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "migration-contig-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "001_first.sql"),
		[]byte("CREATE TABLE IF NOT EXISTS foo (id INTEGER);"),
		0644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "002_second.sql"),
		[]byte("CREATE TABLE IF NOT EXISTS bar (id INTEGER);"),
		0644,
	))

	db := NewTestDB(t, &TestDBOpts{InMemory: true})
	sdb := &SQLiteDB{DB: db, log: zap.NewNop()}

	// Should succeed without gap error
	err = sdb.RunMigrations(zap.NewNop(), tmpDir)
	require.NoError(t, err)
}

// TestGetMigrationStatus validates the status report generation.
func TestGetMigrationStatus(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "migration-status-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "001_first.sql"),
		[]byte("CREATE TABLE IF NOT EXISTS foo (id INTEGER);"),
		0644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "002_second.sql"),
		[]byte("CREATE TABLE IF NOT EXISTS bar (id INTEGER);"),
		0644,
	))

	db := NewTestDB(t, &TestDBOpts{InMemory: true})
	sdb := &SQLiteDB{DB: db, log: zap.NewNop()}

	// Before any migrations: all pending
	report, err := GetMigrationStatus(db, tmpDir)
	require.NoError(t, err)
	assert.Equal(t, 2, report.Total)
	assert.Equal(t, 0, report.AppliedN)
	assert.Equal(t, 2, report.PendingN)

	// Apply all pending migrations
	require.NoError(t, sdb.RunMigrations(zap.NewNop(), tmpDir))

	// All migrations now applied
	report, err = GetMigrationStatus(db, tmpDir)
	require.NoError(t, err)
	assert.Equal(t, 2, report.Total)
	assert.Equal(t, 2, report.AppliedN)
	assert.Equal(t, 0, report.PendingN)

	// Format test — just ensure no panic
	formatted := FormatMigrateStatus(report)
	assert.Contains(t, formatted, "applied")
	assert.Contains(t, formatted, "pending")
}

// TestNewSQLiteDB validates DB creation with WAL mode and foreign keys.
func TestNewSQLiteDB(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sqlite-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	sdb, err := NewSQLiteDB(tmpDir, "test.db", zap.NewNop())
	require.NoError(t, err)
	defer sdb.Close()

	var journalMode string
	require.NoError(t, sdb.QueryRow("PRAGMA journal_mode").Scan(&journalMode))
	assert.Equal(t, "wal", journalMode)

	var fkEnabled int
	require.NoError(t, sdb.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled))
	assert.Equal(t, 1, fkEnabled)

	_, err = os.Stat(filepath.Join(tmpDir, "test.db"))
	assert.NoError(t, err)
}

// TestNewSQLiteDBPath verifies Path() and DBName() helpers.
func TestNewSQLiteDBPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sqlite-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	sdb, err := NewSQLiteDB(tmpDir, "media.db.sqlite", zap.NewNop())
	require.NoError(t, err)
	defer sdb.Close()

	assert.Equal(t, filepath.Join(tmpDir, "media.db.sqlite"), sdb.Path())
	assert.Equal(t, "media.db.sqlite", sdb.DBName())
}

// TestDBMediaConstant verifies the DBMedia constant value.
func TestDBMediaConstant(t *testing.T) {
	assert.Equal(t, "media.db.sqlite", DBMedia)
}

// setupTestMigrationDir creates a temporary directory containing a minimal
// but realistic migration file. Returns the path; caller removes it.
func setupTestMigrationDir(t *testing.T) string {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "migration-test-*")
	require.NoError(t, err)

	migration001 := `CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    payload_json TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS job_events (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    type TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS scripts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic TEXT NOT NULL DEFAULT '',
    narrative_text TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_jobs_type ON jobs(type);
`
	err = os.WriteFile(filepath.Join(tmpDir, "001_velox_core.sql"), []byte(migration001), 0644)
	require.NoError(t, err)

	return tmpDir
}
