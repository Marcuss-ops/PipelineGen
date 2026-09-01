package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ErrOptimisticLockFailed is returned by Transition when the optimistic-lock
// guard (WHERE revision = ?) fails — another worker or operation modified
// the job row between the read and this write.
var ErrOptimisticLockFailed = fmt.Errorf("optimistic lock failed: revision or status changed")

// ErrInvalidColumn is returned by Transition when a caller-supplied column
// name (req.Updates or req.ExtraSets) is not in the allowlist of known
// columns for the jobs table.
var ErrInvalidColumn = fmt.Errorf("invalid column: not in jobs table allowlist")

// allowedJobColumns is the allowlist of column names that can legally appear
// in a Transition SET clause (req.Updates keys and req.ExtraSets snippets).
// Adding a column to the jobs table MUST be mirrored here so the Transition
// guard catches typos and missing-migration gaps at call time rather than
// surfacing a silent "no rows affected" or a SQL syntax error.
var allowedJobColumns = map[string]bool{
	"id": true, "type": true, "status": true, "priority": true,
	"project": true, "video_name": true, "active_key": true,
	"correlation_id": true, "payload_json": true,
	"progress": true, "error": true, "retry_count": true, "max_retries": true,
	"worker_id": true, "lease_id": true, "lease_expiry": true,
	"created_at": true, "updated_at": true, "started_at": true,
	"completed_at": true, "cancelled_at": true, "revision": true,
}

// validateSetColumn returns the bare column name extracted from a SET clause
// like "col = ?", "col = datetime('now')", or "col = excluded.col", and an
// error when the column is not in allowedJobColumns.
func validateSetColumn(clause string) (string, error) {
	// Extract everything before the first '='.
	idx := strings.IndexByte(clause, '=')
	if idx < 0 {
		return "", fmt.Errorf("%w: %q (no '=' separator)", ErrInvalidColumn, clause)
	}
	col := strings.TrimSpace(clause[:idx])
	if col == "" {
		return "", fmt.Errorf("%w: empty column name in %q", ErrInvalidColumn, clause)
	}
	if !allowedJobColumns[col] {
		return col, fmt.Errorf("%w: %q (not in jobs table allowlist)", ErrInvalidColumn, col)
	}
	return col, nil
}

// TransitionRequest carries all the parameters for an atomic job status
// transition. Every field except JobID, ExpectedStatus, and NewStatus is
// optional.
type TransitionRequest struct {
	JobID            string
	ExpectedRevision int
	ExpectedStatus   job.Status
	NewStatus        job.Status

	// WorkerID and LeaseID carry the fencing tokens from the caller.
	WorkerID string
	LeaseID  string

	// Updates carries typed optional column updates. Nil means "none";
	// each non-nil pointer field in JobUpdates is applied to the
	// corresponding column. Prefer this over the legacy Updates map.
	Updates *JobUpdates

	// LegacyUpdates is the pre-Azione-6 untyped map. Deprecated; new
	// callers must use Updates (*JobUpdates) instead. Kept for
	// backward compatibility with callers that have not yet migrated.
	LegacyUpdates map[string]any

	// ExtraSets are raw SQL snippets appended to the SET clause.
	ExtraSets []string

	// Result, when non-nil, is serialised to result_json.
	Result map[string]any

	// Error, when non-nil, is stored in the error column.
	Error *string

	// Progress, when non-nil, is stored in the progress column.
	Progress *int
}

// JobUpdates carries typed optional column updates for a Transition.
// Every field is a pointer — nil means "skip this column"; non-nil means
// "set this column to the pointed-at value". This gives compile-time type
// safety where the legacy Updates map[string]any gave runtime panics.
type JobUpdates struct {
	StartedAt   *time.Time // → started_at
	CompletedAt *time.Time // → completed_at
	CancelledAt *time.Time // → cancelled_at
	LeaseExpiry *time.Time // → lease_expiry
	RetryCount  *int       // → retry_count
	Priority    *int       // → priority
}

