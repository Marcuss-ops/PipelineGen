package observability

import (
	"context"
	"database/sql"
	"testing"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	_ "github.com/mattn/go-sqlite3"
)

// TestSQLiteRecorder_SaveReportProjectsActiveMS pins the durable projection
// that closes the measured "active_ms is never populated" instrumentation
// gap: active = wall − blocked (queue_wait sits outside the wall), clamped at
// zero. The kernel report JSON contract (active_ms omitted until interval
// union) stays untouched — this is the SQLite column projection only.
func TestSQLiteRecorder_SaveReportProjectsActiveMS(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testObservabilitySchema(t, db)
	recorder := NewSQLiteRecorder(db)

	now := time.Now().UTC()
	report := &kernobs.RunReport{
		RunID: "run-active", JobID: "job-active", JobType: "script.generate",
		AttemptID: "attempt-active", Status: kernobs.StatusRunning,
		CreatedAt: now, StartedAt: now,
	}
	if err := recorder.StartReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}

	finished := now.Add(30 * time.Second)
	report.Status = kernobs.StatusSucceeded
	report.FinishedAt = finished
	report.WallTimeMs = 30_000
	report.BlockedMs = 11_500
	if err := recorder.SaveReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}

	var activeMS int64
	if err := db.QueryRow(`SELECT active_ms FROM run_observability WHERE run_id=?`, report.RunID).Scan(&activeMS); err != nil {
		t.Fatal(err)
	}
	if activeMS != 18_500 {
		t.Fatalf("active_ms = %d, want 18500 (30000 wall - 11500 blocked)", activeMS)
	}
}

func TestSQLiteRecorder_SaveReportClampsNegativeActiveMS(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testObservabilitySchema(t, db)
	recorder := NewSQLiteRecorder(db)

	now := time.Now().UTC()
	report := &kernobs.RunReport{
		RunID: "run-clamp", JobID: "job-clamp", JobType: "script.generate",
		AttemptID: "attempt-clamp", Status: kernobs.StatusRunning,
		CreatedAt: now, StartedAt: now,
	}
	if err := recorder.StartReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}

	finished := now.Add(time.Second)
	report.Status = kernobs.StatusSucceeded
	report.FinishedAt = finished
	report.WallTimeMs = 1_000
	// Clock-skew shaped input: blocked intervals exceed the wall.
	report.BlockedMs = 4_000
	if err := recorder.SaveReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}

	var activeMS int64
	if err := db.QueryRow(`SELECT active_ms FROM run_observability WHERE run_id=?`, report.RunID).Scan(&activeMS); err != nil {
		t.Fatal(err)
	}
	if activeMS != 0 {
		t.Fatalf("active_ms = %d, want 0 (never negative)", activeMS)
	}
}
