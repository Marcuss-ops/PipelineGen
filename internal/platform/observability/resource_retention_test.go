package observability

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	_ "github.com/mattn/go-sqlite3"
)

func TestSQLiteRecorder_PersistsAggregateAndAppliesSeparateRetention(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE run_observability (run_id TEXT PRIMARY KEY, job_id TEXT, attempt_id TEXT)`,
		`CREATE TABLE run_resource_reports (run_id TEXT PRIMARY KEY, job_id TEXT, attempt_id TEXT, schema_version INTEGER, started_at TEXT, finished_at TEXT, sample_count INTEGER, report_json TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE run_resource_samples (sample_id TEXT PRIMARY KEY, run_id TEXT, job_id TEXT, attempt_id TEXT, observed_at TEXT, sample_json TEXT, created_at TEXT)`,
		`CREATE TABLE run_resource_aggregates (run_id TEXT PRIMARY KEY, job_id TEXT, attempt_id TEXT, schema_version INTEGER, sample_count INTEGER, first_observed_at TEXT, last_observed_at TEXT, aggregate_json TEXT, created_at TEXT, updated_at TEXT)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO run_observability(run_id,job_id,attempt_id) VALUES ('run-1','job-1','attempt-1')`); err != nil {
		t.Fatal(err)
	}
	recorder := NewSQLiteRecorder(db)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	cpu := 80.0
	report := &kernobs.RunResourceReport{RunID: "run-1", JobID: "job-1", AttemptID: "attempt-1", Samples: []kernobs.ResourceSample{
		{SampleID: "old", ObservedAt: now.Add(-48 * time.Hour), CPUAvgPct: &cpu},
		{SampleID: "new", ObservedAt: now.Add(-time.Hour), CPUAvgPct: &cpu},
	}}
	if err := recorder.SaveResourceReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	var aggregateJSON string
	if err := db.QueryRow(`SELECT aggregate_json FROM run_resource_aggregates WHERE run_id='run-1'`).Scan(&aggregateJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(aggregateJSON, `"sample_count":2`) {
		t.Fatalf("aggregate=%s", aggregateJSON)
	}
	raw, aggregate, err := recorder.ApplyResourceRetention(context.Background(), now, ResourceRetentionPolicy{RawSampleAge: 24 * time.Hour, AggregateAge: 72 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if raw != 1 || aggregate != 0 {
		t.Fatalf("deleted raw=%d aggregate=%d, want 1/0", raw, aggregate)
	}
	var samples int
	if err := db.QueryRow(`SELECT COUNT(*) FROM run_resource_samples`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if samples != 1 {
		t.Fatalf("samples=%d, want 1", samples)
	}
	if _, err := db.Exec(`UPDATE run_resource_aggregates SET updated_at=? WHERE run_id='run-1'`, now.Add(-96*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	_, aggregate, err = recorder.ApplyResourceRetention(context.Background(), now, ResourceRetentionPolicy{RawSampleAge: 24 * time.Hour, AggregateAge: 72 * time.Hour})
	if err != nil || aggregate != 1 {
		t.Fatalf("aggregate deletion=%d err=%v, want 1", aggregate, err)
	}
}
