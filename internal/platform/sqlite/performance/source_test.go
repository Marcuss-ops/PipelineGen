package performance

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	_ "github.com/mattn/go-sqlite3"
)

func testSourceDBs(t *testing.T) (jobs, obs *sql.DB) {
	t.Helper()
	var err error
	jobs, err = sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	obs, err = sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.Exec(`CREATE TABLE job_steps (step_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, step_name TEXT NOT NULL, step_type TEXT NOT NULL, status TEXT NOT NULL, started_at TEXT, completed_at TEXT, duration_ms INTEGER NOT NULL, input_count INTEGER NOT NULL, output_count INTEGER NOT NULL, input_bytes INTEGER NOT NULL, output_bytes INTEGER NOT NULL, metrics_json TEXT NOT NULL, error_code TEXT NOT NULL, error_message TEXT NOT NULL, created_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err = obs.Exec(`CREATE TABLE run_observability (run_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, job_type TEXT NOT NULL, attempt_id TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT, report_json TEXT NOT NULL, workflow_payload_json TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return jobs, obs
}

func TestSourceLoadProjectsRunAudioAndSteps(t *testing.T) {
	jobs, obs := testSourceDBs(t)
	defer jobs.Close()
	defer obs.Close()

	jobID := "job-1"
	report := kernobs.RunReport{
		RunID:       "run-1",
		JobID:       jobID,
		JobType:     "script.generate",
		Status:      kernobs.StatusSucceeded,
		WallTimeMs:  87431,
		QueueWaitMs: 1850,
		Operations: []kernobs.OperationReport{
			{Operation: "generate", Component: "ollama", Provider: "gemma", DurationMs: 18340, Items: 1},
			{Operation: "upload", Stage: "audio_compile", Component: "drive", DurationMs: 2380, Items: 1},
		},
	}
	reportJSON, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}

	checkpoint := struct {
		Result *scriptgeneration.GenerateResult `json:"result,omitempty"`
	}{
		Result: &scriptgeneration.GenerateResult{
			AudioMetrics: &scriptgeneration.AudioPipelineMetrics{
				TTSMS:              12410,
				TimelineCompileMS:  920,
				AudioPlanCompileMS: 31,
				MixMS:              4120,
				AACEncodeMS:        7130,
				ProbeMS:            281,
				HashMS:             144,
				UploadMS:           2380,
				TotalMS:            18400,
				AudioDurationMS:    93650,
				TTSCalls:           14,
			},
		},
	}
	payloadJSON, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	now := "2026-08-14T10:00:00Z"
	if _, err := obs.Exec(`INSERT INTO run_observability (run_id,job_id,job_type,attempt_id,status,created_at,started_at,report_json,workflow_payload_json,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		report.RunID, jobID, report.JobType, "attempt-1", report.Status, now, now, string(reportJSON), string(payloadJSON), now); err != nil {
		t.Fatal(err)
	}

	steps := []struct {
		stepID, name, typ, status, started, completed string
		duration                                      int64
		errMsg                                        string
	}{
		{"step-doc", "DOCUMENT", "phase", "COMPLETED", "2026-08-14T10:00:01Z", "2026-08-14T10:00:02Z", 1241, ""},
		{"step-enq", "VELOX_ENQUEUE", "phase", "COMPLETED", "2026-08-14T10:00:02Z", "2026-08-14T10:00:02Z", 115, ""},
	}
	for i, st := range steps {
		if _, err := jobs.Exec(`INSERT INTO job_steps (step_id,job_id,step_name,step_type,status,started_at,completed_at,duration_ms,input_count,output_count,input_bytes,output_bytes,metrics_json,error_code,error_message,created_at) VALUES (?,?,?,?,?,?,?,?,0,0,0,0,'{}','',?,?)`,
			st.stepID, jobID, st.name, st.typ, st.status, st.started, st.completed, st.duration, st.errMsg, st.started); err != nil {
			t.Fatalf("insert step %d: %v", i, err)
		}
	}

	src, err := NewSource(jobs, obs)
	if err != nil {
		t.Fatal(err)
	}
	run, audio, got, err := src.Load(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}

	if run.JobID != jobID || run.WallTimeMs != 87431 || run.QueueWaitMs != 1850 {
		t.Fatalf("run = %+v", run)
	}
	if len(run.Operations) != 2 || run.Operations[0].Operation != "generate" || run.Operations[1].Operation != "upload" {
		t.Fatalf("operations = %+v", run.Operations)
	}
	if audio.MixMS != 4120 || audio.AACEncodeMS != 7130 || audio.TTSCalls != 14 {
		t.Fatalf("audio = %+v", audio)
	}
	if len(got) != 2 {
		t.Fatalf("steps = %d, want 2", len(got))
	}
	if got[0].Name != "DOCUMENT" || got[0].DurationMS != 1241 {
		t.Fatalf("step[0] = %+v", got[0])
	}
	if got[1].Name != "VELOX_ENQUEUE" || got[1].DurationMS != 115 {
		t.Fatalf("step[1] = %+v", got[1])
	}
	if got[0].StartedAt.IsZero() || got[0].CompletedAt.IsZero() {
		t.Fatalf("step timestamps not parsed: %+v", got[0])
	}
}

func TestSourceLoadRequiresIdentityAndReportsMissingRun(t *testing.T) {
	jobs, obs := testSourceDBs(t)
	defer jobs.Close()
	defer obs.Close()

	src, err := NewSource(jobs, obs)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := src.Load(context.Background(), ""); err == nil {
		t.Fatal("empty job id must fail")
	}
	if _, _, _, err := src.Load(context.Background(), "missing"); err == nil {
		t.Fatal("missing run must fail closed, not return an empty success")
	}
}

func TestSourceLoadHandlesEmptyAudioMetrics(t *testing.T) {
	jobs, obs := testSourceDBs(t)
	defer jobs.Close()
	defer obs.Close()

	jobID := "job-nometric"
	report := kernobs.RunReport{RunID: "run-nometric", JobID: jobID, JobType: "script.generate", Status: kernobs.StatusSucceeded, WallTimeMs: 10}
	reportJSON, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := obs.Exec(`INSERT INTO run_observability (run_id,job_id,job_type,attempt_id,status,created_at,started_at,report_json,workflow_payload_json,updated_at) VALUES (?,?,?,?,?,?,?,?,'{}',?)`,
		report.RunID, jobID, report.JobType, "attempt-nometric", report.Status, now, now, string(reportJSON), now); err != nil {
		t.Fatal(err)
	}

	src, err := NewSource(jobs, obs)
	if err != nil {
		t.Fatal(err)
	}
	_, audio, steps, err := src.Load(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if audio.MixMS != 0 || audio.TTSCalls != 0 {
		t.Fatalf("empty metrics should stay zero, got %+v", audio)
	}
	if len(steps) != 0 {
		t.Fatalf("steps = %d, want 0", len(steps))
	}
}
