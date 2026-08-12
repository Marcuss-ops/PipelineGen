package storage

import (
	"context"
	"testing"

	"go.uber.org/zap/zaptest"
)

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
