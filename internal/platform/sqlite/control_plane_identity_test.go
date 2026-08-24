package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestValidateConfiguredControlPlaneWritersRequiresExactlyOneCanonicalWriter(t *testing.T) {
	tests := []struct {
		name    string
		dbs     []ConfiguredDatabase
		wantErr string
	}{
		{
			name: "single canonical writer",
			dbs:  []ConfiguredDatabase{{Name: "primary", Path: "/tmp/primary.sqlite", Role: ControlPlaneRoleCanonical, Writable: true, ControlPlane: true}},
		},
		{
			name: "operational observability store is not a control-plane writer",
			dbs: []ConfiguredDatabase{
				{Name: "primary", Path: "/tmp/media.sqlite", Role: ControlPlaneRoleCanonical, Writable: true, ControlPlane: true},
				{Name: "observability", Path: "/tmp/observability.sqlite", Role: ControlPlaneRoleReadOnly, Writable: true, ControlPlane: false},
			},
		},
		{
			name: "two canonical writers",
			dbs: []ConfiguredDatabase{
				{Name: "media", Path: "/tmp/media.sqlite", Role: ControlPlaneRoleCanonical, Writable: true, ControlPlane: true},
				{Name: "jobs", Path: "/tmp/jobs.sqlite", Role: ControlPlaneRoleCanonical, Writable: true, ControlPlane: true},
			},
			wantErr: "multiple control-plane writers detected",
		},
		{
			name:    "no canonical writer",
			dbs:     []ConfiguredDatabase{{Name: "archive", Path: "/tmp/archive.sqlite", Role: ControlPlaneRoleArchive, Writable: false, ControlPlane: true}},
			wantErr: "want exactly one",
		},
		{
			name:    "writable read-only role is invalid",
			dbs:     []ConfiguredDatabase{{Name: "primary", Path: "/tmp/primary.sqlite", Role: ControlPlaneRoleReadOnly, Writable: true, ControlPlane: true}},
			wantErr: "writable Control Plane database",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfiguredControlPlaneWriters(tt.dbs)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !containsString(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateConfiguredControlPlaneWritersRejectsWritablePathAlias(t *testing.T) {
	dbPath := t.TempDir() + "/control-plane.sqlite"
	err := ValidateConfiguredControlPlaneWriters([]ConfiguredDatabase{
		{Name: "media", Path: dbPath, Role: ControlPlaneRoleCanonical, Writable: true, ControlPlane: true},
		{Name: "jobs", Path: dbPath, Role: ControlPlaneRoleCanonical, Writable: true, ControlPlane: true},
	})
	if err == nil || !containsString(err.Error(), "same SQLite file") {
		t.Fatalf("error = %v, want same-file writer collision", err)
	}
}

func TestValidateConfiguredControlPlaneWritersRejectsSymlinkAlias(t *testing.T) {
	dir := t.TempDir()
	canonicalPath := filepath.Join(dir, "media.sqlite")
	aliasPath := filepath.Join(dir, "jobs.sqlite")
	if err := os.WriteFile(canonicalPath, []byte("sqlite placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canonicalPath, aliasPath); err != nil {
		t.Fatal(err)
	}

	err := ValidateConfiguredControlPlaneWriters([]ConfiguredDatabase{
		{Name: "media", Path: canonicalPath, Role: ControlPlaneRoleCanonical, Writable: true, ControlPlane: true},
		{Name: "jobs", Path: aliasPath, Role: ControlPlaneRoleCanonical, Writable: true, ControlPlane: true},
	})
	if err == nil || !containsString(err.Error(), "same SQLite file") {
		t.Fatalf("error = %v, want symlink writer collision", err)
	}
}

func TestReadControlPlaneMetaValidatesCanonicalIdentity(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE control_plane_meta (
		database_id TEXT PRIMARY KEY,
		schema_family TEXT NOT NULL,
		instance_role TEXT NOT NULL,
		canonical_version INTEGER NOT NULL,
		created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO control_plane_meta VALUES ('cp_test', 'pipelinegen-control-plane', 'CANONICAL', 1, '2026-08-12T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	meta, err := ReadControlPlaneMeta(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if meta.DatabaseID != "cp_test" || meta.InstanceRole != ControlPlaneRoleCanonical {
		t.Fatalf("metadata = %+v, want canonical cp_test identity", meta)
	}
}

func containsString(value, want string) bool {
	for i := 0; i+len(want) <= len(value); i++ {
		if value[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
