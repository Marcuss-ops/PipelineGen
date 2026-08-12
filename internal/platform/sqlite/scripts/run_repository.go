package scripts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"go.uber.org/zap"
)

type workflowCheckpoint struct {
	Request      scriptgen.GenerateRequest `json:"request"`
	Result       *scriptgen.GenerateResult `json:"result,omitempty"`
	Status       scriptgen.RunStatus       `json:"status"`
	CurrentStage scriptgen.Stage           `json:"current_stage"`
	ErrorCode    string                    `json:"error_code,omitempty"`
	ErrorMessage string                    `json:"error_message,omitempty"`
	FailedStage  scriptgen.Stage           `json:"failed_stage,omitempty"`
	AttemptCount int                       `json:"attempt_count"`
	NextRetryAt  *time.Time                `json:"next_retry_at,omitempty"`
}

type SQLiteRunRepository struct {
	db  *sql.DB
	log *zap.Logger
}

func NewSQLiteRunRepository(db *sql.DB, log *zap.Logger) (*SQLiteRunRepository, error) {
	if db == nil {
		return nil, errors.New("scriptgeneration: canonical run repository requires observability DB")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &SQLiteRunRepository{db: db, log: log}, nil
}

func (r *SQLiteRunRepository) Create(ctx context.Context, run *scriptgen.GenerationRun) error {
	if run == nil || run.ID == "" {
		return errors.New("scriptgeneration: Create requires a run with ID")
	}
	body, err := marshalCheckpoint(run)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	created, updated := run.CreatedAt, run.UpdatedAt
	if created.IsZero() {
		created = now
	}
	if updated.IsZero() {
		updated = now
	}
	jobID := run.JobID
	if jobID == "" {
		jobID = run.ID
	}
	attemptID := run.ID + ":script"
	status := canonicalStatus(run.Status)
	_, err = r.db.ExecContext(ctx, `INSERT INTO job_attempts (attempt_id,job_id,run_id,attempt_number,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?) ON CONFLICT(attempt_id) DO NOTHING`, attemptID, jobID, run.ID, run.AttemptCount+1, status, formatTime(created), formatTime(updated))
	if err != nil {
		return fmt.Errorf("scriptgeneration: create attempt: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO run_observability (run_id,job_id,job_type,attempt_id,status,created_at,started_at,report_json,workflow_payload_json,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET workflow_payload_json=excluded.workflow_payload_json,updated_at=excluded.updated_at`, run.ID, jobID, "script.generate", attemptID, status, formatTime(created), formatTime(created), "{}", string(body), formatTime(updated))
	if err != nil {
		return fmt.Errorf("scriptgeneration: create run: %w", err)
	}
	return nil
}

func (r *SQLiteRunRepository) SetJobID(ctx context.Context, runID, jobID string) error {
	if runID == "" || jobID == "" {
		return errors.New("scriptgeneration: SetJobID requires run and job IDs")
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE job_attempts SET job_id=?,updated_at=? WHERE run_id=?`, jobID, formatTime(time.Now().UTC()), runID); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `UPDATE run_observability SET job_id=?,updated_at=? WHERE run_id=?`, jobID, formatTime(time.Now().UTC()), runID)
	return err
}

func (r *SQLiteRunRepository) Get(ctx context.Context, runID string) (*scriptgen.GenerationRun, error) {
	if runID == "" {
		return nil, errors.New("scriptgeneration: Get requires run ID")
	}
	return r.scan(ctx, `WHERE run_id=?`, runID)
}

func (r *SQLiteRunRepository) GetByJobID(ctx context.Context, jobID string) (*scriptgen.GenerationRun, error) {
	if jobID == "" {
		return nil, errors.New("scriptgeneration: GetByJobID requires job ID")
	}
	return r.scan(ctx, `WHERE job_id=? ORDER BY created_at DESC LIMIT 1`, jobID)
}

func (r *SQLiteRunRepository) GetByIdempotencyKey(ctx context.Context, key string) (*scriptgen.GenerationRun, error) {
	if key == "" {
		return nil, nil
	}
	return r.scan(ctx, `WHERE json_extract(workflow_payload_json,'$.request.idempotency_key')=? ORDER BY created_at DESC LIMIT 1`, key)
}

func (r *SQLiteRunRepository) UpdateStage(ctx context.Context, runID string, status scriptgen.RunStatus, stage scriptgen.Stage) error {
	run, err := r.Get(ctx, runID)
	if err != nil || run == nil {
		return err
	}
	run.Status, run.CurrentStage, run.UpdatedAt = status, stage, time.Now().UTC()
	body, err := marshalCheckpoint(run)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE run_observability SET status=?,workflow_payload_json=?,updated_at=? WHERE run_id=?`, canonicalStatus(status), string(body), formatTime(run.UpdatedAt), runID)
	return err
}

func (r *SQLiteRunRepository) FailRun(ctx context.Context, input scriptgen.FailRunInput) error {
	run, err := r.Get(ctx, input.RunID)
	if err != nil || run == nil {
		return err
	}
	run.Status, run.CurrentStage, run.FailedStage = scriptgen.RunStatusFailed, scriptgen.StageFailed, input.FailedStage
	run.ErrorCode, run.ErrorMessage, run.AttemptCount, run.NextRetryAt, run.UpdatedAt = input.ErrorCode, input.ErrorMessage, input.AttemptCount, input.NextRetryAt, time.Now().UTC()
	body, err := marshalCheckpoint(run)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE run_observability SET status='FAILED',error_code=?,error=?,workflow_payload_json=?,updated_at=? WHERE run_id=?`, input.ErrorCode, input.ErrorMessage, string(body), formatTime(run.UpdatedAt), input.RunID)
	return err
}

func (r *SQLiteRunRepository) SavePartialResult(ctx context.Context, runID string, result *scriptgen.GenerateResult) error {
	run, err := r.Get(ctx, runID)
	if err != nil || run == nil {
		return err
	}
	run.Result = result
	run.UpdatedAt = time.Now().UTC()
	body, err := marshalCheckpoint(run)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE run_observability SET workflow_payload_json=?,updated_at=? WHERE run_id=?`, string(body), formatTime(run.UpdatedAt), runID)
	return err
}

func (r *SQLiteRunRepository) scan(ctx context.Context, suffix, arg string) (*scriptgen.GenerationRun, error) {
	var id, jobID, status, created, updated, payload string
	var started, finished sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT run_id,job_id,status,created_at,updated_at,started_at,finished_at,workflow_payload_json FROM run_observability `+suffix, arg).Scan(&id, &jobID, &status, &created, &updated, &started, &finished, &payload)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cp workflowCheckpoint
	if err := json.Unmarshal([]byte(payload), &cp); err != nil {
		return nil, fmt.Errorf("decode script workflow checkpoint: %w", err)
	}
	createdAt, err := parseTime(created)
	if err != nil {
		return nil, err
	}
	updatedAt, err := parseTime(updated)
	if err != nil {
		return nil, err
	}
	run := &scriptgen.GenerationRun{ID: id, JobID: jobID, Request: cp.Request, Status: cp.Status, CurrentStage: cp.CurrentStage, Result: cp.Result, ErrorCode: cp.ErrorCode, ErrorMessage: cp.ErrorMessage, FailedStage: cp.FailedStage, AttemptCount: cp.AttemptCount, NextRetryAt: cp.NextRetryAt, CreatedAt: createdAt, UpdatedAt: updatedAt}
	if run.Status == "" {
		run.Status = scriptStatus(status)
	}
	if run.CurrentStage == "" {
		run.CurrentStage = scriptgen.StageWorkerQueued
	}
	return run, nil
}

func marshalCheckpoint(run *scriptgen.GenerationRun) ([]byte, error) {
	return json.Marshal(workflowCheckpoint{Request: run.Request, Result: run.Result, Status: run.Status, CurrentStage: run.CurrentStage, ErrorCode: run.ErrorCode, ErrorMessage: run.ErrorMessage, FailedStage: run.FailedStage, AttemptCount: run.AttemptCount, NextRetryAt: run.NextRetryAt})
}
func canonicalStatus(s scriptgen.RunStatus) string {
	if s == scriptgen.RunStatusCompleted {
		return "SUCCEEDED"
	}
	if s == scriptgen.RunStatusFailed {
		return "FAILED"
	}
	return "RUNNING"
}
func scriptStatus(s string) scriptgen.RunStatus {
	if s == "SUCCEEDED" {
		return scriptgen.RunStatusCompleted
	}
	if s == "FAILED" {
		return scriptgen.RunStatusFailed
	}
	return scriptgen.RunStatusRunning
}
func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func parseTime(v string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		t, err = time.Parse("2006-01-02 15:04:05", v)
	}
	return t, err
}

var _ scriptgen.RunRepository = (*SQLiteRunRepository)(nil)
