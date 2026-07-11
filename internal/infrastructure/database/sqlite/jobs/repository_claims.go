package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── ClaimNext ───────────────────────────────────────────────────────────

// ClaimNext atomically claims the oldest queued job, transitioning it to
// running (via Start). Returns (nil, nil) on empty queue.
// Implements Store.ClaimNext.
//
// Concurrency (post-PR-Polling design, ADR-0003 §Implementation-status #6
// supersession by PR-Queue-Split-claimMu cleanup, June 2026): the previous
// `claimMu` application-level mutex is REMOVED. Two workers racing the
// same row serialise on SQLite's WAL + the `AND revision = ?` CAS gate in
// Start() — the loser sees rows-affected=0 → ErrTransitionConflict,
// surfaced as an error to the caller (treated as "not claimed, retry
// next iteration"). Empty queue remains (nil, nil) for ErrNoRows on the
// initial SELECT.
func (r *SQLiteStore) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration, types []string) (*job.Job, error) {
	now := time.Now()
	leaseExpiry := now.Add(leaseTTL)

	// Find the best candidate (queued only — ClaimNext in the domain interface
	// is the atomic claim + start, so we select from queued).
	query := `SELECT ` + jobColumns + ` FROM jobs
		WHERE status = 'QUEUED' ORDER BY priority DESC, created_at ASC LIMIT 1`
	var args []any
	if len(types) > 0 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			placeholders[i] = "?"
			args = append(args, t)
		}
		query = `SELECT ` + jobColumns + ` FROM jobs
			WHERE status = 'QUEUED' AND type IN (` + strings.Join(placeholders, ",") + `)
			ORDER BY priority DESC, created_at ASC LIMIT 1`
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	j := &job.Job{}
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
func (r *SQLiteStore) Start(ctx context.Context, cmd StartJob) (*job.Job, error) {
	now := time.Now().UTC()
	leaseExpiry := now.Add(cmd.LeaseTTL)
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'RUNNING', started_at = ?,
		 lease_expiry = ?, lease_id = ?, worker_id = ?,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('QUEUED', 'LEASED')
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
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO job_events (id, job_id, type, message, data_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, cmd.JobID, "job_running", "job.Job started", "{}", timeutil.FormatRFC3339(now),
	); err != nil {
		return nil, fmt.Errorf("start: insert job event: %w", err)
	}
	return r.Get(ctx, cmd.JobID)
}

// ── RenewLease ───────────────────────────────────────────────────────────

