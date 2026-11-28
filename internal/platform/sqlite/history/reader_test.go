package history

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestGetRunReportPrefersTheJobBodyOverFinalizeChildren pins the /full timing
// projection: a successful final job records its main script.generate report
// and then one or more finalize.drive_delivery children. The children are newer
// and carry a small report with no stages, so "latest row" projected 5-7 s with
// no critical path instead of the 70-141 s run the caller asks about (r92-r96).
func TestGetRunReportPrefersTheJobBodyOverFinalizeChildren(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "obs.db.sqlite"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE run_observability (
		run_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, job_type TEXT NOT NULL,
		status TEXT NOT NULL, created_at TEXT NOT NULL, report_json TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	const jobID = "job-1"
	mainReport := `{"wall_time_ms":140845,"stages":[{"name":"remote_final_job","duration_ms":140000}]}`
	finalizeReport := `{"wall_time_ms":5038}`
	for _, row := range []struct {
		runID, jobType, created, report string
	}{
		{"run-main", "script.generate", "2026-11-02T10:41:50Z", mainReport},
		{"run-finalize-a", "finalize.drive_delivery.requested.v1", "2026-11-02T10:44:12Z", finalizeReport},
		{"run-finalize-b", "finalize.drive_delivery.requested.v1", "2026-11-02T10:44:13Z", finalizeReport},
	} {
		if _, err := db.Exec(`INSERT INTO run_observability
			(run_id,job_id,job_type,status,created_at,report_json) VALUES (?,?,?,?,?,?)`,
			row.runID, jobID, row.jobType, "SUCCEEDED", row.created, row.report); err != nil {
			t.Fatalf("insert %s: %v", row.runID, err)
		}
	}
	reader, err := NewReader(db, db)
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	raw, err := reader.GetRunReport(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get run report: %v", err)
	}
	if string(raw) != mainReport {
		t.Fatalf("report = %s, want the script.generate report %s", raw, mainReport)
	}
}

// TestGetScriptRunSnapshotPrefersTheSubmissionRow pins PERFLIVE-13: the
// worker's attempt row is newer than the submission row and must not mask the
// checkpoint that carries remote_final_job while the render is still running.
func TestGetScriptRunSnapshotPrefersTheSubmissionRow(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "obs.db.sqlite"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE run_observability (
		run_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, job_type TEXT NOT NULL,
		attempt_id TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL,
		workflow_payload_json TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	const jobID = "job-live-final"
	submissionPayload := `{"status":"RUNNING","current_stage":"FINAL_JOB","result":{"remote_final_job":{"job_id":"master-live","status":"QUEUED"}}}`
	for _, row := range []struct{ runID, attemptID, created, payload string }{
		{"run-sub", "run-sub:script", "2026-11-25T17:00:00Z", submissionPayload},
		{"run-worker", "attempt-worker", "2026-11-25T17:01:00Z", `{}`},
	} {
		if _, err := db.Exec(`INSERT INTO run_observability
			(run_id,job_id,job_type,attempt_id,status,created_at,workflow_payload_json) VALUES (?,?,?,?,?,?,?)`,
			row.runID, jobID, "script.generate", row.attemptID, "RUNNING", row.created, row.payload); err != nil {
			t.Fatalf("insert %s: %v", row.runID, err)
		}
	}
	reader, err := NewReader(db, db)
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	snapshot, err := reader.GetScriptRunSnapshot(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get script run snapshot: %v", err)
	}
	if snapshot == nil || snapshot.RunID != "run-sub" {
		t.Fatalf("snapshot = %+v, want the submission run", snapshot)
	}
	if !strings.Contains(string(snapshot.RemoteFinalJob), "master-live") {
		t.Fatalf("remote_final_job = %s, want the live Master handle", snapshot.RemoteFinalJob)
	}
}

func TestGetScriptRunSnapshotPreservesFailureAttribution(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "obs.db.sqlite"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE run_observability (
		run_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, job_type TEXT NOT NULL,
		attempt_id TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL,
		workflow_payload_json TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	const payload = `{"status":"FAILED","current_stage":"FAILED","failed_stage":"RENDERING_VIDEO","error_code":"LOCALIZED_RENDER_FAILED"}`
	if _, err := db.Exec(`INSERT INTO run_observability
		(run_id,job_id,job_type,attempt_id,status,created_at,workflow_payload_json) VALUES (?,?,?,?,?,?,?)`,
		"run-render-failure", "job-render-failure", "script.generate", "run-render-failure:script", "FAILED", "2026-11-27T17:00:00Z", payload); err != nil {
		t.Fatalf("insert script run: %v", err)
	}
	reader, err := NewReader(db, db)
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	snapshot, err := reader.GetScriptRunSnapshot(context.Background(), "job-render-failure")
	if err != nil {
		t.Fatalf("get script run snapshot: %v", err)
	}
	if snapshot == nil {
		t.Fatal("snapshot is nil")
	}
	if snapshot.CurrentStage != "FAILED" || snapshot.FailedStage != "RENDERING_VIDEO" || snapshot.ErrorCode != "LOCALIZED_RENDER_FAILED" {
		t.Fatalf("snapshot failure attribution = current=%q failed=%q code=%q", snapshot.CurrentStage, snapshot.FailedStage, snapshot.ErrorCode)
	}
}

// TestGetRunReportFallsBackToTheLatestRunWithoutAJobBody pins that the filter
// is a preference, not a requirement: a job with only finalize children still
// exposes its most recent report.
func TestGetRunReportFallsBackToTheLatestRunWithoutAJobBody(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "obs.db.sqlite"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE run_observability (
		run_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, job_type TEXT NOT NULL,
		status TEXT NOT NULL, created_at TEXT NOT NULL, report_json TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for _, row := range []struct{ runID, created, report string }{
		{"run-old", "2026-11-02T10:00:00Z", `{"wall_time_ms":1}`},
		{"run-new", "2026-11-02T10:01:00Z", `{"wall_time_ms":2}`},
	} {
		if _, err := db.Exec(`INSERT INTO run_observability
			(run_id,job_id,job_type,status,created_at,report_json) VALUES (?,?,?,?,?,?)`,
			row.runID, "job-2", "finalize.drive_delivery.requested.v1", "SUCCEEDED", row.created, row.report); err != nil {
			t.Fatalf("insert %s: %v", row.runID, err)
		}
	}
	reader, err := NewReader(db, db)
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	raw, err := reader.GetRunReport(context.Background(), "job-2")
	if err != nil {
		t.Fatalf("get run report: %v", err)
	}
	if string(raw) != `{"wall_time_ms":2}` {
		t.Fatalf("report = %s, want the latest finalize report", raw)
	}
}
