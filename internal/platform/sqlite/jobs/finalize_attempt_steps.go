package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	domjob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// finalizeAttemptState carries the transaction-scoped state of one
// FinalizeAttempt call. Every phase receives the same value, so the single
// transaction, the CAS row and the outcome decision cannot drift apart
// between phases.
type finalizeAttemptState struct {
	tx  *sql.Tx
	cmd domjob.FinalizeAttemptCommand

	now    time.Time
	nowStr string

	// jobs row read under the CAS fence.
	status        domjob.Status
	workerID      string
	leaseID       string
	revision      int
	maxRetries    int
	retryCount    int
	jobType       string
	correlationID string

	decision finalizeAttemptDecision
}

// newFinalizeAttemptState freezes the command and the single clock reading
// used by every phase of one finalization.
func newFinalizeAttemptState(cmd domjob.FinalizeAttemptCommand) *finalizeAttemptState {
	st := &finalizeAttemptState{cmd: cmd, now: time.Now().UTC()}
	st.nowStr = timeutil.FormatRFC3339(st.now)
	return st
}

// loadFinalizeAttemptRow reads the row that carries the CAS fence, the
// retry-limit decision data and the DLQ columns.
func loadFinalizeAttemptRow(ctx context.Context, st *finalizeAttemptState) error {
	err := st.tx.QueryRowContext(ctx,
		`SELECT status, worker_id, lease_id, revision, max_retries, retry_count, type, COALESCE(correlation_id, '') FROM jobs WHERE id = ?`,
		st.cmd.JobID,
	).Scan(&st.status, &st.workerID, &st.leaseID, &st.revision, &st.maxRetries, &st.retryCount, &st.jobType, &st.correlationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrJobNotFound
		}
		return fmt.Errorf("finalizeAttempt: select jobs (id=%s): %w", st.cmd.JobID, err)
	}
	return nil
}

// checkFinalizeAttemptFence enforces the three CAS guards against the row read
// in loadFinalizeAttemptRow. A mismatch returns the canonical typed sentinel
// and the deferred rollback discards the transaction.
func checkFinalizeAttemptFence(st *finalizeAttemptState) error {
	if st.workerID != st.cmd.WorkerID {
		return fmt.Errorf("%w: worker mismatch (current=%q want=%q)", domjob.ErrLeaseLost, st.workerID, st.cmd.WorkerID)
	}
	if st.leaseID != st.cmd.LeaseID {
		return fmt.Errorf("%w: lease mismatch (current=%q want=%q)", domjob.ErrLeaseLost, st.leaseID, st.cmd.LeaseID)
	}
	if st.revision != st.cmd.ExpectedRevision {
		observability.JobTransitionConflictTotal.WithLabelValues("finalize_attempt").Inc()
		return fmt.Errorf("%w: revision %d, expected %d", domjob.ErrTransitionConflict, st.revision, st.cmd.ExpectedRevision)
	}
	return nil
}

// decideFinalizeAttemptOutcome computes the target status once. The
// retry-exhaustion downgrade lives in decideFinalizeAttempt; this phase only
// stores its result on the transaction state.
func decideFinalizeAttemptOutcome(st *finalizeAttemptState) error {
	decision, err := decideFinalizeAttempt(st.cmd.Outcome, st.cmd.ErrorMessage, st.retryCount, st.maxRetries)
	if err != nil {
		return err
	}
	st.decision = decision
	return nil
}

// applyFinalizeAttemptUpdate writes the single dynamic UPDATE and, for a
// success, the canonical result row. The WHERE clause repeats the CAS fence so
// a row that changed between the SELECT and the UPDATE cannot be finalized.
func applyFinalizeAttemptUpdate(ctx context.Context, st *finalizeAttemptState) error {
	var deferredUntil any
	if st.cmd.Outcome == domjob.OutcomeDeferred {
		// deferred_until is persisted at second precision; round UP so a
		// sub-second delay can never land in the past.
		due := st.now.Add(st.cmd.Backoff)
		if truncated := due.Truncate(time.Second); !truncated.Equal(due) {
			due = truncated.Add(time.Second)
		}
		deferredUntil = timeutil.FormatRFC3339(due)
	}
	setClauses := []string{
		"status = ?",
		"updated_at = ?",
		"revision = revision + 1",
		"completed_at = ?",
		"error = ?",
		"worker_id = ''",
		"lease_id = ''",
		"lease_expiry = NULL",
		"deferred_until = ?",
	}
	args := []any{st.decision.targetStatus, st.nowStr, st.nowStr, st.decision.errorMessage, deferredUntil}
	resultJSON := "{}"

	if st.cmd.Outcome == domjob.OutcomeSucceeded {
		resultJSON = string(st.cmd.Result)
		if resultJSON == "" || resultJSON == "null" {
			resultJSON = "{}"
		}
		setClauses = append(setClauses, "progress = 100")
	}
	if st.decision.incrementRetry {
		setClauses = append(setClauses, "retry_count = retry_count + 1")
	}

	whereClause := `WHERE id = ? AND status IN ('RUNNING', 'FINALIZING', 'RETRY_WAIT') AND worker_id = ? AND lease_id = ? AND revision = ?`
	whereArgs := []any{st.cmd.JobID, st.cmd.WorkerID, st.cmd.LeaseID, st.cmd.ExpectedRevision}
	query := fmt.Sprintf("UPDATE jobs SET %s %s", joinFinalizeClauses(setClauses), whereClause)
	args = append(args, whereArgs...)

	res, err := st.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("finalizeAttempt: update jobs (id=%s): %w", st.cmd.JobID, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		observability.JobTransitionConflictTotal.WithLabelValues("finalize_attempt").Inc()
		return domjob.ErrTransitionConflict
	}
	if st.cmd.Outcome == domjob.OutcomeSucceeded {
		if err := persistJobResult(ctx, st.tx, st.cmd.JobID, st.retryCount, resultJSON); err != nil {
			return fmt.Errorf("finalizeAttempt: %w", err)
		}
	}
	return nil
}

