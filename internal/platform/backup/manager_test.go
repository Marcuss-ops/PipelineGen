package backup

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestManagerCreatesVerifiesAndRestoresManifest(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.sqlite")
	db, err := sql.Open("sqlite3", source)
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE schema_migrations(version INTEGER, filename TEXT, checksum TEXT)`,
		`INSERT INTO schema_migrations VALUES (206,'206_performance_registry.sql','test')`,
		`CREATE TABLE control_plane_meta(database_id TEXT)`,
		`INSERT INTO control_plane_meta VALUES ('cp_test')`,
		`CREATE TABLE registry_events(seq INTEGER)`,
		`INSERT INTO registry_events VALUES (7)`,
		`CREATE TABLE content_objects(sha256 TEXT)`,
		`INSERT INTO content_objects VALUES ('abc')`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	m := NewManager()
	backupPath := filepath.Join(dir, "backup.sqlite")
	manifestPath := filepath.Join(dir, "backup.manifest.json")
	manifest, err := m.CreateControlPlaneBackup(context.Background(), source, backupPath, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DatabaseID != "cp_test" || manifest.RegistrySeq != 7 || manifest.CASObjectCount != 1 {
		t.Fatalf("manifest=%+v", manifest)
	}
	if _, err := m.VerifyControlPlaneBackup(context.Background(), backupPath, manifestPath); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(dir, "fresh", "restored.sqlite")
	result, err := m.RunRestoreDrill(context.Background(), backupPath, restored)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrityOK || !result.SmokeInsertOK || len(result.FKViolations) != 0 {
		t.Fatalf("restore=%+v", result)
	}
}
