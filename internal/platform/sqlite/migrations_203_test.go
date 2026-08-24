package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrations_202_203_ControlPlaneSchemaAndChecksums(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "control-plane.sqlite")
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsOnDB(dbPath, nil, targetDir, "primary"); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := RunMigrationsOnDB(dbPath, nil, targetDir, "primary"); err != nil {
		t.Fatalf("reinstall: %v", err)
	}

	db := openSmokeDB(t, dbPath)
	defer db.Close()
	for _, table := range []string{"registry_events", "registry_runs", "projection_registry", "backup_registry", "canonical_mutations"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
	assertMigrationChecksum203(t, db, 202, "202_canonical_mutation_uow.sql")
	assertMigrationChecksum203(t, db, 203, "203_control_plane_ledger_replay_metadata.sql")
}

func assertMigrationChecksum203(t *testing.T, db *sql.DB, version int, filename string) {
	t.Helper()
	var gotName, gotChecksum string
	if err := db.QueryRow(`SELECT filename, checksum FROM schema_migrations WHERE version=?`, version).Scan(&gotName, &gotChecksum); err != nil {
		t.Fatalf("ledger migration %d: %v", version, err)
	}
	if gotName != filename {
		t.Fatalf("migration %d filename=%q want %q", version, gotName, filename)
	}
	content, err := os.ReadFile(filepath.Join("../../../migrations/sqlite", filename))
	if err != nil {
		t.Fatal(err)
	}
	if gotChecksum != sha256Hex(content) {
		t.Fatalf("migration %d checksum mismatch", version)
	}
}
