package jobregistry

import (
	"context"
	"database/sql"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	_ "github.com/mattn/go-sqlite3"
)

func TestRegistryRecordsJobStepsMetricsRelationsAndStats(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE jobs (id TEXT PRIMARY KEY, type TEXT NOT NULL, status TEXT NOT NULL, project TEXT NOT NULL DEFAULT '', video_name TEXT NOT NULL DEFAULT '', active_key TEXT NOT NULL DEFAULT '', correlation_id TEXT NOT NULL DEFAULT '', payload_json TEXT, payload_hash TEXT NOT NULL DEFAULT '', result_json TEXT, error TEXT, worker_id TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, started_at TEXT, completed_at TEXT, project_id TEXT NOT NULL DEFAULT '', video_id TEXT NOT NULL DEFAULT '', parent_job_id TEXT NOT NULL DEFAULT '', root_job_id TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0);
		CREATE TABLE job_steps (step_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, step_name TEXT NOT NULL, step_type TEXT NOT NULL, status TEXT NOT NULL, started_at TEXT, completed_at TEXT, duration_ms INTEGER NOT NULL, input_count INTEGER NOT NULL, output_count INTEGER NOT NULL, input_bytes INTEGER NOT NULL, output_bytes INTEGER NOT NULL, metrics_json TEXT NOT NULL, error_code TEXT NOT NULL, error_message TEXT NOT NULL, created_at TEXT NOT NULL);
		CREATE TABLE job_registry_metrics (metric_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, step_id TEXT, metric_name TEXT NOT NULL, metric_value REAL NOT NULL, unit TEXT NOT NULL, created_at TEXT NOT NULL);
		CREATE TABLE job_asset_relations (job_id TEXT NOT NULL, asset_id TEXT NOT NULL, relation TEXT NOT NULL, ordinal INTEGER NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(job_id,asset_id,relation));
		CREATE TABLE job_registry_events (seq INTEGER PRIMARY KEY AUTOINCREMENT, event_id TEXT UNIQUE NOT NULL, job_id TEXT NOT NULL, event_type TEXT NOT NULL, payload_json TEXT NOT NULL, created_at TEXT NOT NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := r.RecordJob(ctx, capregistry.Job{JobID: "job-1", JobType: "render", Status: "SUCCEEDED", PayloadJSON: `{"video_id":"v1"}`, VideoID: "v1", CreatedAt: "2026-08-12T00:00:00Z", StartedAt: "2026-08-12T00:00:01Z", CompletedAt: "2026-08-12T00:00:03Z"}); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordStep(ctx, capregistry.Step{StepID: "step-1", JobID: "job-1", StepName: "velox_render", Status: "SUCCEEDED", DurationMS: 2000, StartedAt: "2026-08-12T00:00:01Z", CreatedAt: "2026-08-12T00:00:01Z"}); err != nil {
		t.Fatal(err)
	}
	if err := r.RelateAsset(ctx, capregistry.AssetRelation{JobID: "job-1", AssetID: "out-1", Relation: "RENDERED", CreatedAt: "2026-08-12T00:00:03Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AppendEvent(ctx, capregistry.Event{JobID: "job-1", EventType: "JOB_COMPLETED", CreatedAt: "2026-08-12T00:00:03Z"}); err != nil {
		t.Fatal(err)
	}
	stats, err := r.Stats(ctx, "2026-08-11T00:00:00Z", "2026-08-13T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Jobs != 1 || stats.Successful != 1 || stats.VideosRendered != 1 || len(stats.SlowestSteps) != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}