// toSetClauses converts non-nil JobUpdates fields into (column = ?) pairs
// with their typed argument values. Time pointers are formatted via
// timeutil.FormatPtrRFC3339.
func (u *JobUpdates) toSetClauses() (clauses []string, args []any) {
	if u == nil {
		return nil, nil
	}
	if u.StartedAt != nil {
		clauses = append(clauses, "started_at = ?")
		args = append(args, timeutil.FormatPtrRFC3339(u.StartedAt))
	}
	if u.CompletedAt != nil {
		clauses = append(clauses, "completed_at = ?")
		args = append(args, timeutil.FormatPtrRFC3339(u.CompletedAt))
	}
	if u.CancelledAt != nil {
		clauses = append(clauses, "cancelled_at = ?")
		args = append(args, timeutil.FormatPtrRFC3339(u.CancelledAt))
	}
	if u.LeaseExpiry != nil {
		clauses = append(clauses, "lease_expiry = ?")
		args = append(args, timeutil.FormatPtrRFC3339(u.LeaseExpiry))
	}
	if u.RetryCount != nil {
		clauses = append(clauses, "retry_count = ?")
		args = append(args, *u.RetryCount)
	}
	if u.Priority != nil {
		clauses = append(clauses, "priority = ?")
		args = append(args, *u.Priority)
	}
	return
}

// Transition atomically executes a guarded status change on the jobs table.
func (r *SQLiteStore) Transition(ctx context.Context, req TransitionRequest) (*job.Job, error) {
	now := time.Now()

	// Build the UPDATE query with parameterised SET clauses.
	setClauses := []string{"status = ?", "updated_at = ?", "revision = revision + 1"}
	args := []any{req.NewStatus, timeutil.FormatRFC3339(now)}

	// Handle typed Updates (preferred path).
	if req.Updates != nil {
		clauses, upArgs := req.Updates.toSetClauses()
		setClauses = append(setClauses, clauses...)
		args = append(args, upArgs...)
	}

	// Handle legacy Updates map (backward compat; deprecated).
	for col, val := range req.LegacyUpdates {
		if _, err := validateSetColumn(col + " = ?"); err != nil {
			return nil, fmt.Errorf("transition %s: %w", req.JobID, err)
		}
		switch v := val.(type) {
		case *time.Time:
			setClauses = append(setClauses, col+" = ?")
			args = append(args, timeutil.FormatPtrRFC3339(v))
		default:
			setClauses = append(setClauses, col+" = ?")
			args = append(args, v)
		}
	}

	var resultPayload string
	// Handle special fields.
	if req.Result != nil {
		resultBytes, _ := json.Marshal(req.Result)
		resultPayload = string(resultBytes)
	}
	if req.Error != nil {
		setClauses = append(setClauses, "error = ?")
		args = append(args, *req.Error)
	}
	if req.Progress != nil {
		setClauses = append(setClauses, "progress = ?")
		args = append(args, *req.Progress)
	}

	// Handle raw ExtraSets.
	for _, clause := range req.ExtraSets {
		if _, err := validateSetColumn(clause); err != nil {
			return nil, fmt.Errorf("transition %s: ExtraSet %w", req.JobID, err)
		}
	}
	setClauses = append(setClauses, req.ExtraSets...)

	// Build the SET clause string.
	setClause := ""
	for i, c := range setClauses {
		if i > 0 {
			setClause += ", "
		}
		setClause += c
	}

	// Build WHERE clause with optimistic-lock guard.
	whereClause := "WHERE id = ? AND status = ? AND revision = ?"
	whereArgs := []any{req.JobID, req.ExpectedStatus, req.ExpectedRevision}

	// Lease-fencing: when WorkerID and LeaseID are both set.
	if req.WorkerID != "" && req.LeaseID != "" {
		whereClause += " AND worker_id = ? AND lease_id = ?"
		whereArgs = append(whereArgs, req.WorkerID, req.LeaseID)
	}

	query := fmt.Sprintf("UPDATE jobs SET %s %s", setClause, whereClause)
	allArgs := append(args, whereArgs...)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("transition: begin: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, query, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("transition: exec: %w", err)
	}

	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("transition %s: %w", req.JobID, ErrOptimisticLockFailed)
	}
	if resultPayload != "" {
		if err := persistJobResult(ctx, tx, req.JobID, 0, resultPayload); err != nil {
			return nil, fmt.Errorf("transition: persist result: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("transition: commit: %w", err)
	}

	// Re-fetch the updated row.
	updated, err := r.Get(ctx, req.JobID)
	if err != nil {
		return nil, fmt.Errorf("transition: re-fetch after update: %w", err)
	}
	if updated == nil {
		return nil, fmt.Errorf("transition: job %s disappeared after successful UPDATE", req.JobID)
	}

	return updated, nil
}
