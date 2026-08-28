package performance

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func reportTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE jobs (id TEXT PRIMARY KEY,type TEXT,status TEXT,worker_id TEXT,host TEXT,started_at TEXT,completed_at TEXT,duration_ms INTEGER,result_json TEXT)`,
		`CREATE TABLE job_registry_metrics (metric_id TEXT PRIMARY KEY,job_id TEXT,metric_name TEXT,metric_value REAL,unit TEXT,created_at TEXT)`,
		`CREATE TABLE performance_operations (operation_id TEXT PRIMARY KEY,job_id TEXT,operation TEXT,elapsed_ms INTEGER,output_size_bytes INTEGER,cache_hit INTEGER,source_duration_ms INTEGER,created_at TEXT)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestReportReaderDerivesAllAnalyticsMetrics(t *testing.T) {
	db := reportTestDB(t)
	_, err := db.Exec(`INSERT INTO jobs VALUES ('job-1','clip_render','SUCCEEDED','worker-1','host-1','2026-08-28T00:00:00Z','2026-08-28T00:00:10Z',10000,?)`, `{"render":{"metrics_v2":{"total_ms":9000,"backend_selected":"ffmpeg"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO job_registry_metrics VALUES
		('m1','job-1','queue_wait_ms',1000,'ms','2026-08-28T00:00:00Z'),
		('m2','job-1','wall_time_ms',10000,'ms','2026-08-28T00:00:01Z'),
		('m3','job-1','prepare_ms',2000,'ms','2026-08-28T00:00:04Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO performance_operations VALUES
		('o1','job-1','render',9000,100,1,10000,'2026-08-28T00:00:02Z'),
		('o2','job-1','prepare',1000,50,0,0,'2026-08-28T00:00:03Z')`)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewReportReader(db)
	if err != nil {
		t.Fatal(err)
	}
	report, err := reader.PerformanceReport(context.Background(), "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if report.Queue.WaitMS != 1000 || report.Job.WallTimeMS != 10000 {
		t.Fatalf("job/queue=%+v/%+v", report.Job, report.Queue)
	}
	if report.Render.MetricsV2["backend_selected"] != "ffmpeg" {
		t.Fatalf("metrics_v2=%v", report.Render.MetricsV2)
	}
	if report.Derived.XRT != 1 || report.Derived.SpeedFactor != 1 {
		t.Fatalf("xRT/speed=%+v, want 1/1", report.Derived)
	}
	if report.Derived.CacheRatio != .5 {
		t.Fatalf("cache ratio=%v, want 0.5", report.Derived.CacheRatio)
	}
	if report.Derived.CriticalPathPercent != 20 {
		t.Fatalf("critical path=%v, want 20", report.Derived.CriticalPathPercent)
	}
	if report.Derived.ParallelismEfficiency != 1 {
		t.Fatalf("parallelism efficiency=%v, want 1", report.Derived.ParallelismEfficiency)
	}
	if report.Derived.ClipsPerMinute != 6 {
		t.Fatalf("clips/min=%v, want 6", report.Derived.ClipsPerMinute)
	}
}

func TestReportReaderDerivedMetricsAreZeroWithoutFacts(t *testing.T) {
	db := reportTestDB(t)
	if _, err := db.Exec(`INSERT INTO jobs VALUES ('job-empty','clip_render','RUNNING','','','','',0,'{}')`); err != nil {
		t.Fatal(err)
	}
	reader, err := NewReportReader(db)
	if err != nil {
		t.Fatal(err)
	}
	report, err := reader.PerformanceReport(context.Background(), "job-empty")
	if err != nil {
		t.Fatal(err)
	}
	if report.Derived.XRT != 0 || report.Derived.SpeedFactor != 0 || report.Derived.CacheRatio != 0 || report.Derived.CriticalPathPercent != 0 || report.Derived.ParallelismEfficiency != 0 || report.Derived.ClipsPerMinute != 0 {
		t.Fatalf("missing facts must produce zero derived metrics: %+v", report.Derived)
	}
}
