package performance

import (
	"context"
	"database/sql"
	"testing"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	_ "github.com/mattn/go-sqlite3"
)

func TestConcurrencyStorePersistsAndUpdatesFactsIdempotently(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE benchmark_batches (batch_id TEXT PRIMARY KEY,run_id TEXT NOT NULL DEFAULT '',worker_slot_count INTEGER NOT NULL CHECK(worker_slot_count > 0),started_at TEXT NOT NULL,completed_at TEXT,created_at TEXT NOT NULL); CREATE TABLE benchmark_batch_jobs (batch_id TEXT NOT NULL,job_id TEXT NOT NULL,run_id TEXT NOT NULL DEFAULT '',worker_slot INTEGER,queued_at TEXT,started_at TEXT NOT NULL,completed_at TEXT,status TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,PRIMARY KEY(batch_id,job_id))`); err != nil {
		t.Fatal(err)
	}
	store, err := NewConcurrencyStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	batch := capBatch("batch-1")
	if err := store.RecordBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	batch.WorkerSlotCount = 4
	batch.CompletedAt = "2026-08-28T00:00:10Z"
	if err := store.RecordBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	job := capJob("batch-1", "job-1")
	if err := store.RecordBatchJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	job.Status = "SUCCEEDED"
	job.CompletedAt = "2026-08-28T00:00:09Z"
	if err := store.RecordBatchJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	var slots int
	if err := db.QueryRow(`SELECT worker_slot_count FROM benchmark_batches WHERE batch_id='batch-1'`).Scan(&slots); err != nil {
		t.Fatal(err)
	}
	if slots != 4 {
		t.Fatalf("slots=%d, want 4", slots)
	}
	var count, status int
	if err := db.QueryRow(`SELECT COUNT(*), SUM(status='SUCCEEDED') FROM benchmark_batch_jobs WHERE batch_id='batch-1'`).Scan(&count, &status); err != nil {
		t.Fatal(err)
	}
	if count != 1 || status != 1 {
		t.Fatalf("jobs count/status=%d/%d, want 1/1", count, status)
	}
}

func capBatch(id string) capperformance.BatchConcurrency {
	return capperformance.BatchConcurrency{BatchID: id, RunID: "run-1", WorkerSlotCount: 2, StartedAt: "2026-08-28T00:00:00Z", CreatedAt: "2026-08-28T00:00:00Z"}
}
func capJob(batch, job string) capperformance.BatchJobInterval {
	slot := 1
	return capperformance.BatchJobInterval{BatchID: batch, JobID: job, RunID: "run-1", WorkerSlot: &slot, QueuedAt: "2026-08-28T00:00:00Z", StartedAt: "2026-08-28T00:00:01Z", Status: "RUNNING", CreatedAt: "2026-08-28T00:00:01Z"}
}
