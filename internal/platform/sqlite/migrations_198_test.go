package sqlite

import "testing"

func TestMigrations_198_CreatesCanonicalControlPlaneIdentity(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	cols := scanColumnNames(t, db, "control_plane_meta")
	for _, want := range []string{"singleton_id", "database_id", "schema_family", "instance_role", "canonical_version", "created_at"} {
		if _, ok := cols[want]; !ok {
			t.Fatalf("control_plane_meta missing column %q (present: %v)", want, cols)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM control_plane_meta`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("control_plane_meta rows = %d, want singleton", count)
	}
	if _, err := db.Exec(`INSERT INTO control_plane_meta (singleton_id, database_id, schema_family, instance_role, canonical_version, created_at) VALUES (2, 'cp_second', 'pipelinegen-control-plane-2', 'READ_ONLY', 1, datetime('now'))`); err == nil {
		t.Fatal("control_plane_meta accepted a second singleton row")
	}
	var family, role string
	if err := db.QueryRow(`SELECT schema_family, instance_role FROM control_plane_meta LIMIT 1`).Scan(&family, &role); err != nil {
		t.Fatal(err)
	}
	if family != "pipelinegen-control-plane" || role != "CANONICAL" {
		t.Fatalf("control plane identity = family %q role %q", family, role)
	}
}
