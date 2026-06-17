package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/media/models"
	"velox/go-master/pkg/timeutil"
)

// ErrOptimisticLockFailed is returned by Transition when the optimistic
// lock token (revision + expected status) does not match the row in
// the database. Callers should reload the job via Get to read the new
// authoritative state and decide whether to retry, abort, or surface
// the conflict up to the user.
var ErrOptimisticLockFailed = errors.New("jobs: optimistic lock failed")

// transitionUpdateKeys is the defence-in-depth whitelist of column
// names that a TransitionRequest.Updates map may target. Anything
// outside this set is rejected by Transition before reaching SQL.
// Adding a new transition-side column requires extending this list.
var transitionUpdateKeys = map[string]bool{
	"result_json":  true,
	"progress":     true,
	"error":        true,
	"completed_at": true,
	"cancelled_at": true,
	"started_at":   true,
	"lease_expiry": true,
	"worker_id":    true,
	"retry_count":  true,
	"active_key":   true,
}

// TransitionRequest is the canonical request shape for advancing a
// job's state under optimistic-lock. It mirrors the persistence-side
// representation; the domain-facing alias lives in
// internal/core/domain/job as job.TransitionRequest.
//
// Fields JobID, ExpectedRevision, ExpectedStatus and NewStatus are
// mandatory. Updates is optional: when non-nil it is applied
// atomically with the status flip.
type TransitionRequest struct {
	JobID            string
	ExpectedRevision int
	ExpectedStatus   models.JobStatus
	NewStatus        models.JobStatus

	// Updates maps column name → value applied atomically alongside
	// the status change. Keys must be in transitionUpdateKeys.
	// Supported value types: string, int, int64, time.Time,
	// *time.Time (nil → SQL NULL), []byte.
	Updates map[string]any
}

// Transition advances a job from req.ExpectedStatus to req.NewStatus
// under an optimistic-lock token (`revision = req.ExpectedRevision`).
// The Updates map is applied atomically with the status change so a
// single round-trip covers both the lifecycle move and the payload
// mutation (e.g. result_json + completed_at + active_key clear).
//
// Returns ErrOptimisticLockFailed when RowsAffected == 0: another
// writer raced past the caller's read. The caller decides whether
// to reload + retry or surface the conflict.
//
// On success, returns the freshly-read job row (with the bumped
// revision) so callers can chain further transitions without a
// second Get.
//
// See internal/core/domain/job.TransitionRequest for the canonical
// contract. The persistence side lives here; the domain interface in
// internal/core/domain/job is the API surface consumers depend on.
func (r *Repository) Transition(ctx context.Context, req TransitionRequest) (*models.Job, error) {
	if req.JobID == "" {
		return nil, fmt.Errorf("transition: job_id is required")
	}
	if req.ExpectedRevision <= 0 {
		return nil, fmt.Errorf("transition: expected_revision must be positive, got %d", req.ExpectedRevision)
	}
	if req.NewStatus == "" {
		return nil, fmt.Errorf("transition: new_status is required")
	}
	if err := models.TransitionJob(req.ExpectedStatus, req.NewStatus); err != nil {
		return nil, fmt.Errorf("transition: state machine rejected: %w", err)
	}

	setClauses := []string{"status = ?", "revision = revision + 1", "updated_at = ?"}
	args := []any{req.NewStatus, timeutil.FormatRFC3339(time.Now())}

	for key, val := range req.Updates {
		if !transitionUpdateKeys[key] {
			return nil, fmt.Errorf("transition: disallowed update key %q", key)
		}
		setClauses = append(setClauses, key+" = ?")
		switch v := val.(type) {
		case string:
			args = append(args, v)
		case int:
			args = append(args, v)
		case int64:
			args = append(args, v)
		case time.Time:
			args = append(args, timeutil.FormatRFC3339(v))
		case *time.Time:
			args = append(args, timeutil.FormatPtrRFC3339(v))
		case []byte:
			args = append(args, string(v))
		default:
			return nil, fmt.Errorf("transition: unsupported value type for %q: %T", key, v)
		}
	}

	args = append(args, req.JobID, req.ExpectedRevision, req.ExpectedStatus)
	query := "UPDATE jobs SET " + strings.Join(setClauses, ", ") +
		" WHERE id = ? AND revision = ? AND status = ?"

	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("transition: update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("%w: job_id=%s expected revision=%d status=%s",
			ErrOptimisticLockFailed, req.JobID, req.ExpectedRevision, req.ExpectedStatus)
	}

	return r.Get(ctx, req.JobID)
}
