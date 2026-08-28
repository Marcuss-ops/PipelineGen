package performance

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	_ "github.com/mattn/go-sqlite3"
)

// TestConcurrencyReport_DerivesDeterministicBatchReport is the end-to-end pin
// for the deterministic batch concurrency report: batch facts recorded on the
// primary DB + canonical run reports on the observability DB → DeriveBatchReport
// with the end-before-start tie-breaker. Run A finishes EXACTLY when run B
// starts: without the tie-breaker the peak would be 2 or 1 depending on input
// order; the report must always say peak 1, avg 1.0.
func TestConcurrencyReport_DerivesDeterministicBatchReport(t *testing.T) {
	primary, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	if _, err := primary.Exec(`CREATE TABLE benchmark_batches (batch_id TEXT PRIMARY KEY,run_id TEXT NOT NULL DEFAULT '',worker_slot_count INTEGER NOT NULL CHECK(worker_slot_count > 0),started_at TEXT NOT NULL,completed_at TEXT,created_at TEXT NOT NULL); CREATE TABLE benchmark_batch_jobs (batch_id TEXT NOT NULL,job_id TEXT NOT NULL,run_id TEXT NOT NULL DEFAULT '',worker_slot INTEGER,queued_at TEXT,started_at TEXT NOT NULL,completed_at TEXT,status TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,PRIMARY KEY(batch_id,job_id))`); err != nil {
		t.Fatal(err)
	}
	obs, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Close()
	if _, err := obs.Exec(`CREATE TABLE run_observability (run_id TEXT PRIMARY KEY, report_json TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	store, err := NewConcurrencyStore(primary, obs)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	slotA, slotB := 0, 1
	if err := store.RecordBatch(ctx, capperformance.BatchConcurrency{
		BatchID: "batch-det", RunID: "run-a", WorkerSlotCount: 2,
		StartedAt: base.Format(time.RFC3339Nano), CreatedAt: base.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	for _, j := range []capperformance.BatchJobInterval{
		{BatchID: "batch-det", JobID: "job-a", RunID: "run-a", WorkerSlot: &slotA, StartedAt: base.Format(time.RFC3339Nano), Status: "SUCCEEDED", CreatedAt: base.Format(time.RFC3339Nano)},
		{BatchID: "batch-det", JobID: "job-b", RunID: "run-b", WorkerSlot: &slotB, StartedAt: base.Add(10 * time.Millisecond).Format(time.RFC3339Nano), Status: "SUCCEEDED", CreatedAt: base.Format(time.RFC3339Nano)},
	} {
		if err := store.RecordBatchJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}

	// Run reports: run-a 0..10ms, run-b 10..20ms — back-to-back with an
	// EXACT boundary at 10ms. One run carries a render operation so the
	// per-phase derivation is exercised too.
	seedReport(t, obs, "run-a", kernobs.RunReport{
		RunID: "run-a", StartedAt: base, FinishedAt: base.Add(10 * time.Millisecond), ExecutionWallMs: 10,
		Operations: []kernobs.OperationReport{
			{Operation: "render", StartedAt: base, FinishedAt: base.Add(10 * time.Millisecond), DurationMs: 10},
		},
	})
	seedReport(t, obs, "run-b", kernobs.RunReport{
		RunID: "run-b", StartedAt: base.Add(10 * time.Millisecond), FinishedAt: base.Add(20 * time.Millisecond), ExecutionWallMs: 10,
		Operations: []kernobs.OperationReport{
			{Operation: "render", StartedAt: base.Add(10 * time.Millisecond), FinishedAt: base.Add(20 * time.Millisecond), DurationMs: 10},
		},
	})

	rep, err := store.ConcurrencyReport(ctx, "batch-det")
	if err != nil {
		t.Fatal(err)
	}
	if rep.PeakConcurrency != 1 {
		t.Fatalf("peak = %d, want 1 (tie-breaker: back-to-back runs never overlap)", rep.PeakConcurrency)
	}
	if rep.AverageConcurrency < 0.99 || rep.AverageConcurrency > 1.01 {
		t.Fatalf("avg = %f, want 1.0", rep.AverageConcurrency)
	}
	if rep.ParallelismFactor < 0.99 || rep.ParallelismFactor > 1.01 {
		t.Fatalf("parallelism_factor = %f, want 1.0", rep.ParallelismFactor)
	}
	if rep.SlotUtilization < 0.49 || rep.SlotUtilization > 0.51 {
		t.Fatalf("slot_utilization = %f, want 0.5 (peak 1 of 2 slots)", rep.SlotUtilization)
	}
	if rep.ScalingEfficiency < 0.49 || rep.ScalingEfficiency > 0.51 {
		t.Fatalf("scaling_efficiency = %f, want 0.5 (pf 1.0 / 2 slots)", rep.ScalingEfficiency)
	}
	render, ok := rep.Phases["render"]
	if !ok {
		t.Fatalf("phases = %+v, want render", rep.Phases)
	}
	if render.Count != 2 || render.PeakConcurrency != 1 {
		t.Fatalf("render phase = %+v, want count 2 peak 1", render)
	}
}

// TestConcurrencyReport_UnknownBatchAndMissingRuns verifies the no-fabrication
// edges: an unknown batch returns an all-zero report, and batch jobs without a
// stored observability report are skipped (never invented).
func TestConcurrencyReport_UnknownBatchAndMissingRuns(t *testing.T) {
	primary, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	if _, err := primary.Exec(`CREATE TABLE benchmark_batches (batch_id TEXT PRIMARY KEY,run_id TEXT NOT NULL DEFAULT '',worker_slot_count INTEGER NOT NULL CHECK(worker_slot_count > 0),started_at TEXT NOT NULL,completed_at TEXT,created_at TEXT NOT NULL); CREATE TABLE benchmark_batch_jobs (batch_id TEXT NOT NULL,job_id TEXT NOT NULL,run_id TEXT NOT NULL DEFAULT '',worker_slot INTEGER,queued_at TEXT,started_at TEXT NOT NULL,completed_at TEXT,status TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,PRIMARY KEY(batch_id,job_id))`); err != nil {
		t.Fatal(err)
	}
	obs, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Close()
	if _, err := obs.Exec(`CREATE TABLE run_observability (run_id TEXT PRIMARY KEY, report_json TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	store, err := NewConcurrencyStore(primary, obs)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Unknown batch: all-zero report, no error, no fabrication.
	rep, err := store.ConcurrencyReport(ctx, "no-such-batch")
	if err != nil {
		t.Fatal(err)
	}
	if rep.PeakConcurrency != 0 || rep.BatchWallMs != 0 || rep.Phases != nil {
		t.Fatalf("unknown batch report = %+v, want all-zero", rep)
	}

	// Batch whose job has no stored run report: skipped, empty report.
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	slot := 0
	if err := store.RecordBatch(ctx, capperformance.BatchConcurrency{
		BatchID: "batch-empty", WorkerSlotCount: 2, StartedAt: base.Format(time.RFC3339Nano), CreatedAt: base.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordBatchJob(ctx, capperformance.BatchJobInterval{
		BatchID: "batch-empty", JobID: "job-ghost", RunID: "run-ghost", WorkerSlot: &slot,
		StartedAt: base.Format(time.RFC3339Nano), Status: "SUCCEEDED", CreatedAt: base.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	rep, err = store.ConcurrencyReport(ctx, "batch-empty")
	if err != nil {
		t.Fatal(err)
	}
	if rep.PeakConcurrency != 0 || rep.Phases != nil {
		t.Fatalf("batch without reports = %+v, want all-zero (no fabricated intervals)", rep)
	}
}

func seedReport(t *testing.T, obs *sql.DB, runID string, report kernobs.RunReport) {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := obs.Exec(`INSERT INTO run_observability (run_id, report_json) VALUES (?,?)`, runID, string(raw)); err != nil {
		t.Fatal(err)
	}
}
