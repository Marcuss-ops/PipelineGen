package observability

import (
	"context"
	"database/sql"
	"testing"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	_ "github.com/mattn/go-sqlite3"
)

func resourceReportSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	testObservabilitySchema(t, db)
	for _, stmt := range []string{
		`CREATE TABLE run_resource_reports (run_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, attempt_id TEXT NOT NULL UNIQUE, schema_version INTEGER NOT NULL, started_at TEXT, finished_at TEXT, sample_count INTEGER NOT NULL DEFAULT 0, report_json TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE run_resource_samples (sample_id TEXT PRIMARY KEY, run_id TEXT NOT NULL, job_id TEXT NOT NULL, attempt_id TEXT NOT NULL, observed_at TEXT NOT NULL, sample_json TEXT NOT NULL, created_at TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLiteRecorderSaveResourceReportRoundTripAndIdempotency(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	resourceReportSchema(t, db)
	recorder := NewSQLiteRecorder(db)
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	run := &kernobs.RunReport{
		RunID: "run-resource", JobID: "job-resource", JobType: "benchmark", AttemptID: "attempt-resource",
		Status: kernobs.StatusRunning, CreatedAt: now, StartedAt: now,
	}
	if err := recorder.StartReport(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	report := &kernobs.RunResourceReport{
		RunID: "run-resource", JobID: "job-resource", AttemptID: "attempt-resource",
		StartedAt: now, FinishedAt: now.Add(2 * time.Second),
		Samples: []kernobs.ResourceSample{
			{SampleID: "sample-1", ObservedAt: now, CPUAvgPct: func() *float64 { v := 74.5; return &v }()},
			{SampleID: "sample-2", ObservedAt: now.Add(500 * time.Millisecond), RSSPeakBytes: func() *int64 { v := int64(1024); return &v }()},
		},
	}
	if err := recorder.SaveResourceReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	// Replaying the same report must converge without duplicating samples.
	if err := recorder.SaveResourceReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	var reportVersion, sampleCount, sampleRows int
	if err := db.QueryRow(`SELECT schema_version,sample_count FROM run_resource_reports WHERE run_id=?`, report.RunID).Scan(&reportVersion, &sampleCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM run_resource_samples WHERE run_id=?`, report.RunID).Scan(&sampleRows); err != nil {
		t.Fatal(err)
	}
	if reportVersion != kernobs.RunResourceReportSchemaVersion || sampleCount != 2 || sampleRows != 2 {
		t.Fatalf("stored resource report=%d/%d/%d", reportVersion, sampleCount, sampleRows)
	}
	var sampleJSON string
	if err := db.QueryRow(`SELECT sample_json FROM run_resource_samples WHERE sample_id='sample-1'`).Scan(&sampleJSON); err != nil {
		t.Fatal(err)
	}
	if sampleJSON == "" || sampleJSON == "{}" {
		t.Fatal("raw sample JSON was not persisted")
	}
}

func TestSQLiteRecorderSaveResourceReportRejectsSampleIdentityReuse(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	resourceReportSchema(t, db)
	recorder := NewSQLiteRecorder(db)
	now := time.Now().UTC()
	for _, run := range []*kernobs.RunReport{
		{RunID: "run-a", JobID: "job-a", AttemptID: "attempt-a", Status: kernobs.StatusRunning, CreatedAt: now, StartedAt: now},
		{RunID: "run-b", JobID: "job-b", AttemptID: "attempt-b", Status: kernobs.StatusRunning, CreatedAt: now, StartedAt: now},
	} {
		if err := recorder.StartReport(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
	sample := kernobs.ResourceSample{SampleID: "shared-sample", ObservedAt: now}
	first := &kernobs.RunResourceReport{RunID: "run-a", JobID: "job-a", AttemptID: "attempt-a", Samples: []kernobs.ResourceSample{sample}}
	if err := recorder.SaveResourceReport(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := &kernobs.RunResourceReport{RunID: "run-b", JobID: "job-b", AttemptID: "attempt-b", Samples: []kernobs.ResourceSample{sample}}
	if err := recorder.SaveResourceReport(context.Background(), second); err == nil {
		t.Fatal("sample identity reuse across runs must be rejected")
	}
}
