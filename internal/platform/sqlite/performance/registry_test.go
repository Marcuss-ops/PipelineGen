package performance

import (
	"context"
	"database/sql"
	"testing"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	_ "github.com/mattn/go-sqlite3"
)

func TestRegistryIsIdempotentAndCorrelatesRun(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE performance_runs (run_id TEXT PRIMARY KEY, job_id TEXT, root_job_id TEXT, video_id TEXT, workload_id TEXT, workload_version TEXT, git_sha TEXT, worker_id TEXT, host_id TEXT, status TEXT, wall_ms INTEGER, cpu_user_ms INTEGER, cpu_system_ms INTEGER, peak_rss_bytes INTEGER, disk_read_bytes INTEGER, disk_write_bytes INTEGER, network_rx_bytes INTEGER, network_tx_bytes INTEGER, metadata_json TEXT, started_at TEXT, completed_at TEXT)`,
		`CREATE TABLE performance_steps (step_id TEXT PRIMARY KEY, run_id TEXT, job_id TEXT, name TEXT, status TEXT, duration_ms INTEGER, input_count INTEGER, output_count INTEGER, input_bytes INTEGER, output_bytes INTEGER, cache_hits INTEGER, cache_misses INTEGER, metadata_json TEXT, started_at TEXT, completed_at TEXT)`,
		`CREATE TABLE performance_artifacts (artifact_id TEXT PRIMARY KEY, run_id TEXT, kind TEXT, sha256 TEXT, size_bytes INTEGER, uri TEXT, created_at TEXT)`,
		`CREATE TABLE benchmark_workloads (workload_id TEXT, version TEXT, input_manifest_sha256 TEXT, parameters_json TEXT, expected_output_sha256 TEXT, created_at TEXT, PRIMARY KEY(workload_id,version))`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	r, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run := capperformance.Run{RunID: "run-1", JobID: "job-1", RootJobID: "root-1", WorkloadID: "comedians_full_audio", WorkloadVersion: "v1", Status: "SUCCEEDED", StartedAt: "2026-08-12T00:00:00Z", CompletedAt: "2026-08-12T00:00:01Z", WallMS: 1000}
	if err := r.RecordRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	runCount := 0
	if err := db.QueryRow(`SELECT COUNT(*) FROM performance_runs`).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 {
		t.Fatalf("run rows=%d", runCount)
	}
	if err := r.RecordStep(ctx, capperformance.Step{StepID: "step-1", RunID: "run-1", JobID: "job-1", Name: "VELOX_RENDER", Status: "SUCCEEDED", StartedAt: run.StartedAt}); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordArtifact(ctx, capperformance.Artifact{ArtifactID: "artifact-1", RunID: "run-1", Kind: "flamegraph", SHA256: "abc", CreatedAt: run.StartedAt}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterWorkload(ctx, capperformance.Workload{WorkloadID: "comedians_full_audio", Version: "v1", InputManifestSHA256: "manifest", CreatedAt: run.StartedAt}); err != nil {
		t.Fatal(err)
	}
}