// recordFinalizeAttemptDLQ archives the failure in the same transaction as the
// status transition, so a DLQ orphan is structurally impossible.
func recordFinalizeAttemptDLQ(ctx context.Context, st *finalizeAttemptState) (bool, error) {
	if len(st.cmd.DLQPayload) == 0 {
		return false, nil
	}
	dlqRetryCount := st.retryCount
	if st.decision.incrementRetry {
		dlqRetryCount = st.retryCount + 1
	}
	if _, err := st.tx.ExecContext(ctx,
		`INSERT INTO dead_letter_jobs (job_id, job_type, correlation_id, error, payload_json, retry_count, failed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		st.cmd.JobID, st.jobType, st.correlationID, st.decision.errorMessage, string(st.cmd.DLQPayload), dlqRetryCount, st.nowStr,
	); err != nil {
		return false, fmt.Errorf("finalizeAttempt: DLQ insert (id=%s): %w", st.cmd.JobID, err)
	}
	return true, nil
}

// recordFinalizeArtifactState applies the optional artifact patch. A zero-row
// patch is a stale artifact, not a silent no-op.
func recordFinalizeArtifactState(ctx context.Context, st *finalizeAttemptState) error {
	if st.cmd.ArtifactState == nil {
		return nil
	}
	artRes, err := st.tx.ExecContext(ctx,
		`UPDATE artifact_stages SET state = ?, updated_at = ? WHERE id = ? AND job_id = ? AND state NOT IN ('SUCCEEDED', 'FAILED_PERMANENT')`,
		st.cmd.ArtifactState.NewState, st.nowStr, st.cmd.ArtifactState.ArtifactID, st.cmd.JobID,
	)
	if err != nil {
		return fmt.Errorf("finalizeAttempt: artifact-state patch (artifact=%s job=%s state=%s): %w",
			st.cmd.ArtifactState.ArtifactID, st.cmd.JobID, st.cmd.ArtifactState.NewState, err)
	}
	artAffected, _ := artRes.RowsAffected()
	if artAffected == 0 {
		return fmt.Errorf("%w: artifact=%s job=%s target_state=%s",
			ErrFinalizeAttemptArtifactStale, st.cmd.ArtifactState.ArtifactID, st.cmd.JobID, st.cmd.ArtifactState.NewState)
	}
	return nil
}

// recordFinalizeOutboxEvents inserts the optional outbox events with the
// event_key idempotency clause and returns the canonical written-key list.
func recordFinalizeOutboxEvents(ctx context.Context, st *finalizeAttemptState) ([]string, error) {
	written := make([]string, 0, len(st.cmd.OutboxEvents))
	for _, evt := range st.cmd.OutboxEvents {
		payloadJSON := string(evt.Payload)
		if payloadJSON == "" {
			payloadJSON = "{}"
		}
		if _, err := st.tx.ExecContext(ctx,
			`INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING`,
			evt.Type, st.cmd.JobID, st.jobType, payloadJSON, evt.EventKey, st.nowStr, st.nowStr,
		); err != nil {
			return nil, fmt.Errorf("finalizeAttempt: outbox insert (type=%s event_key=%s): %w", evt.Type, evt.EventKey, err)
		}
		written = append(written, evt.EventKey)
	}
	return written, nil
}

// recordFinalizeTimelineEvent appends the optional audit row.
func recordFinalizeTimelineEvent(ctx context.Context, st *finalizeAttemptState) error {
	if st.cmd.EventType == "" {
		return nil
	}
	if err := insertJobTimelineEvent(ctx, st.tx, st.cmd.JobID, st.cmd.EventType, st.decision.errorMessage, st.cmd.EventData, st.now); err != nil {
		return fmt.Errorf("finalizeAttempt: job_events insert (id=%s type=%s): %w", st.cmd.JobID, st.cmd.EventType, err)
	}
	return nil
}
