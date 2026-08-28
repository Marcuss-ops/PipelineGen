package performance

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestObservabilityEndToEndPersistsAndReadsWithoutRawDuplicates(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE jobs (id TEXT PRIMARY KEY,type TEXT,status TEXT,worker_id TEXT,host TEXT,started_at TEXT,completed_at TEXT,duration_ms INTEGER,result_json TEXT)`,
		`CREATE TABLE job_registry_metrics (metric_id TEXT PRIMARY KEY,job_id TEXT,metric_name TEXT,metric_value REAL,unit TEXT,created_at TEXT)`,
		`CREATE TABLE performance_operations (operation_id TEXT PRIMARY KEY,run_id TEXT,job_id TEXT,operation TEXT,elapsed_ms INTEGER,output_size_bytes INTEGER,cache_hit INTEGER,source_duration_ms INTEGER,created_at TEXT)`,
		`CREATE TABLE resource_observations (observation_id TEXT PRIMARY KEY,run_id TEXT,job_id TEXT,worker_id TEXT,host TEXT,observed_at TEXT,cpu_avg_pct REAL,cpu_peak_pct REAL,rss_peak_bytes INTEGER,gpu_avg_pct REAL,gpu_peak_pct REAL)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	result := map[string]any{"render": map[string]any{"metrics_v2": map[string]any{"renderer_finalize_ms": 7, "drive_publish_ms": 20, "publication_total_ms": 25}}}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO jobs VALUES ('job-e2e','clip_render','SUCCEEDED','worker-1','host-1','2026-08-28T00:00:00Z','2026-08-28T00:00:10Z',10000,?)`, string(resultJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO job_registry_metrics VALUES ('metric-1','job-e2e','queue_wait_ms',500,'ms','2026-08-28T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO resource_observations VALUES ('resource-1','run-e2e','job-e2e','worker-1','host-1',?,70,90,1234,40,80)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	reader, err := NewReportReader(db)
	if err != nil {
		t.Fatal(err)
	}
	report, err := reader.PerformanceReport(context.Background(), "job-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if report.Job.QueueWaitMS != 500 || report.Render.MetricsV2["drive_publish_ms"] != float64(20) {
		t.Fatalf("report=%+v", report)
	}

	if _, err := db.Exec(`INSERT INTO performance_operations VALUES ('obs-1','run-e2e','job-e2e','render',9000,0,1,10000,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO performance_operations VALUES ('obs-1','run-e2e','job-e2e','render',9000,0,1,10000,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM performance_operations WHERE run_id='run-e2e'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("raw operation rows=%d, want 1", count)
	}
}
