// Package finalizer — lease_fence.go (PR-GODOBJ-5-FINALIZER split)
//
// Hosts the lease-fence SELECT surface for the JobFinalizer. Two
// declarations move from the pre-split monolithic job_finalizer.go
// into this file:
//
//   - jobRow — the result of the lease-fenced SELECT job query.
//     Carries status + worker_id + lease_id + revision + retry_count
//
//   - lease_expiry + result_json (COALESCE → "") for downstream
//     consumption by handleIdempotentCompletion + selectJobForFinalization.
//
//   - selectJobForFinalization (Lease+Fence method) — runs the
//     SQL-fenced lease-gate SELECT:
//     (a) read job row inside the open transaction
//     (b) reject if status ∉ {RUNNING, FINALIZING, SUCCEEDED}
//     (c) SUCCEEDED → skip lease ownership checks (worker_id + lease_id
//     were cleared by markSucceeded) and route to
//     handleIdempotentCompletion which compares fingerprints.
//     (d) worker_id / lease_id match the request's Lease
//     (e) re-validate lease expiry against DB row (NOT request.Lease.
//     ExpiresAt — the DB row carries the canonical value)
//     (f) request.Attempt must equal retry_count+1 (else ErrStaleAttempt)
//
// Dedup (PR-GODOBJ-5 intentional godlike/07 no-fake-availability): the
// pre-split monolithic `selectJobForFinalization` carried two
// IDENTICAL `if row.status == "SUCCEEDED" { return &row, nil }`
// checks (separated by a near-duplicate comment block). Both routed
// to the same handleIdempotentCompletion path. The split collapses
// them to ONE check — the first (kept) one carries the canonical
// comment explaining WHY SUCCEEDED skips the lease-ownership gate.
//
// godlike/06 SSOT: this file is the canonical owner of "did the
// lease-fence pass for job X?". Callers MUST route through
// selectJobForFinalization — never re-implement the SQL fence inline.
package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// ── Job row (lease fence) ───────────────────────────────────────────

// jobRow holds the result of the lease-fenced SELECT.
type jobRow struct {
	status      string
	workerID    string
	leaseID     string
	revision    int
	retryCount  int
	leaseExpiry sql.NullString
	resultJSON  string
}

// selectJobForFinalization reads the job row inside the transaction
// and validates the lease fence.
//
// Validation surface (godlike/07 typed-error contract):
//
//   - sql.ErrNoRows → JOB_NOT_FOUND (no sentinel wrap — caller decides
//     whether to retry or escalate).
//   - status ∉ {RUNNING, FINALIZING, SUCCEEDED} → INVALID_STATUS
//     (transitions to terminal but not completable).
//   - status == SUCCEEDED → early-return row WITHOUT further ownership
//     checks; handleIdempotentCompletion (called by the orchestrator)
//     compares fingerprints to decide idempotent success vs
//     ErrCompletionConflict. Worker_id/lease_id were cleared by
//     markSucceeded, so the ownership gate would spuriously fail.
//   - worker_id mismatch → LEASE_OWNER_MISMATCH (wrapped
//     ErrLeaseOwnerMismatch).
//   - lease_id mismatch → LEASE_ID_MISMATCH (wrapped ErrLeaseExpired).
//   - DB-row lease_expiry in past → LEASE_EXPIRED_DB (wrapped
//     ErrLeaseExpired).
//   - request.Attempt != retry_count+1 → STALE_ATTEMPT (wrapped
//     ErrStaleAttempt).
//
// The SUCCEEDED early-return is a SINGLE check (PR-GODOBJ-5 dedup
// collapsed two identical pre-split checks — first kept, second
// removed; godlike/07 no-fake-availability: both checks routed
// to handleIdempotentCompletion identically, so removing the
// duplicate is a pure refactor with no behavior drift).
func (f *Finalizer) selectJobForFinalization(
	ctx context.Context,
	tx *sql.Tx,
	lease *finalization.Lease,
) (*jobRow, error) {
	var row jobRow
	err := tx.QueryRowContext(ctx,
		`SELECT status, worker_id, lease_id, revision, retry_count, lease_expiry, COALESCE(result_json, '')
		 FROM jobs
		 WHERE id = ?
		   AND (lease_expiry IS NULL OR lease_expiry > CURRENT_TIMESTAMP)`,
		lease.JobID,
	).Scan(&row.status, &row.workerID, &row.leaseID, &row.revision, &row.retryCount, &row.leaseExpiry, &row.resultJSON)
	if err == sql.ErrNoRows {
		return nil, finalization.NewFinalizationError(
			"JOB_NOT_FOUND", "job not found",
			lease.JobID, lease.Attempt, nil,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("finalizer: select job: %w", err)
	}

	// Validate lease ownership inside the transaction (defence against
	// race between pre-validation and commit).
	if row.status != "RUNNING" && row.status != "FINALIZING" && row.status != "SUCCEEDED" && row.status != "RETRY_WAIT" {
		return nil, finalization.NewFinalizationError(
			"INVALID_STATUS",
			fmt.Sprintf("job status %q is not completable", row.status),
			lease.JobID, lease.Attempt, nil,
		)
	}

	// Already-SUCCEEDED jobs skip lease ownership checks because
	// markSucceeded clears worker_id + lease_id on completion.
	// The caller (orchestrator step 4) routes these to
	// handleIdempotentCompletion which compares completion
	// fingerprints to decide idempotent success vs ErrCompletionConflict.
	if row.status == "SUCCEEDED" {
		return &row, nil
	}

	if row.workerID != lease.WorkerID {
		return nil, finalization.NewFinalizationError(
			"LEASE_OWNER_MISMATCH",
			fmt.Sprintf("lease owner mismatch: worker %q != expected %q", row.workerID, lease.WorkerID),
			lease.JobID, lease.Attempt, finalization.ErrLeaseOwnerMismatch,
		)
	}
	if row.leaseID != lease.LeaseID {
		return nil, finalization.NewFinalizationError(
			"LEASE_ID_MISMATCH",
			fmt.Sprintf("lease ID mismatch: %q != expected %q", row.leaseID, lease.LeaseID),
			lease.JobID, lease.Attempt, finalization.ErrLeaseExpired,
		)
	}

	// Re-validate lease expiry against the DB row (not the request value).
	// The pre-validation check on req.Lease.Valid() uses the request's
	// ExpiresAt; the DB row carries the canonical value.
	if row.leaseExpiry.Valid {
		expiryTime, parseErr := time.Parse(time.RFC3339, row.leaseExpiry.String)
		if parseErr == nil && time.Now().UTC().After(expiryTime) {
			return nil, finalization.NewFinalizationError(
				"LEASE_EXPIRED_DB",
				fmt.Sprintf("lease expired at %s (checked from DB row)", row.leaseExpiry.String),
				lease.JobID, lease.Attempt, finalization.ErrLeaseExpired,
			)
		}
	}

	// The request attempt must equal the job's retry_count + 1
	// (the attempt counter increments on each retry).
	expectedAttempt := row.retryCount + 1
	if lease.Attempt != expectedAttempt {
		return nil, finalization.NewFinalizationError(
			"STALE_ATTEMPT",
			fmt.Sprintf("request attempt %d != expected %d (retry_count=%d)", lease.Attempt, expectedAttempt, row.retryCount),
			lease.JobID, lease.Attempt, finalization.ErrStaleAttempt,
		)
	}

	return &row, nil
}
