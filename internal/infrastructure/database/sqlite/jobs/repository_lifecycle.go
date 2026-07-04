package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainremote "github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// SetProgress updates progress percentage and emits an event.
//
// FASE 0.2 (July 4 2026) silent-drop rewrite per
// PR-GODOBJ-14-WORKER-REGISTRY godlike/07 no-fake-availability: pre-PR
// the function used `_ = r.AddEvent(...)` — any AddEvent failure
// (DB hiccup, dup row, partial commit) was silently dropped with
// zero observability. Post-PR the AddEvent call is error-checked
// and any drop bumps observability.WorkerEventDropsTotal so the
// operator dashboard can alert on this failure mode.
//
// Cardinality bound: job_type label is "" at this site because the
// SetProgress wrapper is infra-layer and does not see the
// jobs.type column (would require a join). Forward-pointer
// PR-Telemetry-AddEvent-Infra-Type threads job_type through; until
// then, the "" label partitions "infra-level drops" from per-job_type
// drops counted by the worker-package sites (worker_metrics.go).
func (r *SQLiteStore) SetProgress(ctx context.Context, jobID string, progress int, message string) error {
	query := `UPDATE jobs SET progress = ?, updated_at = ? WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, progress, timeutil.FormatRFC3339(time.Now()), jobID)
	if err != nil {
		return fmt.Errorf("setProgress: %w", err)
	}
	if message != "" {
		if addEvtErr := r.AddEvent(ctx, jobID, "progress", message, map[string]any{"progress": progress}); addEvtErr != nil {
			// godlike/07 typed-error contract: the wrapper has no
			// logger access (infra layer); the counter is the
			// canonical observability surface here. The SetProgress
			// return value is unchanged (nil) so the broker-side
			// flow continues — this is a non-fatal telemetry emit.
			observability.WorkerEventDropsTotal.WithLabelValues("").Inc()
		}
	}
	return nil
}

// (FASE 0.1 / July 4, 2026 SSOT cutover)
// The pre-FASE-0.1 package-local sentinel
// `ErrArtifactJobRequiresCompleteWithArtifacts` was REMOVED. Per
// godlike/06 one-canonical-owner-per-fact + godlike/07 no-fake-
// availability, the canonical typed sentinel for the failure mode
// "legacy Complete path attempted on artifact-producing job" is
// domainremote.ErrCompleteJobPathViolation. The SQL-layer gate below
// wraps it via fmt.Errorf("%w: ...", domainremote.ErrCompleteJobPathViolation,
// ...). Callers MUST errors.Is against the canonical sentinel name.
// The deprecation record REMOTE-COMPLETE-LEGACY in
// architecture/deprecations.yaml tracks the EXPAND-phase deprecation
// window with removal_date 2026-Q4 (per the REMOTE-COMPLETE-LEGACY
// migration_phase).

// ── Complete (atomic transaction) ────────────────────────────────────────

// Complete marks a job as completed with a result. Fenced by lease.
//
// Deprecated: artifact-producing jobs (ProducesArtifacts=true in the
// registry) MUST use the JobFinalizer.CompleteWithArtifacts path instead.
// This method rejects artifact-producing job types with the canonical
// sentinel domainremote.ErrCompleteJobPathViolation (godlike/06 SSOT,
// REMOTE-COMPLETE-LEGACY EXPAND-phase window, removal_date 2026-Q4).
// Use broker.CompleteWithArtifacts which routes through the transactional
// finalization spine.
func (r *SQLiteStore) Complete(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, result json.RawMessage) error {
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)
	resultJSON := string(result)
	if resultJSON == "" || resultJSON == "null" {
		resultJSON = "{}"
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("complete: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Validate ownership — also read the job type to gate artifact producers.
	var status job.Status
	var curWorkerID, curLeaseID, jobType string
	var revision int
	err = tx.QueryRowContext(ctx, `SELECT status, worker_id, lease_id, type, revision FROM jobs WHERE id = ?`, id).
		Scan(&status, &curWorkerID, &curLeaseID, &jobType, &revision)
	if err != nil {
		if err == sql.ErrNoRows {
			return ErrJobNotFound
		}
		return fmt.Errorf("complete: select: %w", err)
	}
	if err := validateOwnership(id, "complete", status, curWorkerID, curLeaseID, revision,
		workerID, leaseID, int64(expectedRevision), job.StatusRunning, job.StatusFinalizing); err != nil {
		return err
	}

	// Gate: artifact-producing jobs MUST use CompleteWithArtifacts.
	// FASE 0.1 (July 4, 2026) SSOT: the canonical typed sentinel for this
	// failure mode is domainremote.ErrCompleteJobPathViolation (godlike/06
	// one-owner-per-fact). The SQL-layer surface wraps it via
	// fmt.Errorf("%w: ...", domainremote.ErrCompleteJobPathViolation, ...)
	// so callers errors.Is the canonical sentinel.
	if r.producesArtifacts != nil && r.producesArtifacts[jobType] {
		return fmt.Errorf("%w: job type %q produces artifacts — use CompleteWithArtifacts instead of Complete", domainremote.ErrCompleteJobPathViolation, jobType)
	}

	// Atomic update
	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'SUCCEEDED', completed_at = ?, result_json = ?,
		 progress = 100, worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('RUNNING', 'FINALIZING') AND worker_id = ? AND lease_id = ? AND revision = ?`,
		nowStr, resultJSON, nowStr,
		id, workerID, leaseID, expectedRevision)
	if err != nil {
		return fmt.Errorf("complete: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		// Race-after-validateOwnership (i.e. validateOwnership passed but a
		// concurrent transaction committed before our UPDATE). The earlier
		// validateOwnership-mismatch case is already counted via the bump
		// inside validateOwnership itself when method="complete". This second
		// bump covers the race window — never a double-count in practice
		// because validateOwnership only bumps on FAILURE (early return),
		// so by the time we reach here, validateOwnership has already passed.
		observability.JobTransitionConflictTotal.WithLabelValues("complete").Inc()
		return ErrTransitionConflict
	}

	// Insert event
	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_completed", "job.Job completed successfully", "{}", nowStr); err != nil {
		return fmt.Errorf("complete: insert job event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("complete: commit: %w", err)
	}
	return nil
}

