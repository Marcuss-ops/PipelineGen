package wiring

import (
	"context"
	"testing"

	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"go.uber.org/zap/zaptest"
)

func TestDatabasesValidateControlPlaneIdentityAcceptsCanonicalPrimary(t *testing.T) {
	mainDB, err := storage.OpenSQLiteDB(t.TempDir()+"/media.sqlite", zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	defer mainDB.Close()
	insertMeta(t, mainDB, "CANONICAL")

	dbs := &Databases{Main: mainDB}
	if err := dbs.ValidateControlPlaneIdentity(context.Background()); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestDatabasesValidateControlPlaneIdentityRejectsReadOnlyPrimary(t *testing.T) {
	mainDB, err := storage.OpenSQLiteDB(t.TempDir()+"/media.sqlite", zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	defer mainDB.Close()
	insertMeta(t, mainDB, "READ_ONLY")

	err = (&Databases{Main: mainDB}).ValidateControlPlaneIdentity(context.Background())
	if err == nil || !containsIdentityError(err.Error(), "want \"CANONICAL\"") {
		t.Fatalf("error = %v, want read-only primary failure", err)
	}
}

func TestDatabasesValidateControlPlaneIdentityRejectsSplitWritableJobsDB(t *testing.T) {
	mainDB, err := storage.OpenSQLiteDB(t.TempDir()+"/media.sqlite", zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	defer mainDB.Close()
	jobsDB, err := storage.OpenSQLiteDB(t.TempDir()+"/jobs.sqlite", zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	defer jobsDB.Close()
	insertMeta(t, mainDB, "CANONICAL")

	dbs := &Databases{Main: mainDB, Jobs: jobsDB}
	err = dbs.ValidateControlPlaneIdentity(context.Background())
	if err == nil || !containsIdentityError(err.Error(), "multiple control-plane writers detected") {
		t.Fatalf("error = %v, want multiple-writer failure", err)
	}
}

func insertMeta(t *testing.T, db *storage.SQLiteDB, role string) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE control_plane_meta (
		database_id TEXT PRIMARY KEY,
		schema_family TEXT NOT NULL,
		instance_role TEXT NOT NULL,
		canonical_version INTEGER NOT NULL,
		created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO control_plane_meta (database_id, schema_family, instance_role, canonical_version, created_at) VALUES ('cp_test', 'pipelinegen-control-plane', ?, 1, '2026-08-12T00:00:00Z')`, role); err != nil {
		t.Fatal(err)
	}
}

func containsIdentityError(value, want string) bool {
	for i := 0; i+len(want) <= len(value); i++ {
		if value[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
