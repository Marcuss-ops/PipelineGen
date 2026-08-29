package jobs

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

const workObsTestSchema = preparationUnitsTableDDL + `
CREATE TABLE preparation_job_units (
  job_id TEXT NOT NULL, unit_id TEXT NOT NULL, fingerprint TEXT NOT NULL,
  required INTEGER NOT NULL DEFAULT 1, adopted INTEGER NOT NULL DEFAULT 0,
  queue_rank INTEGER, planned_at TEXT NOT NULL, adopted_at TEXT,
  PRIMARY KEY (job_id, unit_id)
);
CREATE TABLE preparation_attempts (
  attempt_id TEXT PRIMARY KEY, unit_fingerprint TEXT NOT NULL,
  trigger_job_id TEXT NOT NULL DEFAULT '', worker_id TEXT NOT NULL DEFAULT '',
  host TEXT NOT NULL DEFAULT '', execution_mode TEXT NOT NULL,
  resource_class TEXT NOT NULL, scheduler_priority REAL NOT NULL DEFAULT 0,	status TEXT NOT NULL, expected_work_ms INTEGER NOT NULL DEFAULT 0,
	workload_dimension TEXT NOT NULL DEFAULT '', workload_amount REAL NOT NULL DEFAULT 0,
	queued_at TEXT, started_at TEXT NOT NULL, finished_at TEXT,
  queue_wait_ms INTEGER NOT NULL DEFAULT 0, wall_ms INTEGER NOT NULL DEFAULT 0,
  singleflight_wait_ms INTEGER NOT NULL DEFAULT 0, bytes_read INTEGER NOT NULL DEFAULT 0,
  bytes_written INTEGER NOT NULL DEFAULT 0, network_rx_bytes INTEGER NOT NULL DEFAULT 0,
  network_tx_bytes INTEGER NOT NULL DEFAULT 0, cache_hit INTEGER NOT NULL DEFAULT 0,
  preempted_by_active INTEGER NOT NULL DEFAULT 0, estimated_saved_ms INTEGER NOT NULL DEFAULT 0,
  error_code TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);`

func TestPreparationStore_ListWorkObservations(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(context.Background(), workObsTestSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	store := NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	// Two unit kinds; three completed attempts + one HIT + one non-completed (excluded).
	seedObs := []struct {
		fp, kind, status string
		wallMS           int64
	}{
		{"fp-tts-1", "tts.synthesize", "READY", 3500},
		{"fp-tts-3", "tts.synthesize", "READY", 3700},
		{"fp-render", "chronon.render", "READY", 11000},
		{"fp-tts-hit", "tts.synthesize", "HIT", 900},
		{"fp-tts-2", "tts.synthesize", "RUNNING", 1000}, // excluded: not terminal
	}
	for i, s := range seedObs {
		fp := s.fp
		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO preparation_units
			(fingerprint, unit_id, unit_kind, job_type, state, created_at, updated_at)
			VALUES (?, ?, ?, 'script.generate','READY', 'now','now')`, fp, "u-"+fp, s.kind); err != nil {
			t.Fatalf("insert unit %d: %v", i, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO preparation_attempts
			(attempt_id, unit_fingerprint, execution_mode, resource_class, status,
			 started_at, finished_at, wall_ms, created_at)
			VALUES (?, ?, 'ACTIVE', 'CPU', ?, 'start', 'finish', ?, 'now')`, "att-"+fp+"-"+s.status, fp, s.status, s.wallMS); err != nil {
			t.Fatalf("insert attempt %d: %v", i, err)
		}
	}

	obs, err := store.ListPreparationWorkObservations(ctx, 100)
	if err != nil {
		t.Fatalf("ListPreparationWorkObservations: %v", err)
	}
	// Only READY/HIT + wall_ms>0 rows, joined to kind.
	if len(obs) != 4 {
		t.Fatalf("len(obs) = %d, want 4 (excluding the RUNNING attempt)", len(obs))
	}
	byKind := map[job.UnitKind]int{}
	for _, o := range obs {
		byKind[o.Kind]++
	}
	if byKind["tts.synthesize"] != 3 {
		t.Fatalf("tts observations = %d, want 3 including HIT", byKind["tts.synthesize"])
	}
	if byKind["chronon.render"] != 1 {
		t.Fatalf("render observations = %d, want 1", byKind["chronon.render"])
	}
}
