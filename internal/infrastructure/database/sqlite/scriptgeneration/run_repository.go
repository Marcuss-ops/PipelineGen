// Package scriptgenrepo provides the SQLite-backed implementation of
// the scriptgeneration.RunRepository port.
//
// The repository persists GenerationRun aggregates into the
// pipeline_runs table. It keeps all I/O and SQL details inside the
// infrastructure layer; the domain package (internal/scriptgeneration)
// remains free of database dependencies.
package scriptgenrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/scriptgeneration"
)

// SQLiteRunRepository is the SQLite-backed implementation of
// scriptgeneration.RunRepository.
type SQLiteRunRepository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewSQLiteRunRepository constructs a SQLiteRunRepository backed by the
// given *sql.DB. Returns an error when db is nil (fail-closed).
func NewSQLiteRunRepository(db *sql.DB, log *zap.Logger) (*SQLiteRunRepository, error) {
	if db == nil {
		return nil, errors.New("scriptgeneration: SQLiteRunRepository requires a non-nil *sql.DB")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &SQLiteRunRepository{db: db, log: log}, nil
}

// Compile-time assertion: SQLiteRunRepository implements the domain port.
var _ scriptgen.RunRepository = (*SQLiteRunRepository)(nil)

// Create persists a new GenerationRun.
func (r *SQLiteRunRepository) Create(ctx context.Context, run *scriptgen.GenerationRun) error {
	if run == nil {
		return errors.New("scriptgeneration: Create: run is nil")
	}

	reqJSON, err := json.Marshal(run.Request)
	if err != nil {
		return fmt.Errorf("scriptgeneration: Create: marshal request: %w", err)
	}

	resultJSON := []byte("null")
	if run.Result != nil {
		resultJSON, err = json.Marshal(run.Result)
		if err != nil {
			return fmt.Errorf("scriptgeneration: Create: marshal result: %w", err)
		}
	}

	nextRetryAt := sql.NullString{}
	if run.NextRetryAt != nil {
		nextRetryAt = sql.NullString{
			String: run.NextRetryAt.Format(time.RFC3339),
			Valid:  true,
		}
	}

	createdAt := run.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := run.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO pipeline_runs (
			id, job_id, idempotency_key, status, current_stage,
			requested_payload_json, result_json,
			error_code, error_message, failed_stage, attempt_count, next_retry_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.ID,
		run.JobID,
		run.Request.IdempotencyKey,
		string(run.Status),
		string(run.CurrentStage),
		string(reqJSON),
		string(resultJSON),
		run.ErrorCode,
		run.ErrorMessage,
		string(run.FailedStage),
		run.AttemptCount,
		nextRetryAt,
		createdAt.Format(time.RFC3339),
		updatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("scriptgeneration: Create(run_id=%q): %w", run.ID, err)
	}
	return nil
}

// Get retrieves a GenerationRun by its run ID.
func (r *SQLiteRunRepository) Get(ctx context.Context, runID string) (*scriptgen.GenerationRun, error) {
	if runID == "" {
		return nil, errors.New("scriptgeneration: Get: runID is empty")
	}
	row := r.db.QueryRowContext(ctx, selectRunSQL+" WHERE id = ?", runID)
	return r.scanRow(row)
}

// GetByJobID retrieves a GenerationRun by its associated worker job ID.
func (r *SQLiteRunRepository) GetByJobID(ctx context.Context, jobID string) (*scriptgen.GenerationRun, error) {
	if jobID == "" {
		return nil, errors.New("scriptgeneration: GetByJobID: jobID is empty")
	}
	row := r.db.QueryRowContext(ctx, selectRunSQL+" WHERE job_id = ? LIMIT 1", jobID)
	return r.scanRow(row)
}

// UpdateStage persists the current stage and status atomically.
func (r *SQLiteRunRepository) UpdateStage(ctx context.Context, runID string, status scriptgen.RunStatus, stage scriptgen.Stage) error {
	if runID == "" {
		return errors.New("scriptgeneration: UpdateStage: runID is empty")
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE pipeline_runs
		SET status = ?, current_stage = ?, updated_at = ?
		WHERE id = ?
	`, string(status), string(stage), time.Now().UTC().Format(time.RFC3339), runID)
	if err != nil {
		return fmt.Errorf("scriptgeneration: UpdateStage(run_id=%q): %w", runID, err)
	}
	return nil
}

// FailRun persists failure metadata for a run atomically.
func (r *SQLiteRunRepository) FailRun(ctx context.Context, input scriptgen.FailRunInput) error {
	if input.RunID == "" {
		return errors.New("scriptgeneration: FailRun: RunID is empty")
	}

	nextRetryAt := sql.NullString{}
	if input.NextRetryAt != nil {
		nextRetryAt = sql.NullString{
			String: input.NextRetryAt.Format(time.RFC3339),
			Valid:  true,
		}
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE pipeline_runs
		SET status = ?,
		    current_stage = ?,
		    error_code = ?,
		    error_message = ?,
		    failed_stage = ?,
		    attempt_count = ?,
		    next_retry_at = ?,
		    updated_at = ?
		WHERE id = ?
	`,
		string(scriptgen.RunStatusFailed),
		string(input.FailedStage),
		input.ErrorCode,
		input.ErrorMessage,
		string(input.FailedStage),
		input.AttemptCount,
		nextRetryAt,
		time.Now().UTC().Format(time.RFC3339),
		input.RunID,
	)
	if err != nil {
		return fmt.Errorf("scriptgeneration: FailRun(run_id=%q): %w", input.RunID, err)
	}
	return nil
}

// SavePartialResult persists intermediate result data so a retry can resume
// from the checkpoint.
func (r *SQLiteRunRepository) SavePartialResult(ctx context.Context, runID string, result *scriptgen.GenerateResult) error {
	if runID == "" {
		return errors.New("scriptgeneration: SavePartialResult: runID is empty")
	}
	if result == nil {
		return errors.New("scriptgeneration: SavePartialResult: result is nil")
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("scriptgeneration: SavePartialResult: marshal result: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		UPDATE pipeline_runs
		SET result_json = ?, updated_at = ?
		WHERE id = ?
	`, string(resultJSON), time.Now().UTC().Format(time.RFC3339), runID)
	if err != nil {
		return fmt.Errorf("scriptgeneration: SavePartialResult(run_id=%q): %w", runID, err)
	}
	return nil
}

// selectRunSQL is the canonical SELECT for a full GenerationRun row.
const selectRunSQL = `
	SELECT
		id, job_id, idempotency_key, status, current_stage,
		requested_payload_json, result_json,
		error_code, error_message, failed_stage, attempt_count, next_retry_at,
		created_at, updated_at
	FROM pipeline_runs
`

// scanRow deserializes a pipeline_runs row into a GenerationRun.
// parseTimeOrNow parses a timestamp string. It accepts both RFC3339 and
// the SQLite datetime format ("2006-01-02 15:04:05") so rows created by SQL
// defaults or by the repository both deserialize correctly.
func parseTimeOrNow(s string) time.Time {
	if s == "" {
		return time.Now().UTC()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	return time.Now().UTC()
}

func (r *SQLiteRunRepository) scanRow(row *sql.Row) (*scriptgen.GenerationRun, error) {
	var id, jobID, idempotencyKey, status, currentStage string
	var requestedPayloadJSON, resultJSON sql.NullString
	var errorCode, errorMessage, failedStage sql.NullString
	var attemptCount int
	var nextRetryAt sql.NullString
	var createdAtStr, updatedAtStr string

	err := row.Scan(
		&id, &jobID, &idempotencyKey, &status, &currentStage,
		&requestedPayloadJSON, &resultJSON,
		&errorCode, &errorMessage, &failedStage, &attemptCount, &nextRetryAt,
		&createdAtStr, &updatedAtStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scriptgeneration: scanRow: %w", err)
	}

	var req scriptgen.GenerateRequest
	if requestedPayloadJSON.Valid && requestedPayloadJSON.String != "" && requestedPayloadJSON.String != "{}" {
		if err := json.Unmarshal([]byte(requestedPayloadJSON.String), &req); err != nil {
			r.log.Warn("scriptgeneration: scanRow: unmarshal request failed",
				zap.Error(err),
			)
		}
	}
	// Ensure the idempotency key read from the dedicated column is
	// reflected in the in-memory request (defensive: the JSON may be
	// stale if the column was backfilled separately).
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = idempotencyKey
	}

	var result *scriptgen.GenerateResult
	if resultJSON.Valid && resultJSON.String != "" && resultJSON.String != "null" {
		var res scriptgen.GenerateResult
		if err := json.Unmarshal([]byte(resultJSON.String), &res); err != nil {
			r.log.Warn("scriptgeneration: scanRow: unmarshal result failed",
				zap.Error(err),
			)
		} else {
			result = &res
		}
	}

	var nextRetry *time.Time
	if nextRetryAt.Valid && nextRetryAt.String != "" {
		if t, err := time.Parse(time.RFC3339, nextRetryAt.String); err == nil {
			nextRetry = &t
		}
	}

	createdAt := parseTimeOrNow(createdAtStr)
	updatedAt := parseTimeOrNow(updatedAtStr)

	return &scriptgen.GenerationRun{
		ID:           id,
		JobID:        jobID,
		Request:      req,
		Status:       scriptgen.RunStatus(status),
		CurrentStage: scriptgen.Stage(currentStage),
		Result:       result,
		ErrorCode:    errorCode.String,
		ErrorMessage: errorMessage.String,
		FailedStage:  scriptgen.Stage(failedStage.String),
		AttemptCount: attemptCount,
		NextRetryAt:  nextRetry,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}, nil
}
