package controlplane

import (
	"context"
	"database/sql"
	"testing"

	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	_ "github.com/mattn/go-sqlite3"
)

func TestVerifyReportsHealthyCanonicalPlane(t *testing.T) {
	db := newVerifierFixture(t, "controlplane-test", 0, 0)
	v, err := New(db, "/tmp/pipelinegen.db")
	if err != nil {
		t.Fatal(err)
	}
	report, err := v.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Healthy() {
		t.Fatalf("expected healthy report: %+v", report)
	}
	if report.DatabaseID != "cp_test" || report.InstanceRole != "CANONICAL" {
		t.Fatalf("identity = database_id=%q role=%q, want cp_test/CANONICAL", report.DatabaseID, report.InstanceRole)
	}
}

func TestVerifyFailsProjectionDrift(t *testing.T) {
	db := newVerifierFixture(t, "controlplane-drift", 4, 2)
	v, err := New(db, "/tmp/pipelinegen.db")
	if err != nil {
		t.Fatal(err)
	}
	report, err := v.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Healthy() || report.ProjectionDrift != 2 {
		t.Fatalf("expected drift failure: %+v", report)
	}
}

func TestVerifyRejectsProjectionAheadOfSSOT(t *testing.T) {
	db := newVerifierFixture(t, "controlplane-ahead", 4, 5)
	v, err := New(db, "/tmp/pipelinegen.db")
	if err != nil {
		t.Fatal(err)
	}
	report, err := v.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Healthy() || report.ProjectionState != "AHEAD_OF_SSOT" {
		t.Fatalf("expected ahead projection failure: %+v", report)
	}
}

func TestVerifyFailsMultipleConfiguredControlPlaneWriters(t *testing.T) {
	db := newVerifierFixture(t, "controlplane-topology", 0, 0)
	v, err := NewWithTopology(db, "/tmp/pipelinegen.db", []storage.ConfiguredDatabase{
		{Name: "primary", Path: "/tmp/primary.sqlite", Role: storage.ControlPlaneRoleCanonical, Writable: true, ControlPlane: true},
		{Name: "jobs", Path: "/tmp/jobs.sqlite", Role: storage.ControlPlaneRoleCanonical, Writable: true, ControlPlane: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := v.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Healthy() {
		t.Fatal("multiple writable canonical databases accepted by verifier")
	}
	for _, check := range report.Checks {
		if check.Name == "control_plane_topology" && check.Status == "FAIL" {
			return
		}
	}
	t.Fatalf("topology failure check missing: %+v", report.Checks)
}

func newVerifierFixture(t *testing.T, name string, registrySeq, projectionSeq int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	statements := []string{
		`CREATE TABLE schema_migrations(version INTEGER, filename TEXT NOT NULL, checksum TEXT NOT NULL)`,
		`CREATE TABLE control_plane_meta(database_id TEXT PRIMARY KEY, schema_family TEXT, instance_role TEXT, canonical_version INTEGER, created_at TEXT)`,
		`INSERT INTO control_plane_meta VALUES ('cp_test','pipelinegen-control-plane','CANONICAL',1,'2026-08-12T00:00:00Z')`,
		`CREATE TABLE media_assets(id TEXT PRIMARY KEY, lifecycle_state TEXT, content_sha256 TEXT)`,
		`CREATE TABLE asset_text_tracks(asset_id TEXT, text_kind TEXT, status TEXT, is_current INTEGER)`,
		`CREATE TABLE jobs(id TEXT PRIMARY KEY)`,
		`CREATE TABLE job_steps(id TEXT)`,
		`CREATE TABLE registry_events(seq INTEGER PRIMARY KEY)`,
		`CREATE TABLE registry_runs(run_id TEXT PRIMARY KEY)`,
		`CREATE TABLE projection_registry(source_registry_seq INTEGER, status TEXT)`,
		`CREATE TABLE backup_registry(backup_id TEXT PRIMARY KEY)`,
		`CREATE TABLE outbox_events(status TEXT)`,
		`CREATE TABLE content_objects(sha256 TEXT PRIMARY KEY)`,
		`CREATE TABLE media_asset_sources(source_id TEXT PRIMARY KEY, content_sha256 TEXT)`,
		`CREATE TABLE source_identity_registry(source_type TEXT, source_key TEXT, content_sha256 TEXT, PRIMARY KEY(source_type, source_key))`,
		`CREATE TABLE canonical_mutations(command_id TEXT PRIMARY KEY, idempotency_key TEXT UNIQUE, request_hash TEXT, status TEXT, result_json TEXT, created_at TEXT, completed_at TEXT, error_message TEXT, registry_seq INTEGER DEFAULT 0, outbox_event_id INTEGER DEFAULT 0)`,
		`CREATE TABLE performance_runs(run_id TEXT PRIMARY KEY, job_id TEXT, root_job_id TEXT, status TEXT, started_at TEXT)`,
		`CREATE TABLE performance_steps(step_id TEXT PRIMARY KEY, run_id TEXT, name TEXT, status TEXT, started_at TEXT)`,
		`CREATE TABLE performance_artifacts(artifact_id TEXT PRIMARY KEY, run_id TEXT, kind TEXT, sha256 TEXT, created_at TEXT)`,
		`CREATE TABLE benchmark_workloads(workload_id TEXT, version TEXT, input_manifest_sha256 TEXT, created_at TEXT, PRIMARY KEY(workload_id,version))`,
	}
	for _, version := range []int{194, 197, 198, 199, 200, 202, 203, 206} {
		statements = append(statements, "INSERT INTO schema_migrations VALUES ("+itoa(version)+",'', '')")
	}
	if registrySeq > 0 {
		statements = append(statements, "INSERT INTO registry_events(seq) VALUES ("+itoa(registrySeq)+")")
	}
	statements = append(statements, "INSERT INTO projection_registry VALUES ("+itoa(projectionSeq)+",'ACTIVE')")
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	for _, version := range []int{194, 197, 198, 199, 200, 202, 203, 206} {
		filename, content, err := currentMigration(version)
		if err != nil {
			t.Fatalf("current migration %d: %v", version, err)
		}
		if _, err := db.Exec(`UPDATE schema_migrations SET filename=?, checksum=? WHERE version=?`, filename, sha256Hex(content), version); err != nil {
			t.Fatalf("migration ledger %d: %v", version, err)
		}
	}
	return db
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	if value < 0 {
		return "-" + itoa(-value)
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
