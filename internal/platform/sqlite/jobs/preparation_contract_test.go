package jobs

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

func TestPreparationSchema_ConstraintsAndIndexes(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), `
CREATE TABLE preparation_dependencies (
 job_id TEXT NOT NULL, unit_id TEXT NOT NULL, depends_on_unit_id TEXT NOT NULL,
 dependency_kind TEXT NOT NULL DEFAULT 'HARD' CHECK (dependency_kind IN ('HARD','SOFT')),
 created_at TEXT NOT NULL, PRIMARY KEY(job_id,unit_id,depends_on_unit_id));
CREATE INDEX idx_preparation_dependencies_upstream ON preparation_dependencies(job_id,unit_id);
CREATE INDEX idx_preparation_dependencies_downstream ON preparation_dependencies(job_id,depends_on_unit_id);
CREATE TABLE preparation_attempts (attempt_id TEXT PRIMARY KEY, execution_mode TEXT NOT NULL CHECK(execution_mode IN ('SPECULATIVE','ACTIVE','ADOPTION_CHECK')), status TEXT NOT NULL CHECK(status IN ('RUNNING','READY','FAILED','CANCELLED','PREEMPTED','HIT')));
`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO preparation_dependencies(job_id,unit_id,depends_on_unit_id,created_at) VALUES ('j','u','d','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO preparation_dependencies(job_id,unit_id,depends_on_unit_id,created_at) VALUES ('j','u','d','now')`); err == nil {
		t.Fatal("duplicate dependency accepted")
	}
	if _, err := db.Exec(`INSERT INTO preparation_dependencies(job_id,unit_id,depends_on_unit_id,dependency_kind,created_at) VALUES ('j','u2','d','INVALID','now')`); err == nil {
		t.Fatal("invalid dependency kind accepted")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name IN ('idx_preparation_dependencies_upstream','idx_preparation_dependencies_downstream')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("dependency indexes=%d, want 2", count)
	}
}

func TestPreparationAttempt_RecordsAndFencesWorker(t *testing.T) {
	db := newPreparationStoreTestDB(t)
	if _, err := db.Exec(`CREATE TABLE preparation_attempts (
		attempt_id TEXT PRIMARY KEY, unit_fingerprint TEXT NOT NULL, trigger_job_id TEXT NOT NULL DEFAULT '',
		worker_id TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '', execution_mode TEXT NOT NULL,
		resource_class TEXT NOT NULL, scheduler_priority REAL NOT NULL DEFAULT 0, status TEXT NOT NULL,
		expected_work_ms INTEGER NOT NULL DEFAULT 0, workload_dimension TEXT NOT NULL DEFAULT '', workload_amount REAL NOT NULL DEFAULT 0, queued_at TEXT, started_at TEXT NOT NULL,
		finished_at TEXT, queue_wait_ms INTEGER NOT NULL DEFAULT 0, wall_ms INTEGER NOT NULL DEFAULT 0,
		singleflight_wait_ms INTEGER NOT NULL DEFAULT 0, bytes_read INTEGER NOT NULL DEFAULT 0,
		bytes_written INTEGER NOT NULL DEFAULT 0, network_rx_bytes INTEGER NOT NULL DEFAULT 0,
		network_tx_bytes INTEGER NOT NULL DEFAULT 0, cache_hit INTEGER NOT NULL DEFAULT 0,
		preempted_by_active INTEGER NOT NULL DEFAULT 0, estimated_saved_ms INTEGER NOT NULL DEFAULT 0,
		error_code TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	store := NewSQLiteStore(db, zap.NewNop())
	attemptID, err := store.StartPreparationAttempt(context.Background(), PreparationAttemptInput{UnitFingerprint: "fp", WorkerID: "worker-a", ExecutionMode: "SPECULATIVE", ResourceClass: "CPU_LIGHT"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishPreparationAttempt(context.Background(), attemptID, "worker-b", "READY", "", "", 10); err == nil {
		t.Fatal("wrong worker finished attempt")
	}
	if err := store.FinishPreparationAttempt(context.Background(), attemptID, "worker-a", "PREEMPTED", "ACTIVE_STARTED", "active job claimed", 10); err != nil {
		t.Fatal(err)
	}
}
