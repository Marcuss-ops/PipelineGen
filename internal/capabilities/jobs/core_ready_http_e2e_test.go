package jobs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	historyinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/history"
	runrepo "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"

	_ "github.com/mattn/go-sqlite3"
)

type coreReadyJobService struct{ current *job.Job }

func (s *coreReadyJobService) Enqueue(context.Context, *job.EnqueueRequest) (*job.Job, error) {
	return nil, nil
}
func (s *coreReadyJobService) Get(context.Context, string) (*job.Job, error) { return s.current, nil }
func (s *coreReadyJobService) Cancel(context.Context, string) error          { return nil }
func (s *coreReadyJobService) List(context.Context, job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (s *coreReadyJobService) IsTerminal(job.Status) bool        { return false }
func (s *coreReadyJobService) RegisterHandler(string, any) error { return nil }
func (s *coreReadyJobService) ListEvents(context.Context, string) ([]job.Event, error) {
	return nil, nil
}
func (s *coreReadyJobService) Retry(context.Context, string) (*job.Job, error) { return nil, nil }

var _ job.Service = (*coreReadyJobService)(nil)

// This exercises the exact GET target returned by POST /api/script/generate
// (status_url=/api/jobs/{id}/full), backed by the real SQLite run-ledger reader.
// The core script is durable, but the broker has not yet published all required
// artifacts: the HTTP contract must expose availability without success.
func TestCoreReadyScriptIsAvailableOnCanonicalJobPollingEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE job_attempts (
		attempt_id TEXT PRIMARY KEY,
		job_id TEXT NOT NULL DEFAULT '',
		run_id TEXT NOT NULL DEFAULT '',
		attempt_number INTEGER NOT NULL DEFAULT 1,
		status TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE run_observability (
		run_id TEXT PRIMARY KEY,
		job_id TEXT NOT NULL DEFAULT '',
		job_type TEXT NOT NULL DEFAULT '',
		attempt_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT '',
		started_at TEXT,
		finished_at TEXT,
		report_json TEXT NOT NULL DEFAULT '{}',
		workflow_payload_json TEXT NOT NULL DEFAULT '',
		error_code TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT ''
	)`)
	require.NoError(t, err)

	runs, err := runrepo.NewSQLiteRunRepository(db, zap.NewNop())
	require.NoError(t, err)
	const runID = "run-core-ready"
	run := &scriptgen.GenerationRun{
		ID:           runID,
		Request:      scriptgen.GenerateRequest{IdempotencyKey: "core-ready-http-e2e"},
		Status:       scriptgen.RunStatusRunning,
		CurrentStage: scriptgen.StagePublishingDocuments,
	}
	require.NoError(t, runs.Create(ctx, run))
	require.NoError(t, runs.SetJobID(ctx, runID, "job-core-ready"))
	result := &scriptgen.GenerateResult{
		Output: scriptgen.GenerateOutput{Text: "The script is ready."},
		Scenes: []scriptgen.Scene{{ID: "scene-0", Text: map[scriptgen.Language]string{"en": "The script is ready."}}},
	}
	require.NoError(t, runs.SavePartialResult(ctx, runID, result))
	require.NoError(t, runs.UpdateStage(ctx, runID, scriptgen.RunStatusRunning, scriptgen.StageCoreReady))
	history, err := historyinfra.NewReader(db, db)
	require.NoError(t, err)
	service := &coreReadyJobService{current: &job.Job{
		ID: "job-core-ready", Type: scriptpkg.TypeGenerate, Status: job.StatusRunning,
	}}
	handler := capjobs.NewJobsHandler(service, nil, zap.NewNop())
	handler.SetHistoryReader(history)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/jobs"))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/jobs/job-core-ready/full", nil)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var response struct {
		Status       job.Status `json:"status"`
		CurrentStage string     `json:"current_stage"`
		CoreReady    bool       `json:"core_ready"`
		Script       struct {
			Text      string `json:"text"`
			WordCount int    `json:"word_count"`
		} `json:"script"`
		ScriptRun struct {
			RunID        string `json:"run_id"`
			Status       string `json:"status"`
			CurrentStage string `json:"current_stage"`
		} `json:"script_run"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response), recorder.Body.String())
	require.Equal(t, job.StatusRunning, response.Status, "CORE_READY must not change broker success semantics")
	require.Equal(t, "CORE_READY", response.CurrentStage)
	require.True(t, response.CoreReady)
	require.Equal(t, "run-core-ready", response.ScriptRun.RunID)
	require.Equal(t, "RUNNING", response.ScriptRun.Status)
	require.Equal(t, "CORE_READY", response.ScriptRun.CurrentStage)
	require.Equal(t, "The script is ready.", response.Script.Text)
	savedRun, err := runs.Get(ctx, runID)
	require.NoError(t, err, "the durable run should remain readable after HTTP polling")
	require.Equal(t, scriptgen.StageCoreReady, savedRun.CurrentStage)
	require.Equal(t, scriptgen.RunStatusRunning, savedRun.Status,
		"HTTP polling must not transition the durable run to completed")
}
