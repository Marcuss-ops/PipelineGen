package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/media/models"
	"velox/go-master/pkg/hashutil"
	"velox/go-master/pkg/timeutil"
)

// ── ClaimNext ───────────────────────────────────────────────────────────

// ClaimNext atomically claims the next PENDING job, transitioning it to
// LEASED under a fencing token (LeaseID). Returns (nil, nil) on empty queue.
// Returns ErrAlreadyClaimed on optimistic-lock collision with another worker.
func (r *Repository) ClaimNext(ctx context.Context, cmd ClaimNext) (*Lease, error) {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()

	now := time.Now()
	leaseExpiry := now.Add(cmd.LeaseTTL)
	leaseExpiryStr := timeutil.FormatRFC3339(leaseExpiry)
	nowStr := timeutil.FormatRFC3339(now)

	// Find the best candidate
	query := `SELECT ` + jobColumns + ` FROM jobs
		WHERE status = 'PENDING' ORDER BY priority DESC, created_at ASC LIMIT 1`
	args := []any{}
	if len(cmd.Types) > 0 {
		placeholders := make([]string, len(cmd.Types))
		for i, t := range cmd.Types {
			placeholders[i] = "?"
			args = append(args, t)
		}
		query = `SELECT ` + jobColumns + ` FROM jobs
			WHERE status = 'PENDING' AND type IN (` + strings.Join(placeholders, ",") + `)
			ORDER BY priority DESC, created_at ASC LIMIT 1`
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	job := &models.Job{}
	if err := scanJobColumns(row, job); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("claimNext: scan: %w", err)
	}

	// Atomic CAS: PENDING → LEASED with fencing token
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'LEASED', worker_id = ?, lease_id = ?,
		 lease_expiry = ?, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = 'PENDING' AND revision = ?`,
		cmd.WorkerID, cmd.LeaseID, leaseExpiryStr, nowStr, job.ID, job.Revision,
	)
	if err != nil {
		return nil, fmt.Errorf("claimNext: update: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, ErrAlreadyClaimed
	}

	job.Status = models.StatusLeased
	job.WorkerID = cmd.WorkerID
	job.LeaseID = cmd.LeaseID
	job.LeaseExpiry = &leaseExpiry
	job.Revision++

	return &Lease{Job: job, LeaseID: cmd.LeaseID, LeaseExpiry: leaseExpiry}, nil
}

// ── Start ────────────────────────────────────────────────────────────────

// Start transitions a LEASED or PENDING job to RUNNING.
func (r *Repository) Start(ctx context.Context, cmd StartJob) (*models.Job, error) {
	now := time.Now().UTC()
	leaseExpiry := now.Add(cmd.LeaseTTL)
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'RUNNING', started_at = ?,
		 lease_expiry = ?, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('PENDING', 'LEASED')
		 AND worker_id = ? AND lease_id = ? AND revision = ?`,
		timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(leaseExpiry),
		timeutil.FormatRFC3339(now),
		cmd.JobID, cmd.WorkerID, cmd.LeaseID, cmd.Revision,
	)
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, ErrTransitionConflict
	}
	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	_, _ = r.db.ExecContext(ctx,
		`INSERT INTO job_events (id, job_id, type, message, data_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, cmd.JobID, "job_running", "Job started", "{}", timeutil.FormatRFC3339(now),
	)
	return r.Get(ctx, cmd.JobID)
}

// ── RenewLease ───────────────────────────────────────────────────────────

// RenewLease extends an existing lease for a RUNNING job.
// Returns ErrLeaseLost if the worker/lease/revision don't match.
func (r *Repository) RenewLease(ctx context.Context, cmd RenewLease) (*models.Job, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET lease_expiry = ?, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = 'RUNNING'
		 AND worker_id = ? AND lease_id = ? AND revision = ?`,
		timeutil.FormatRFC3339(cmd.NewExpiration), timeutil.FormatRFC3339(time.Now()),
		cmd.JobID, cmd.WorkerID, cmd.LeaseID, cmd.Revision,
	)
	if err != nil {
		return nil, fmt.Errorf("renewLease: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, ErrLeaseLost
	}
	return r.Get(ctx, cmd.JobID)
}

// ── RequeueExpiredLeases ────────────────────────────────────────────────

// RequeueExpiredLeases reclaims LEASED/RUNNING jobs with expired leases.
// Each row processed individually: remaining retries → RETRY_WAIT,
// exhausted → FAILED. All in per-row transactions.
func (r *Repository) RequeueExpiredLeases(ctx context.Context, now time.Time, limit int) ([]RequeueResult, error) {
	nowStr := timeutil.FormatRFC3339(now)
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, retry_count, max_retries, revision
		 FROM jobs WHERE status IN ('LEASED', 'RUNNING') AND lease_expiry < ?
		 ORDER BY lease_expiry LIMIT ?`, nowStr, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("requeueExpired: select: %w", err)
	}
	defer rows.Close()

	var results []RequeueResult
	for rows.Next() {
		var jobID string
		var retryCount, maxRetries, revision int
		if err := rows.Scan(&jobID, &retryCount, &maxRetries, &revision); err != nil {
			return nil, fmt.Errorf("requeueExpired: scan: %w", err)
		}
		results = append(results, r.requeueSingle(ctx, jobID, retryCount, maxRetries, revision, now))
	}
	return results, nil
}

func (r *Repository) requeueSingle(ctx context.Context, jobID string, retryCount, maxRetries, revision int, now time.Time) RequeueResult {
	nowStr := timeutil.FormatRFC3339(now)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return RequeueResult{JobID: jobID, Error: fmt.Sprintf("begin tx: %v", err)}
	}
	defer tx.Rollback()

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	if retryCount < maxRetries {
		res, err := tx.ExecContext(ctx,
			`UPDATE jobs SET status = 'RETRY_WAIT', worker_id = '',
			 lease_id = '', lease_expiry = NULL, revision = revision + 1, updated_at = ?
			 WHERE id = ? AND status IN ('LEASED', 'RUNNING') AND lease_expiry < ? AND revision = ?`,
			nowStr, jobID, nowStr, revision)
		if err != nil || mustRowsAffected(res) == 0 {
			return RequeueResult{JobID: jobID, Error: "rows affected 0"}
		}
		tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			evtID, jobID, "job_retry_wait", "Lease expired, retrying", "{}", nowStr)
		tx.Commit()
		return RequeueResult{JobID: jobID, NewStatus: models.StatusRetryWait}
	}
	// Exhausted → FAILED
	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'FAILED', completed_at = ?, error = ?,
		 worker_id = '', lease_id = '', lease_expiry = NULL, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('LEASED', 'RUNNING') AND lease_expiry < ? AND revision = ?`,
		nowStr, "max retries exhausted (reaper)", nowStr, jobID, nowStr, revision)
	if err != nil || mustRowsAffected(res) == 0 {
		return RequeueResult{JobID: jobID, Error: "rows affected 0"}
	}
	tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, jobID, "job_failed", "Max retries exhausted", "{}", nowStr)
	tx.Commit()
	return RequeueResult{JobID: jobID, NewStatus: models.StatusFailed}
}

func mustRowsAffected(res sql.Result) int {
	n, _ := res.RowsAffected()
	return int(n)
}
