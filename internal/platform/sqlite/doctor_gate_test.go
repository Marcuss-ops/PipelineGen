package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/dbhealth"
)

// DB-08 fixture: an orphan injected with FK enforcement off must make the
// doctor return an error finding (the gate's exit-code contract).
func TestSQLiteDoctorDetectsInjectedOrphan(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "doctor.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE parent (id TEXT PRIMARY KEY);
		CREATE TABLE child (id TEXT PRIMARY KEY, parent_id TEXT REFERENCES parent(id));`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO child (id, parent_id) VALUES ('c1', 'missing')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}

	findings := RunSQLiteDoctor(ctx, db, DoctorOptions{Plane: "fixture", Quick: true})
	if !dbhealth.HasErrors(findings) {
		t.Fatalf("doctor did not flag the injected orphan: %+v", findings)
	}
	if !dbhealth.HasBlocking(findings, false) {
		t.Fatal("orphan must be blocking without --strict")
	}
}

// A clean DB must not produce errors (retention/allowlist warnings are fine).
func TestSQLiteDoctorCleanDB(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "doctor-clean.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `CREATE TABLE parent (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	findings := RunSQLiteDoctor(ctx, db, DoctorOptions{Plane: "fixture", Quick: true})
	if dbhealth.HasErrors(findings) {
		t.Fatalf("clean DB produced error findings: %+v", findings)
	}
}