// RenewLease extends an existing lease for a running or finalizing job.
//
// JOBS-T01-SQLITE-REPO (P0, 2026-07-15) signature-drift fix: the
// previous UPDATE silently bumped `revision = revision + 1`, which
// invalidated the worker's expectedRevision for subsequent Complete /
// Fail CAS checks (the kernel/job.Store::RenewLease signature has NO
// return channel for the new revision). Result: stale expectedRevision
// → CAS mismatch → tx rollback → RUNNING-state job orphaned in the
// broker. The fix removes the silent revision bump; the canonical
// invariant is that revision is bumped ONLY on fenced state
// transitions (Complete / Fail / ScheduleRetry / Cancel / Retry /
// FinalizeAggregateParent), NOT on lease extensions.
func (r *SQLiteStore) RenewLease(ctx context.Context, id string, workerID string, leaseTTL time.Duration) error {
	newExpiry := time.Now().Add(leaseTTL)
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET lease_expiry = ?, updated_at = ?
		 WHERE id = ? AND status IN ('RUNNING', 'FINALIZING')
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
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5.b (July 2026, surfaced by
// lifecycle_p1b_test.go): the previous implementation iterated over
// the SELECT rows handle WHILE calling requeueSingle → BeginTx, which
// held a SQLite SHARED read lock on the database for the entire loop.
// The subsequent BeginTx (for the per-row UPDATE) needed a RESERVED
// write lock; in non-WAL deployments (and in the P1.B test fixture,
// which uses a tempfile without WAL) this deadlocked with rows-affected=0.
//
// godlike/07 fail-closed: the fix drains the SELECT into a local slice
// and CLOSES the rows handle BEFORE invoking requeueSingle. This
// releases the SHARED lock so each per-row BeginTx can acquire its
// RESERVED lock without contention. The fix is correctness-preserving
// in WAL mode (the per-row requeue semantics are unchanged) and is
// the only configuration that works in non-WAL deployments.
func (r *SQLiteStore) RequeueExpiredLeases(ctx context.Context, now time.Time, limit int) ([]RequeueResult, error) {
	nowStr := timeutil.FormatRFC3339(now)
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, retry_count, max_retries, revision
		 FROM jobs WHERE status IN ('LEASED', 'RUNNING', 'FINALIZING') AND lease_expiry < ?
		 ORDER BY lease_expiry LIMIT ?`, nowStr, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("requeueExpired: select: %w", err)
	}

	// Buffer the targets to memory and close the rows handle BEFORE
	// invoking requeueSingle. This releases the SQLite SHARED read
	// lock so the per-row BeginTx in requeueSingle can acquire its
	// RESERVED write lock without contention.
	type reqItem struct {
		jobID      string
		retryCount int
		maxRetries int
		revision   int
	}
	var items []reqItem
	for rows.Next() {
		var item reqItem
		if err := rows.Scan(&item.jobID, &item.retryCount, &item.maxRetries, &item.revision); err != nil {
			rows.Close()
			return nil, fmt.Errorf("requeueExpired: scan: %w", err)
		}
		items = append(items, item)
	}
	rows.Close()

	var results []RequeueResult
	for _, item := range items {
		results = append(results, r.requeueSingle(ctx, item.jobID, item.retryCount, item.maxRetries, item.revision, now))
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
	var currentStatus job.Status
	err = tx.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = ?`, jobID).Scan(&currentStatus)
	if err != nil {
		return RequeueResult{JobID: jobID, Error: fmt.Sprintf("select status: %v", err)}
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	if retryCount < maxRetries {
		// leased → queued (claimed but never started), running → retry_wait (was executing)
		targetStatus := job.StatusRetryWait
		eventType := "job_retry_wait"
		eventMsg := "Lease expired, retrying"
		if currentStatus == job.StatusLeased {
			targetStatus = job.StatusQueued
			eventType = "job_queued"
			eventMsg = "Lease expired, re-queued"
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE jobs SET status = ?, worker_id = '',
			 lease_id = '', lease_expiry = NULL, revision = revision + 1, updated_at = ?
			 WHERE id = ? AND status = ? AND lease_expiry < ? AND revision = ?`,
			targetStatus, nowStr, jobID, currentStatus, nowStr, revision)
		// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5.b (July 2026, surfaced by
		// lifecycle_p1b_test.go): split the error check so a SQL exec
		// error (e.g. "database is locked") is surfaced verbatim in
		// RequeueResult.Error instead of being masked as the generic
		// "rows affected 0". Operators need the actual error to
		// diagnose reclaim failures; the previous one-line check
		// collapsed both cases and made lock-contention debugging
		// impossible.
		if err != nil {
			return RequeueResult{JobID: jobID, Error: fmt.Sprintf("requeue update: %v", err)}
		}
		if mustRowsAffected(res) == 0 {
			return RequeueResult{JobID: jobID, Error: "rows affected 0 (CAS fence: status/lease_expiry/revision mismatch)"}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			evtID, jobID, eventType, eventMsg, "{}", nowStr); err != nil {
			return RequeueResult{JobID: jobID, Error: fmt.Sprintf("insert job event: %v", err)}
		}
		if err := tx.Commit(); err != nil {
			return RequeueResult{JobID: jobID, Error: fmt.Sprintf("commit: %v", err)}
		}
		return RequeueResult{JobID: jobID, NewStatus: targetStatus}
	}
	// Exhausted → failed
	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'FAILED', completed_at = ?, error = ?,
		 worker_id = '', lease_id = '', lease_expiry = NULL, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = ? AND lease_expiry < ? AND revision = ?`,
		nowStr, "max retries exhausted (reaper)", nowStr, jobID, currentStatus, nowStr, revision)
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5.b (July 2026): same split as
	// the under-max-retries block above — surface the SQL error verbatim
	// instead of masking it as the generic "rows affected 0".
	if err != nil {
		return RequeueResult{JobID: jobID, Error: fmt.Sprintf("requeue exhausted update: %v", err)}
	}
	if mustRowsAffected(res) == 0 {
		return RequeueResult{JobID: jobID, Error: "rows affected 0 (CAS fence: status/lease_expiry/revision mismatch)"}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, jobID, "job_failed", "Max retries exhausted", "{}", nowStr); err != nil {
		return RequeueResult{JobID: jobID, Error: fmt.Sprintf("insert job event: %v", err)}
	}
	if err := tx.Commit(); err != nil {
		return RequeueResult{JobID: jobID, Error: fmt.Sprintf("commit: %v", err)}
	}
	return RequeueResult{JobID: jobID, NewStatus: job.StatusFailed}
}

func mustRowsAffected(res sql.Result) int {
	n, _ := res.RowsAffected()
	return int(n)
}
