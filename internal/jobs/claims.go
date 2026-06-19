package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── ClaimNext ───────────────────────────────────────────────────────────

// ClaimNext atomically claims the oldest queued job, transitioning it to
// running (via Start). Returns (nil, nil) on empty queue.
// Implements Repository.ClaimNext.
func (r *SQLiteStore) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration, types []string) (*Job, error) {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()

	now := time.Now()
	leaseExpiry := now.Add(leaseTTL)

	// Find the best candidate (queued only — ClaimNext in the domain interface
	// is the atomic claim + start, so we select from queued).
	query := `SELECT ` + jobColumns + ` FROM jobs
		WHERE status = 'queued' ORDER BY priority DESC, created_at ASC LIMIT 1`
	args := []any{}
	if len(types) > 0 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			placeholders[i] = "?"
			args = append(args, t)
		}
		query = `SELECT ` + jobColumns + ` FROM jobs
			WHERE status = 'queued' AND type IN (` + strings.Join(placeholders, ",") + `)
			ORDER BY priority DESC, created_at ASC LIMIT 1`
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	j := &Job{}
	if err := scanJobColumns(row, j); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("ClaimNext: scan: %w", err)
	}

	// Generate lease ID.
	leaseID := fmt.Sprintf("lease_%d_%s", now.UnixNano(), hashutil.RandomString(8))
	startCmd := StartJob{
		JobID:    j.ID,
		WorkerID: workerID,
		LeaseID:  leaseID,
		LeaseTTL: leaseTTL,
		Revision: int64(j.Revision),
	}
	startedJob, err := r.Start(ctx, startCmd)
	if err != nil {
		return nil, fmt.Errorf("ClaimNext: start job %s: %w", j.ID, err)
	}
	startedJob.LeaseID = leaseID
	startedJob.LeaseExpiry = &leaseExpiry
	return startedJob, nil
}

// ── Start (internal, used by ClaimNext) ──────────────────────────────────

// Start transitions a queued or leased job to running.
func (r *SQLiteStore) Start(ctx context.Context, cmd StartJob) (*Job, error) {
	now := time.Now().UTC()
	leaseExpiry := now.Add(cmd.LeaseTTL)
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'running', started_at = ?,
		 lease_expiry = ?, lease_id = ?, worker_id = ?,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('queued', 'leased')
		 AND revision = ?`,
		timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(leaseExpiry),
		cmd.LeaseID, cmd.WorkerID,
		timeutil.FormatRFC3339(now),
		cmd.JobID, cmd.Revision,
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

// RenewLease extends an existing lease for a running job.
func (r *SQLiteStore) RenewLease(ctx context.Context, id string, workerID string, leaseTTL time.Duration) error {
	newExpiry := time.Now().Add(leaseTTL)
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET lease_expiry = ?, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = 'running'
		 AND worker_id = ?`,
		timeutil.FormatRFC3339(newExpiry), timeutil.FormatRFC3339(time.Now()),
		id, workerID,
	)
	if err != nil {
		return fmt.Errorf("RenewLease: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("%w: renew lease", ErrLeaseLost)
	}
	return nil
}

// ── RequeueExpiredLeases ────────────────────────────────────────────────

// RequeueExpiredLeases reclaims leased/running jobs with expired leases.
func (r *SQLiteStore) RequeueExpiredLeases(ctx context.Context, now time.Time, limit int) ([]RequeueResult, error) {
	nowStr := timeutil.FormatRFC3339(now)
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, retry_count, max_retries, revision
		 FROM jobs WHERE status IN ('leased', 'running') AND lease_expiry < ?
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

func (r *SQLiteStore) requeueSingle(ctx context.Context, jobID string, retryCount, maxRetries, revision int, now time.Time) RequeueResult {
	nowStr := timeutil.FormatRFC3339(now)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return RequeueResult{JobID: jobID, Error: fmt.Sprintf("begin tx: %v", err)}
	}
	defer tx.Rollback()

	// Determine current status.
	var currentStatus Status
	err = tx.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = ?`, jobID).Scan(&currentStatus)
	if err != nil {
		return RequeueResult{JobID: jobID, Error: fmt.Sprintf("select status: %v", err)}
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	if retryCount < maxRetries {
		// leased → queued (claimed but never started), running → retry_wait (was executing)
		targetStatus := StatusRetryWait
		eventType := "job_retry_wait"
		eventMsg := "Lease expired, retrying"
		if currentStatus == StatusLeased {
			targetStatus = StatusQueued
			eventType = "job_queued"
			eventMsg = "Lease expired, re-queued"
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE jobs SET status = ?, worker_id = '',
			 lease_id = '', lease_expiry = NULL, revision = revision + 1, updated_at = ?
			 WHERE id = ? AND status = ? AND lease_expiry < ? AND revision = ?`,
			targetStatus, nowStr, jobID, currentStatus, nowStr, revision)
		if err != nil || mustRowsAffected(res) == 0 {
			return RequeueResult{JobID: jobID, Error: "rows affected 0"}
		}
		tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			evtID, jobID, eventType, eventMsg, "{}", nowStr)
		tx.Commit()
		return RequeueResult{JobID: jobID, NewStatus: targetStatus}
	}
	// Exhausted → failed
	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'failed', completed_at = ?, error = ?,
		 worker_id = '', lease_id = '', lease_expiry = NULL, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = ? AND lease_expiry < ? AND revision = ?`,
		nowStr, "max retries exhausted (reaper)", nowStr, jobID, currentStatus, nowStr, revision)
	if err != nil || mustRowsAffected(res) == 0 {
		return RequeueResult{JobID: jobID, Error: "rows affected 0"}
	}
	tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, jobID, "job_failed", "Max retries exhausted", "{}", nowStr)
	tx.Commit()
	return RequeueResult{JobID: jobID, NewStatus: StatusFailed}
}

func mustRowsAffected(res sql.Result) int {
	n, _ := res.RowsAffected()
	return int(n)
}
