package performance

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestReportReaderCompareRuns(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:comparison-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ddls := []string{
		`CREATE TABLE performance_runs (run_id TEXT PRIMARY KEY,job_id TEXT,wall_ms INTEGER,started_at TEXT)`,
		`CREATE TABLE benchmark_batches (batch_id TEXT PRIMARY KEY,run_id TEXT,worker_slot_count INTEGER)`,
		`CREATE TABLE benchmark_batch_jobs (batch_id TEXT,job_id TEXT,run_id TEXT,started_at TEXT,completed_at TEXT)`,
		`CREATE TABLE resource_observations (observation_id TEXT PRIMARY KEY,run_id TEXT,cpu_avg_pct REAL,cpu_peak_pct REAL,rss_peak_bytes INTEGER,gpu_avg_pct REAL,gpu_peak_pct REAL)`,
		`CREATE TABLE performance_operations (operation_id TEXT PRIMARY KEY,run_id TEXT,elapsed_ms INTEGER,source_duration_ms INTEGER,cache_hit INTEGER)`,
	}
	for _, ddl := range ddls {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Exec(`INSERT INTO performance_runs VALUES ('run-1','job-1',10000,'2026-08-28T00:00:00Z'); INSERT INTO benchmark_batches VALUES ('batch-1','run-1',2); INSERT INTO benchmark_batch_jobs VALUES ('batch-1','job-a','run-1','2026-08-28T00:00:00Z','2026-08-28T00:00:05Z'); INSERT INTO benchmark_batch_jobs VALUES ('batch-1','job-b','run-1','2026-08-28T00:00:01Z','2026-08-28T00:00:06Z'); INSERT INTO resource_observations VALUES ('res-1','run-1',70,90,1234,50,80); INSERT INTO performance_operations VALUES ('op-1','run-1',3200,10000,1); INSERT INTO performance_operations VALUES ('op-2','run-1',1800,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewReportReader(db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.CompareRuns(context.Background(), []string{"run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("runs=%d", len(got))
	}
	c := got[0]
	if c.CPUAvgPct != 70 || c.CPUPeakPct != 90 || c.RSSPeakBytes != 1234 || c.GPUAvgPct != 50 || c.GPUPeakPct != 80 {
		t.Fatalf("resources=%+v", c)
	}
	if c.RTF != .5 || c.CacheRatio != .5 {
		t.Fatalf("derived=%+v", c)
	}
	if c.WorkerSlotCount != 2 {
		t.Fatalf("slots=%d", c.WorkerSlotCount)
	}
	if c.Concurrency < .499 || c.Concurrency > .501 {
		t.Fatalf("concurrency=%v, want 0.5", c.Concurrency)
	}
	if c.ScalingEfficiency < .249 || c.ScalingEfficiency > .251 {
		t.Fatalf("scaling efficiency=%v, want 0.25", c.ScalingEfficiency)
	}
}
