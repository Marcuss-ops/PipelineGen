package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"go.uber.org/zap/zaptest"
)

func TestOpenSetRejectsNonCanonicalPrimaryPath(t *testing.T) {
	root := t.TempDir()
	_, err := OpenSet(StorageConfig{
		DataDir:             root,
		PrimaryDBPath:       filepath.Join(root, "media.db.sqlite"),
		ObservabilityDBPath: filepath.Join(root, "observability", "api_requests.db.sqlite"),
	}, zaptest.NewLogger(t))
	if err == nil {
		t.Fatal("OpenSet accepted legacy <DataDir>/media.db.sqlite path")
	}
}

func TestSQLiteHealthPathIsCanonicalOnly(t *testing.T) {
	dataDir := t.TempDir()
	canonical := filepath.Join(dataDir, "media", "media.db.sqlite")
	if got := GetAllDBs(); len(got) != 1 || got[0] != filepath.Join("media", DBMedia) {
		t.Fatalf("GetAllDBs() = %v, want [%q]", got, filepath.Join("media", DBMedia))
	}
	if got := GetDBPath(dataDir, filepath.Join("media", DBMedia)); got != canonical {
		t.Fatalf("GetDBPath canonical = %q, want %q", got, canonical)
	}
	for _, legacy := range []string{"media.db.sqlite", "cmd/admin/data/media/media.db.sqlite", "stock/stock.db.sqlite"} {
		if got := GetDBPath(dataDir, legacy); got != "" {
			t.Fatalf("GetDBPath(%q) = %q, want empty for unsupported path", legacy, got)
		}
	}
}

func TestDatabaseSetValidateControlPlaneIdentityRequiresCanonicalPrimary(t *testing.T) {
	primary, err := OpenSQLiteDB(t.TempDir()+"/primary.sqlite", zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	observability, err := OpenSQLiteDB(t.TempDir()+"/observability.sqlite", zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	defer observability.Close()

	if _, err := primary.Exec(`CREATE TABLE control_plane_meta (
		database_id TEXT PRIMARY KEY,
		schema_family TEXT NOT NULL,
		instance_role TEXT NOT NULL,
		canonical_version INTEGER NOT NULL,
		created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.Exec(`INSERT INTO control_plane_meta VALUES ('cp_test', 'pipelinegen-control-plane', 'CANONICAL', 1, '2026-08-12T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	set := &DatabaseSet{Primary: primary, Observability: observability}
	if err := set.ValidateControlPlaneIdentity(context.Background()); err != nil {
		t.Fatalf("canonical primary rejected: %v", err)
	}

	if _, err := primary.Exec(`UPDATE control_plane_meta SET instance_role = 'READ_ONLY'`); err != nil {
		t.Fatal(err)
	}
	if err := set.ValidateControlPlaneIdentity(context.Background()); err == nil {
		t.Fatal("read-only primary accepted as operational SSOT writer")
	}
}
