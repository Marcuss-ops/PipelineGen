package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	domainremote "github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

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
		return job.ErrTransitionConflict
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
		return job.ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	evtData, _ := json.Marshal(map[string]string{"error": errMsg})
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_failed", errMsg, string(evtData), nowStr); err != nil {
		return fmt.Errorf("fail: insert job event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("fail: commit: %w", err)
	}
	return nil
}
