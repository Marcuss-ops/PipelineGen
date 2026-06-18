package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ErrOptimisticLockFailed is returned by Transition when the optimistic-lock
// guard (WHERE revision = ?) fails — another worker or operation modified
// the job row between the read and this write.
var ErrOptimisticLockFailed = fmt.Errorf("optimistic lock failed: revision or status changed")

// TransitionRequest carries all the parameters for an atomic job status
// transition. Every field except JobID, ExpectedStatus, and NewStatus is
// optional.
type TransitionRequest struct {
	// JobID identifies the job row.
	JobID string

	// ExpectedRevision is the revision the caller last observed.
	// Transition verifies it hasn't changed; if it has, the job was
	// modified concurrently and this transition is rejected.
	ExpectedRevision int

	// ExpectedStatus is the status the caller last observed.
	// Transition verifies the current row status matches this value;
	// if it doesn't, the job is in an unexpected state and the
	// transition is rejected.
	ExpectedStatus models.JobStatus

	// NewStatus is the target status after the transition.
	NewStatus models.JobStatus

	// WorkerID and LeaseID carry the fencing tokens from the caller.
	// When set, the Transition additionally verifies that the current
	// worker_id and lease_id match — preventing stale-lease completion
	// of a reassigned job. Omit both for non-worker operations (e.g.
	// operator retry, cancel).
	WorkerID string
	LeaseID  string

	// Updates is a map of column → value pairs appended to the SET
	// clause. Supports normal values (string, int, map[string]any for
	// JSON) and *time.Time (formatted via timeutil.FormatPtrRFC3339).
	Updates map[string]any

	// ExtraSets are raw SQL snippets appended to the SET clause
	// without parameterisation. Used for expressions like
	// "retry_count = retry_count + 1" that can't be expressed as
	// key → value pairs. Callers MUST ensure these are safe;
	// ExtraSets is not interpolated from user input.
	ExtraSets []string

	// Result, when non-nil, is serialised to result_json.
	Result map[string]any

	// Error, when non-nil, is stored in the error column.
	Error *string

	// Progress, when non-nil, is stored in the progress column.
	Progress *int
}

// Transition atomically executes a guarded status change on the jobs table.
//
// The UPDATE uses WHERE id = ? AND status = ? AND revision = ? as the
// optimistic-lock guard. If the current status or revision has changed
// since the caller last observed the job, the UPDATE affects 0 rows and
// ErrOptimisticLockFailed is returned.
//
// When WorkerID and LeaseID are both set, the guard additionally includes
// AND worker_id = ? AND lease_id = ? — this allows the adapter layer
// (SQLiteJobRepository) to enforce lease-fencing for worker operations
// without re-implementing the SQL pattern.
//
// On success, the revision is incremented and the updated job row is
// re-fetched and returned. The caller must check the returned job's
// revision for the next Transition call.
func (r *Repository) Transition(ctx context.Context, req TransitionRequest) (*models.Job, error) {
	now := time.Now()

	// Build the UPDATE query with parameterised SET clauses.
	setClauses := []string{"status = ?", "updated_at = ?", "revision = revision + 1"}
	args := []any{req.NewStatus, timeutil.FormatRFC3339(now)}

	// Handle Updates map.
	for col, val := range req.Updates {
		switch v := val.(type) {
		case *time.Time:
			setClauses = append(setClauses, col+" = ?")
			args = append(args, timeutil.FormatPtrRFC3339(v))
		default:
			setClauses = append(setClauses, col+" = ?")
			args = append(args, v)
		}
	}

	// Handle special fields.
	if req.Result != nil {
		resultBytes, _ := json.Marshal(req.Result)
		setClauses = append(setClauses, "result_json = ?")
		args = append(args, string(resultBytes))
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

	// Lease-fencing: when WorkerID and LeaseID are both set, add them
	// to the guard so only the owning worker can finalise the job.
	if req.WorkerID != "" && req.LeaseID != "" {
		whereClause += " AND worker_id = ? AND lease_id = ?"
		whereArgs = append(whereArgs, req.WorkerID, req.LeaseID)
	}

	query := fmt.Sprintf("UPDATE jobs SET %s %s", setClause, whereClause)
	allArgs := append(args, whereArgs...)

	result, err := r.db.ExecContext(ctx, query, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("transition: exec: %w", err)
	}

	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("transition %s: %w", req.JobID, ErrOptimisticLockFailed)
	}

	// Re-fetch the updated row so callers have fresh revision + status.
	updated, err := r.Get(ctx, req.JobID)
	if err != nil {
		return nil, fmt.Errorf("transition: re-fetch after update: %w", err)
	}
	if updated == nil {
		return nil, fmt.Errorf("transition: job %s disappeared after successful UPDATE", req.JobID)
	}

	return updated, nil
}
