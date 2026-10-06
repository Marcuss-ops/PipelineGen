package observability

import (
	"context"
	"database/sql"
	"testing"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	_ "github.com/mattn/go-sqlite3"
)

// TestSQLiteRecorder_AppendOperationStampsMissingCreatedAt pins the recorder
// fact: owner-measured operations (RenderingGen/Chronon projections) carry no
// created_at, and ingesting them as created_at=” made 33k rows un-dateable —
// every time-window query silently dropped them. The recorder stamps the row
// at write time when the kernel left it empty; an owner-supplied stamp is
// preserved verbatim.
func TestSQLiteRecorder_AppendOperationStampsMissingCreatedAt(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testObservabilitySchema(t, db)
	recorder := NewSQLiteRecorder(db)

	now := time.Now().UTC()
	report := &kernobs.RunReport{RunID: "run-ops", JobID: "job-ops", JobType: "script.generate", AttemptID: "attempt-ops", Status: kernobs.StatusRunning, CreatedAt: now, StartedAt: now}
	if err := recorder.StartReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}

	before := time.Now().UTC().Add(-time.Second)
	// Owner-measured row WITHOUT created_at (the RenderingGen projection shape).
	if err := recorder.AppendOperation(context.Background(), "run-ops", kernobs.OperationReport{
		ObservationID: "obs-owner", Stage: "overlay_render", Component: "renderinggen", Operation: "render",
		Status: kernobs.StageStatusCompleted, DurationMs: 900,
	}); err != nil {
		t.Fatal(err)
	}
	// Owner-measured row WITH an explicit created_at (must be preserved).
	if err := recorder.AppendOperation(context.Background(), "run-ops", kernobs.OperationReport{
		ObservationID: "obs-owned", Stage: "overlay_render", Component: "renderinggen", Operation: "encode",
		Status: kernobs.StageStatusCompleted, DurationMs: 200, CreatedAt: "2026-01-02T03:04:05Z",
	}); err != nil {
		t.Fatal(err)
	}

	var stamped, owned sql.NullString
	if err := db.QueryRow(`SELECT created_at FROM run_operation_observations WHERE observation_id='obs-owner'`).Scan(&stamped); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT created_at FROM run_operation_observations WHERE observation_id='obs-owned'`).Scan(&owned); err != nil {
		t.Fatal(err)
	}
	if !stamped.Valid || stamped.String == "" {
		t.Fatal("missing created_at must be stamped at write time, not stored empty")
	}
	parsed, err := time.Parse(time.RFC3339Nano, stamped.String)
	if err != nil {
		t.Fatalf("stamped created_at is not RFC3339: %q", stamped.String)
	}
	if parsed.Before(before) {
		t.Fatalf("stamped created_at %s predates the test window (started %s)", parsed, before)
	}
	if !owned.Valid || owned.String != "2026-01-02T03:04:05Z" {
		t.Fatalf("owner-supplied created_at must be preserved verbatim, got %q", owned.String)
	}
}
