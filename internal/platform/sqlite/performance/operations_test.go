package performance

import (
	"context"
	"database/sql"
	"testing"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"

	_ "github.com/mattn/go-sqlite3"
)

const operationsDDL = `CREATE TABLE performance_operations (
    operation_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    step_id TEXT NOT NULL,
    operation TEXT NOT NULL,
    source_sha256 TEXT NOT NULL DEFAULT '',
    source_duration_ms INTEGER NOT NULL DEFAULT 0,
    source_size_bytes INTEGER NOT NULL DEFAULT 0,
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    fps REAL NOT NULL DEFAULT 0,
    input_codec TEXT NOT NULL DEFAULT '',
    output_codec TEXT NOT NULL DEFAULT '',
    elapsed_ms INTEGER NOT NULL DEFAULT 0,
    cpu_user_ms INTEGER NOT NULL DEFAULT 0,
    cpu_system_ms INTEGER NOT NULL DEFAULT 0,
    output_size_bytes INTEGER NOT NULL DEFAULT 0,
    cache_hit INTEGER NOT NULL DEFAULT 0,
    strategy TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
)`

func newOperationsStore(t *testing.T) *OperationStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(operationsDDL); err != nil {
		t.Fatal(err)
	}
	store, err := NewOperationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func boundRunCtx(t *testing.T) context.Context {
	t.Helper()
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{
		RunID: "run-1", JobID: "job-1", AttemptID: "attempt-1",
	})
	t.Cleanup(func() { run.Finish() })
	return kernobs.WithRun(context.Background(), run)
}

func TestOperationStoreRecordsAndResolvesIdentity(t *testing.T) {
	store := newOperationsStore(t)
	ctx := boundRunCtx(t)
	err := store.RecordOperation(ctx, capperformance.OperationMeasurement{
		Operation:        "normalize",
		ElapsedMS:        18000,
		SourceDurationMS: 60000,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	db := store.db
	var runID, jobID, operation string
	var elapsed, sourceDur int64
	if err := db.QueryRow(`SELECT run_id, job_id, operation, elapsed_ms, source_duration_ms FROM performance_operations`).Scan(&runID, &jobID, &operation, &elapsed, &sourceDur); err != nil {
		t.Fatal(err)
	}
	if runID != "run-1" || jobID != "job-1" {
		t.Fatalf("identity not resolved from context: run=%q job=%q", runID, jobID)
	}
	if operation != "normalize" || elapsed != 18000 || sourceDur != 60000 {
		t.Fatalf("measurement mismatch: op=%q elapsed=%d source=%d", operation, elapsed, sourceDur)
	}
}

func TestOperationStoreRejectsAnonymousOperation(t *testing.T) {
	store := newOperationsStore(t)
	if err := store.RecordOperation(context.Background(), capperformance.OperationMeasurement{}); err == nil {
		t.Fatal("empty operation must be rejected")
	}
}

func TestOperationStoreDefaultsIdentityOutsideRun(t *testing.T) {
	store := newOperationsStore(t)
	if err := store.RecordOperation(context.Background(), capperformance.OperationMeasurement{Operation: "probe"}); err != nil {
		t.Fatal(err)
	}
	var runID, jobID string
	if err := store.db.QueryRow(`SELECT run_id, job_id FROM performance_operations`).Scan(&runID, &jobID); err != nil {
		t.Fatal(err)
	}
	if runID != "" || jobID != "" {
		t.Fatalf("identity outside a run must be empty, got run=%q job=%q", runID, jobID)
	}
}

func TestOperationStatsComputesRTF(t *testing.T) {
	store := newOperationsStore(t)
	ctx := context.Background()
	records := []capperformance.OperationMeasurement{
		{Operation: "normalize", ElapsedMS: 18000, SourceDurationMS: 60000},
		{Operation: "normalize", ElapsedMS: 18000, SourceDurationMS: 60000},
		{Operation: "watermark", ElapsedMS: 3000, SourceDurationMS: 30000},
		{Operation: "probe", ElapsedMS: 100, SourceDurationMS: 0},
	}
	for _, m := range records {
		if err := store.RecordOperation(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := store.OperationStats(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	byOp := map[string]capperformance.OperationStats{}
	for _, s := range stats {
		byOp[s.Operation] = s
	}
	if byOp["normalize"].Runs != 2 || byOp["normalize"].AvgElapsedMS != 18000 || byOp["normalize"].AvgRTF != 0.3 {
		t.Fatalf("normalize stats = %+v, want runs=2 avg=18000 rtf=0.3", byOp["normalize"])
	}
	if byOp["watermark"].Runs != 1 || byOp["watermark"].AvgRTF != 0.1 {
		t.Fatalf("watermark stats = %+v, want runs=1 rtf=0.1", byOp["watermark"])
	}
	if byOp["probe"].AvgRTF != 0 {
		t.Fatalf("probe with no source duration must have rtf 0, got %+v", byOp["probe"])
	}
}

func TestOperationStatsSinceFilter(t *testing.T) {
	store := newOperationsStore(t)
	ctx := context.Background()
	early := capperformance.OperationMeasurement{Operation: "normalize", ElapsedMS: 1, CreatedAt: "2026-08-01T00:00:00Z"}
	late := capperformance.OperationMeasurement{Operation: "normalize", ElapsedMS: 1, CreatedAt: "2026-08-18T00:00:00Z"}
	if err := store.RecordOperation(ctx, early); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperation(ctx, late); err != nil {
		t.Fatal(err)
	}
	stats, err := store.OperationStats(ctx, "2026-08-10T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Runs != 1 {
		t.Fatalf("since filter must keep only the late operation, got %+v", stats)
	}
}