// ── Fail (atomic transaction) ────────────────────────────────────────────

// Fail marks a job as failed. Fenced by lease.
func (r *SQLiteStore) Fail(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, errMsg string) error {
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("fail: begin tx: %w", err)
	}
	defer tx.Rollback()

	var status job.Status
	var curWorkerID, curLeaseID string
	var revision int
	err = tx.QueryRowContext(ctx, `SELECT status, worker_id, lease_id, revision FROM jobs WHERE id = ?`, id).
		Scan(&status, &curWorkerID, &curLeaseID, &revision)
	if err != nil {
		if err == sql.ErrNoRows {
			return ErrJobNotFound
		}
		return fmt.Errorf("fail: select: %w", err)
	}
	if err := validateOwnership(id, "fail", status, curWorkerID, curLeaseID, revision,
		workerID, leaseID, int64(expectedRevision), job.StatusRunning, job.StatusFinalizing); err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'FAILED', completed_at = ?, error = ?,
		 worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('RUNNING', 'FINALIZING') AND worker_id = ? AND lease_id = ? AND revision = ?`,
		nowStr, errMsg, nowStr,
		id, workerID, leaseID, expectedRevision)
	if err != nil {
		return fmt.Errorf("fail: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		// Same race-window rationale as Complete's CAS-fence bump above:
		// validateOwnership would have early-returned on its own mismatch,
		// so we never double-count. See the comment block at Complete's
		// CAS fence for the full invariant.
		observability.JobTransitionConflictTotal.WithLabelValues("fail").Inc()
		return ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_failed", errMsg, "{}", nowStr); err != nil {
		return fmt.Errorf("fail: insert job event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("fail: commit: %w", err)
	}
	return nil
}

// Aggregate-flipper port (godlike/07 typed-error contract) — narrows the
// pattern-0 port for aggregator-only callers of FinalizeAggregateParent. The canonical
// SQLiteStore implements this (see FinalizeAggregateParent below). Other broker
// adapters (e.g. future Postgres) MUST also implement it.
//
// FASE 2 (July 2026): expectedVersion added for version-based CAS.
type aggregateFlipper interface {
	FinalizeAggregateParent(ctx context.Context, id string, targetStatus job.Status, result []byte, errMsg string, expectedVersion int) error
}

// ── FinalizeAggregateParent (post-fan-out parent state finalisation, no-lease CAS) ───
//
// AUDIT 2026-07-03 P0 #1 closure. The parent voiceover.generate handler
// emits (parent_state=waiting_children, nil) on full-fanout success; the
// worker-side Complete path flips broker.status=SUCCEEDED and emits
// JOB_COMPLETED outbox event. The aggregator's tick then re-reads the
// parent's children, computes the aggregate child outcome, and must
// re-finalise the parent — sometimes preserving SUCCEEDED + new
// parent_state (succeeded/partial_success), sometimes flipping the
// broker-level status from SUCCEEDED to FAILED (when ALL children
// definitively failed per the godlike/07 P0.1 false-success gate).
//
// Why no-lease CAS: by the time the aggregator ticks this job, the parent
// job's worker-side lease is released (HandleJob returned → tools.Complete
// → repo.Complete cleared worker_id=”, lease_id=”, bumped revision).
// ValidateOwnership-style worker/lease/revision CAS would always reject —
// the aggregator has no lease to compare against. The no-lease CAS guard
// is on (status, json_extract(result_json,'$.parent_state')) instead:
//
//	AND status IN ('RUNNING','FINALIZING','SUCCEEDED')   — pre-terminal + worker-just-completed
//	AND json_extract(result_json,'$.parent_state')
//	    IN ('waiting_children','partial_success')       — awaiting flip
//
// godlike/06 SSOT: this method is the SINGLE canonical writer of
// post-fan-out parent state transitions. No other code path may mutate
// jobs.status from non-terminal → terminalised after the worker has
// emitted JOB_COMPLETED. Worker's Complete + Aggregator's FinalizeAggregateParent
// are the only two writers; the worker's writes are pre-finalisation,
// the aggregator's writes are post-finalisation.
//
// godlike/07 typed-error contract: rows-affected=0 → re-read for
// root-cause (ErrAlreadyTerminalAggregate vs ErrAggregateCASConflict).
// The two sentinels are distinct because operator dashboards render
// them as different alert classes (replay-no-op vs race-concurrent.
// flip).
func (r *SQLiteStore) FinalizeAggregateParent(ctx context.Context, id string, targetStatus job.Status, result []byte, errMsg string, expectedVersion int) error {
	if id == "" {
		return fmt.Errorf("terminalFlip: id is empty")
	}
	if targetStatus != job.StatusSucceeded && targetStatus != job.StatusFailed {
		return fmt.Errorf("terminalFlip: targetStatus must be SUCCEEDED or FAILED, got %q", targetStatus)
	}

	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)
	resultJSON := string(result)
	if resultJSON == "" || resultJSON == "null" {
		resultJSON = "{}"
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("terminalFlip: begin tx: %w", err)
	}
	defer tx.Rollback()

	// FASE 2 (July 2026): version-based CAS — when expectedVersion > 0,
	// add `AND revision = expectedVersion` as a second fence. The
	// revision column is bumped on every state transition (Complete,
	// Fail, FinalizeAggregateParent) so a concurrent tick that already landed the
	// flip will have incremented revision, causing this UPDATE to
	// return 0 rows-affected → ErrAggregateCASConflict.
	query := `UPDATE jobs SET status = ?,
		completed_at = COALESCE(completed_at, ?),
		error = CASE WHEN ? = '' THEN error ELSE ? END,
		result_json = ?,
		progress = 100, worker_id = '', lease_id = '', lease_expiry = NULL,
		revision = revision + 1, updated_at = ?
	WHERE id = ?
		AND status IN ('RUNNING','FINALIZING','SUCCEEDED')
		AND json_extract(result_json,'$.parent_state') IN ('waiting_children','partial_success')`
	args := []any{string(targetStatus), nowStr, errMsg, errMsg, resultJSON, nowStr, id}
	if expectedVersion > 0 {
		query += `
		AND revision = ?`
		args = append(args, expectedVersion)
	}
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("terminalFlip: update: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		// Per code-reviewer PR-AGENT-P01 feedback (July 2026): bump the
		// canonical job_transition_conflict_total counter with a
		// distinct label so operator dashboards can disambiguate
		// aggregate-flip races from worker-side Complete/Fail races.
		observability.JobTransitionConflictTotal.WithLabelValues("aggregate_flip").Inc()
		// CAS guard rejected: distinguish "already terminal" from
		// "pre-flip retry path" by re-reading the row's status.
		var currentStatus job.Status
		var currentResult string
		err := tx.QueryRowContext(ctx,
			`SELECT status, COALESCE(result_json,'{}') FROM jobs WHERE id = ?`, id,
		).Scan(&currentStatus, &currentResult)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("%w: job %q not found", ErrJobNotFound, id)
			}
			return fmt.Errorf("terminalFlip: post-cas inspection: %w", err)
		}
		if currentStatus == job.StatusFailed || currentStatus == job.StatusCancelled {
			// Idempotent replay: parent already in a terminal sink. NOT a flip race
			// — the operator dashboard counts it as a no-op, not a conflict.
			return fmt.Errorf("%w: parent %q already in terminal sink %q", domainremote.ErrAlreadyTerminalAggregate, id, currentStatus)
		}
		// PRE-AGGREGATE CAS Budget consumed by observability.JobTransitionConflictTotal{aggregate_flip}
		// bumping above; here we return the typed ErrAggregateCASConflict for
		// caller errors.Is()-probe intake. The Retry path (queue→requeued) is
		// not a flip race and does NOT bump a separate counter.
		return fmt.Errorf("%w: parent %q status=%q not in (RUNNING|FINALIZING|SUCCEEDED), or parent_state not awaiting", domainremote.ErrAggregateCASConflict, id, currentStatus)
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	eventType := "job.aggregate_completed"
	if targetStatus == job.StatusFailed {
		eventType = "job.aggregate_failed"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, eventType, "parent aggregator terminal-flip (audit 2026-07-03 P0 #1 closure)",
		fmt.Sprintf(`{"target_status":%q}`, string(targetStatus)), nowStr); err != nil {
		return fmt.Errorf("terminalFlip: insert job event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("terminalFlip: commit: %w", err)
	}
	return nil
}

// ── ScheduleRetry ────────────────────────────────────────────────────────

// ScheduleRetry transitions a running job to retry_wait (or failed if retries exhausted).
func (r *SQLiteStore) ScheduleRetry(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, backoff time.Duration) error {
	j, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if j == nil {
		return ErrJobNotFound
	}
	if j.RetryCount >= j.MaxRetries {
		return r.Fail(ctx, id, workerID, leaseID, expectedRevision, "max retries exhausted")
	}

	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("scheduleRetry: begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'RETRY_WAIT', error = ?,
		 retry_count = retry_count + 1, worker_id = '', lease_id = '',
		 lease_expiry = NULL, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('RUNNING', 'FINALIZING')
		 AND worker_id = ? AND lease_id = ? AND revision = ?`,
		"scheduled for retry by worker "+workerID, nowStr,
		id, workerID, leaseID, expectedRevision)
	if err != nil {
		return fmt.Errorf("scheduleRetry: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		// PR-F: ScheduleRetry does NOT route through validateOwnership
		// (its fenced UPDATE carries the CAS check inline). Bump here on
		// the routed ErrTransitionConflict return. Distinct from the
		// err-typed branch above (which returns a wrapped error, not
		// ErrTransitionConflict) and from Retry's "max retries exhausted"
		// recursion into Fail (which uses method="fail" via the
		// validateOwnership path).
		observability.JobTransitionConflictTotal.WithLabelValues("schedule_retry").Inc()
		return ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_retry_wait", "job.Job scheduled for retry", "{}", nowStr); err != nil {
		return fmt.Errorf("scheduleRetry: insert job event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("scheduleRetry: commit: %w", err)
	}
	return nil
}

// ── Cancel ───────────────────────────────────────────────────────────────

// Cancel transitions a non-terminal job to cancelled. Idempotent.
func (r *SQLiteStore) Cancel(ctx context.Context, id string) error {
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'CANCELLED', cancelled_at = ?, worker_id = '',
		 lease_id = '', lease_expiry = NULL, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('QUEUED', 'LEASED', 'RUNNING', 'FINALIZING', 'RETRY_WAIT')`,
		nowStr, nowStr, id)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		// Idempotent: check if already cancelled/completed/failed.
		j, _ := r.Get(ctx, id)
		if j != nil && j.IsTerminal() {
			return nil
		}
		// PR-F: Cancel does not route through validateOwnership; bump
		// here before returning ErrTransitionConflict. The terminal-state
		// short-circuit above (return nil) is NOT a conflict and
		// intentionally not counted.
		observability.JobTransitionConflictTotal.WithLabelValues("cancel").Inc()
		return ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	if _, err := r.db.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_cancelled", "job.Job cancelled", "{}", nowStr); err != nil {
		return fmt.Errorf("cancel: insert job event: %w", err)
	}
	return nil
}

// ── DeadLetter ───────────────────────────────────────────────────────────

func (r *SQLiteStore) DeadLetter(ctx context.Context, id string, errMsg string) error {
	j, err := r.Get(ctx, id)
	if err != nil || j == nil {
		return fmt.Errorf("deadLetter: load job: %w", err)
	}
	payload := string(j.Payload)
	if payload == "" {
		payload = "{}"
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO dead_letter_jobs (job_id, job_type, correlation_id, error, payload_json, retry_count, failed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, j.Type, j.CorrelationID, errMsg, payload, j.RetryCount, timeutil.FormatRFC3339(time.Now()))
	return err
}

// ── Retry (transition retry_wait/failed → queued) ───────────────────────

// Retry re-enqueues a failed or retry_wait job.
func (r *SQLiteStore) Retry(ctx context.Context, id string) (*job.Job, error) {
	j, err := r.Get(ctx, id)
	if err != nil || j == nil {
		return nil, fmt.Errorf("retry: job %s not found", id)
	}
	if j.RetryCount >= j.MaxRetries {
		return nil, fmt.Errorf("retry: exhausted (%d/%d)", j.RetryCount, j.MaxRetries)
	}
	if j.Status != job.StatusRetryWait && j.Status != job.StatusFailed {
		return nil, fmt.Errorf("retry: invalid status %q", j.Status)
	}

	now := timeutil.FormatRFC3339(time.Now())
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'QUEUED', progress = 0, error = '',
		 worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('RETRY_WAIT', 'FAILED') AND revision = ?`,
		now, id, j.Revision)
	if err != nil {
		return nil, fmt.Errorf("retry: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		// PR-F: Retry does not route through validateOwnership; bump
		// here before returning ErrTransitionConflict. Distinct from
		// the inner c.ErrPath branches (retries-exhausted / invalid
		// status) which return pre-wrapped errors.
		observability.JobTransitionConflictTotal.WithLabelValues("retry").Inc()
		return nil, ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", time.Now().UnixNano(), hashutil.RandomString(6))
	if _, err := r.db.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_queued", "job.Job retry activated", "{}", now); err != nil {
		return nil, fmt.Errorf("retry: insert job event: %w", err)
	}

	// PR-Polling / ADR-0002 §D6.5 (June 2026): the requeued job
	// transitions back to QUEUED; wake every sleeping Worker so the
	// retry is picked up immediately. See repository.go::Create for
	// the canonical pattern.
	r.queueChanged()

	return r.Get(ctx, id)
}

// ── Convenience Wrappers ─────────────────────────────────────────────────

// MarkRunningJobsOlderThanFailed moves stale leased/running jobs to failed
// if their lease has expired beyond the given cutoff.
func (r *SQLiteStore) MarkRunningJobsOlderThanFailed(ctx context.Context, cutoff time.Time, reason string) (int, error) {
	now := timeutil.FormatRFC3339(time.Now())
	cutoffStr := timeutil.FormatRFC3339(cutoff)
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'FAILED', completed_at = ?, error = ?,
		 worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE status IN ('LEASED', 'RUNNING', 'FINALIZING') AND lease_expiry < ?`,
		now, reason, now, cutoffStr)
	if err != nil {
		return 0, fmt.Errorf("markRunningJobsOlderThanFailed: %w", err)
	}
	affected, _ := res.RowsAffected()
	return int(affected), nil
}

// AddEvent records a human-readable event on the job timeline.
func (r *SQLiteStore) AddEvent(ctx context.Context, id string, eventType, message string, data map[string]any) error {
	evtID := fmt.Sprintf("evt_%d_%s", time.Now().UnixNano(), hashutil.RandomString(6))
	dataJSON := "{}"
	if data != nil {
		if b, err := json.Marshal(data); err == nil {
			dataJSON = string(b)
		}
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, eventType, message, dataJSON, timeutil.FormatRFC3339(time.Now()))
	if err != nil {
		return fmt.Errorf("addEvent: %w", err)
	}
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

// validateOwnership checks that the current row matches the worker's
// expected lease + revision + status before any fenced UPDATE proceeds.
// PR-F / ADR-0002 §D6.7 (June 2026): the function takes a `method` arg
// so ErrTransitionConflict returns can bump the canonical
// job_transition_conflict_total{method=<name>} counter. The two
// non-TransitionConflict paths (ErrInvalidState, ErrLeaseLost) do NOT
// bump the counter — they're distinct signals
// (worker-called-wrong-transition vs different-worker-on-same-row) and
// merging them under "transition_conflict" would corrupt dashboard
// semantics. The method label is bounded by the 2 callers that route
// through this function (complete / fail); the other 3 fenced-UPDATE
// paths (schedule_retry / cancel / retry) bump at their own CAS-fence
// sites because they DO NOT pass through validateOwnership.
//
// FASE 2b (July 2026): expectedStatus is now variadic — the caller
// passes one or more allowed statuses. Complete/Fail accept both
// RUNNING and FINALIZING.
func validateOwnership(jobID string, method string, currentStatus job.Status,
	currentWorker, currentLease string, currentRevision int,
	expectedWorker, expectedLease string, expectedRevision int64,
	expectedStatuses ...job.Status) error {
	allowed := false
	for _, s := range expectedStatuses {
		if currentStatus == s {
			allowed = true
			break
		}
	}
	if !allowed {
		expectedStrs := make([]string, len(expectedStatuses))
		for i, s := range expectedStatuses {
			expectedStrs[i] = string(s)
		}
		return fmt.Errorf("%w: status %q, expected one of %v", ErrInvalidState, currentStatus, expectedStrs)
	}
	if currentWorker != expectedWorker {
		return fmt.Errorf("%w: worker %q, expected %q", ErrLeaseLost, currentWorker, expectedWorker)
	}
	if currentLease != expectedLease {
		return fmt.Errorf("%w: lease mismatch", ErrLeaseLost)
	}
	if int64(currentRevision) != expectedRevision {
		observability.JobTransitionConflictTotal.WithLabelValues(method).Inc()
		return fmt.Errorf("%w: revision %d, expected %d", ErrTransitionConflict, currentRevision, expectedRevision)
	}
	return nil
}
