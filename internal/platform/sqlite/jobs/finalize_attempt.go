// Package jobs — finalize_attempt.go: canonical SQLiteStore.FinalizeAttempt
// implementation (Fase 4(a), July 2026).
//
// Consolidates the four pre-Fase-4 sibling paths (Complete / Fail /
// ScheduleRetry — plus optional dead_letter_jobs archive + artifact_state
// patch + outbox_events emit + job_events audit row) into ONE atomic SQLite
// transaction.
//
// # Push 4.6 cutover commitment (godlike/06 SSOT)
//
// Per the user's "Hard-break now" commitment on Fase 4(a), the four
// pre-Fase-4 public methods on kernel.Store (Complete / Fail /
// ScheduleRetry / DeadLetter) WILL be REMOVED at Push 4.6 (caller
// migration). Until then they remain on the SQLiteStore + kernel.Store so
// pre-Fase-4 callers continue compiling. Push 4.6 will:
//  1. Re-route local.Broker.Fail/Complete/ScheduleRetry/DeadLetter +
//     remote.Client.Fail/Complete/ScheduleRetry/DeadLetter to
//     delegate to SQLiteStore.FinalizeAttempt (this file).
//  2. Update the 60+ mockBroker test stubs (handler_workers_test.go,
//     worker_registry_e2e_test.go, runner_test.go, etc.) to use
//     FinalizeAttempt semantics.
//  3. Remove the kernel.Store.{Complete,Fail,ScheduleRetry,DeadLetter}
//     interface methods (HARMBREAK).
//
// This push adds FinalizeAttempt as the canonical surface but PRESERVES
// the legacy methods for the migration window. Single-mergeable scope
// per AGENTS.md "Keep commits focused".
//
// # Single-tx flow (9 steps)
//
//  1. SELECT jobs row (status, worker_id, lease_id, revision, max_retries,
//     retry_count, type, correlation_id)  — carries CAS-fence + retry-
//     limit decision data + DLQ-archive columns.
//  2. Validate Outcome + Outcome-specific precondition (Succeeded
//     requires Result; FailedPermanent/ScheduleRetry require ErrorMessage).
//  3. Compute target status (with retry-exhaustion downgrade logic for
//     ScheduleRetry → atomic downgrade to FAILED when retry_count
//     already at max_retries).
//  4. UPDATE jobs SET status/revision/lock-clear + outcome-specific
//     columns (result_json + progress only for Succeeded;
//     retry_count += 1 only for upgrade-allowed ScheduleRetry).
//  5. (optional) INSERT INTO dead_letter_jobs (when DLQPayload non-nil
//     AND Outcome ∈ {FailedPermanent, ScheduleRetry-downgraded-to-Failed}).
//  6. (optional) UPDATE artifact_stages SET state (when ArtifactState
//     non-nil; rejection on terminal-state already-terminals).
//  7. (optional) INSERT INTO outbox_events × N (one per cmd.OutboxEvents
//     entry; ON CONFLICT(event_key) DO NOTHING idempotency).
//  8. (optional) INSERT INTO job_events audit row (when EventType non-empty).
//  9. COMMIT — single atomic commit.
//
// godlike/06 SSOT: this method is the SINGLE canonical writer of terminal
// state transitions out of {SUCCEEDED, FAILED, RETRY_WAIT}. The legacy
// Complete/Fail/ScheduleRetry methods remain on SQLiteStore for pre-Fase-4
// caller compat; they will be removed in Push 4.6 (see above).
//
// godlike/07 fail-closed (no-fake-availability): each TX-internal step
// either runs to completion or rolls back the entire transaction via
// `defer tx.Rollback`. CAS-fence mismatches on step 4 return
// domjob.ErrTransitionConflict / domjob.ErrLeaseLost without TX commit; stale
// artifact-state patches on step 6 surface ErrFinalizeAttemptArtifactStale;
// outbox Events with empty Type or EventKey are rejected pre-TX. Silent-
// default values are explicit typed sentinels, NEVER empty defaults.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	domjob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── FinalizeAttempt typed-error sentinels ───────────────────────────────
//
// Each sentinel is a per-failure-mode typed signal so the caller can
// errors.Is-probe distinct failure classes. Routes:
//
//   - ErrFinalizeAttemptOutcomeInvalid   — caller supplied an outcome
//     outside the canonical enum (godlike/07 fail-closed guard at the FENCE).
//   - ErrFinalizeAttemptResultMissing    — OutcomeSucceeded with empty
//     cmd.Result (would silently-default to {} on the row, violating
//     wire consistency).
//   - ErrFinalizeAttemptErrorMissing     — non-Succeeded outcome with empty
//     cmd.ErrorMessage (silent-empty error message is a hostile trap).
//   - ErrFinalizeAttemptArtifactStale    — ArtifactState patch did NOT
//     match (artifact missing, wrong job_id, or already-terminal state).
//   - ErrFinalizeAttemptOutboxEventMissing — OutboxEvent entry has empty
//     Type or EventKey (would violate event_key UNIQUE idempotency).
//   - ErrFinalizeAttemptDLQIncompatible  — DLQPayload supplied with
//     OutcomeSucceeded (incompatible — DLQ is for terminal failure).
//
// Fase 5(a) canonical-home alignment (July 2026): the 6 sentinels
// below are thin re-export aliases of the canonical declarations at
// `internal/kernel/job/errors.go`. Identity is preserved (same
// `error` value); Push 4.2 callers (`errors.Is(err,
// jobs.ErrFinalizeAttemptOutcomeInvalid)`) compile and probe
// unchanged. The `.Error()` message returns the domjob-formatted
// text (canonical surface).
var (
	ErrFinalizeAttemptOutcomeInvalid     = domjob.ErrFinalizeAttemptOutcomeInvalid
	ErrFinalizeAttemptResultMissing      = domjob.ErrFinalizeAttemptResultMissing
	ErrFinalizeAttemptErrorMissing       = domjob.ErrFinalizeAttemptErrorMissing
	ErrFinalizeAttemptArtifactStale      = domjob.ErrFinalizeAttemptArtifactStale
	ErrFinalizeAttemptOutboxEventMissing = domjob.ErrFinalizeAttemptOutboxEventMissing
	ErrFinalizeAttemptDLQIncompatible    = domjob.ErrFinalizeAttemptDLQIncompatible
)

// FinalizeAttempt is the canonical consolidated terminal-decision primitive.
//
// Implements domjob.Store.FinalizeAttempt (see internal/kernel/job/store.go).
// See internal/kernel/job/finalize_commands.go for the typed-envelope contract.
//
// Returns the canonical (FinalStatus, NewRevision, DLQRecorded,
// OutboxEventsWritten) projection. Callers MUST NOT re-query the jobs row
// to "double-check" — the returned FinalizeAttemptResult struct IS the
// post-commit source of truth (godlike/06 SSOT).
func (r *SQLiteStore) FinalizeAttempt(ctx context.Context, cmd domjob.FinalizeAttemptCommand) (domjob.FinalizeAttemptResult, error) {
	// ── Pre-TX precondition validation ─────────────────────────────────
	// godlike/07 fail-closed: reject BEFORE BeginTx so a bad command
	// doesn't pin a connection in a doomed transaction. Unknown enum
	// values, missing required fields, and incompatible combinations
	// are surfaced as typed sentinels the caller can errors.Is-probe.
	if err := validateFinalizeAttemptCommand(cmd); err != nil {
		return domjob.FinalizeAttemptResult{}, err
	}

	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	// ── TX BEGIN ──────────────────────────────────────────────────────
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domjob.FinalizeAttemptResult{}, fmt.Errorf("finalizeAttempt: begin tx: %w", err)
	}
	defer tx.Rollback()

	// ── Step 1: read jobs row for CAS fence + retry-limit + DLQ data ───
	var (
		status        domjob.Status
		curWorkerID   string
		curLeaseID    string
		revision      int
		maxRetries    int
		retryCount    int
		jobType       string
		correlationID string
	)
	err = tx.QueryRowContext(ctx,
		`SELECT status, worker_id, lease_id, revision, max_retries, retry_count, type, COALESCE(correlation_id, '') FROM jobs WHERE id = ?`,
		cmd.JobID,
	).Scan(&status, &curWorkerID, &curLeaseID, &revision, &maxRetries, &retryCount, &jobType, &correlationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domjob.FinalizeAttemptResult{}, ErrJobNotFound
		}
		return domjob.FinalizeAttemptResult{}, fmt.Errorf("finalizeAttempt: select jobs (id=%s): %w", cmd.JobID, err)
	}

	// ── Step 2: CAS fence ─────────────────────────────────────────────
	// godlike/07 fail-closed: the three CAS guards (worker_id, lease_id,
	// revision) are evaluated against the row read in step 1. A mismatch
	// returns the canonical domjob.ErrLeaseLost / domjob.ErrTransitionConflict typed
	// sentinel; the TX is rolled back via the deferred Rollback().
	if curWorkerID != cmd.WorkerID {
		return domjob.FinalizeAttemptResult{}, fmt.Errorf("%w: worker mismatch (current=%q want=%q)", domjob.ErrLeaseLost, curWorkerID, cmd.WorkerID)
	}
	if curLeaseID != cmd.LeaseID {
		return domjob.FinalizeAttemptResult{}, fmt.Errorf("%w: lease mismatch (current=%q want=%q)", domjob.ErrLeaseLost, curLeaseID, cmd.LeaseID)
	}
	if revision != cmd.ExpectedRevision {
		observability.JobTransitionConflictTotal.WithLabelValues("finalize_attempt").Inc()
		return domjob.FinalizeAttemptResult{}, fmt.Errorf("%w: revision %d, expected %d", domjob.ErrTransitionConflict, revision, cmd.ExpectedRevision)
	}

	// ── Step 3: compute target status + outcome-specific compilation ──
	// The retry-exhaustion downgrade logic preserves the pre-Fase-4
	// ScheduleRetry → Fail recursion at lifecycle_aggregation.go:28.
	//
	// godlike/07 no-mutate-input: the downgraded ErrorMessage is
	// computed into a LOCAL var (errorMessage) so the caller's struct
	// field is never mutated. If we mutated cmd.ErrorMessage, a caller
	// reusing cmd across retries (e.g. a worker with retry-on-failure
	// loop) would silently propagate the suffix across all retries.
	decision, err := decideFinalizeAttempt(cmd.Outcome, cmd.ErrorMessage, retryCount, maxRetries)
	if err != nil {
		return domjob.FinalizeAttemptResult{}, err
	}
	targetStatus := decision.targetStatus
	incrementRetry := decision.incrementRetry
	errorMessage := decision.errorMessage
	// errorMessage is consumed by steps 4 (SET error=?), 5 (DLQ error=?),
	// and 8 (job_events message=?); the local-var assignment above
	// documents the no-mutate-input contract for the retry-exhaustion
	// downgrade branch.

	// ── Step 4: dynamic UPDATE jobs SET ... ───────────────────────────
	// Common SET clauses (always applied): status, revision++1, lock-clear,
	// completed_at, error. Outcome-specific SET clauses:
	//   - Succeeded   : + result_json, + progress=100
	//   - ScheduleRetry + retry allowed: + retry_count += 1
	//   - FailedPermanent, ScheduleRetry-downgraded-to-Failed : (no extra)
	setClauses := []string{
		"status = ?",
		"updated_at = ?",
		"revision = revision + 1",
		"completed_at = ?",
		"error = ?",
		"worker_id = ''",
		"lease_id = ''",
		"lease_expiry = NULL",
	}
	args := []any{targetStatus, nowStr, nowStr, errorMessage}
	resultJSON := "{}"

	if cmd.Outcome == domjob.OutcomeSucceeded {
		resultJSON = string(cmd.Result)
		if resultJSON == "" || resultJSON == "null" {
			// Defensive — cmd.Result was non-empty in the precondition,
			// but content might be JSON-null; normalize to "{}".
			resultJSON = "{}"
		}
		setClauses = append(setClauses, "progress = 100")
	}
	if incrementRetry {
		setClauses = append(setClauses, "retry_count = retry_count + 1")
	}

	whereClause := `WHERE id = ? AND status IN ('RUNNING', 'FINALIZING', 'RETRY_WAIT') AND worker_id = ? AND lease_id = ? AND revision = ?`
	whereArgs := []any{cmd.JobID, cmd.WorkerID, cmd.LeaseID, cmd.ExpectedRevision}
	query := fmt.Sprintf("UPDATE jobs SET %s %s", joinFinalizeClauses(setClauses), whereClause)
	args = append(args, whereArgs...)

	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return domjob.FinalizeAttemptResult{}, fmt.Errorf("finalizeAttempt: update jobs (id=%s): %w", cmd.JobID, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		observability.JobTransitionConflictTotal.WithLabelValues("finalize_attempt").Inc()
		return domjob.FinalizeAttemptResult{}, domjob.ErrTransitionConflict
	}
	if cmd.Outcome == domjob.OutcomeSucceeded {
		if err := persistJobResult(ctx, tx, cmd.JobID, retryCount, resultJSON); err != nil {
			return domjob.FinalizeAttemptResult{}, fmt.Errorf("finalizeAttempt: %w", err)
		}
	}

	// ── Step 5: optional DLQ archive ──────────────────────────────────
	// Schema (canonical migration): dead_letter_jobs (job_id, job_type,
	// correlation_id, error, payload_json, retry_count, failed_at).
	// The pre-Fase-4 DeadLetter (lifecycle_aggregation.go:114) is a
	// separate non-atomic method that reads the job + writes DLQ; Fase 4
	// folds the DLQ-write into the same TX as the status transition so
	// a DLQ-orphan (status flipped without DLQ row) is structurally
	// impossible.
	dlqRecorded := false
	if len(cmd.DLQPayload) > 0 {
		dlqRetryCount := retryCount
		if incrementRetry {
			dlqRetryCount = retryCount + 1
		}
		if _, dlqErr := tx.ExecContext(ctx,
			`INSERT INTO dead_letter_jobs (job_id, job_type, correlation_id, error, payload_json, retry_count, failed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			cmd.JobID, jobType, correlationID, errorMessage, string(cmd.DLQPayload), dlqRetryCount, nowStr,
		); dlqErr != nil {
			return domjob.FinalizeAttemptResult{}, fmt.Errorf("finalizeAttempt: DLQ insert (id=%s): %w", cmd.JobID, dlqErr)
		}
		dlqRecorded = true
	}

	// ── Step 6: optional artifact-state patch ─────────────────────────
	// Schema (migration 147 from Push 3.1a): artifact_stages (id,
	// job_id, ..., state, ...). The UPDATE is fenced to a non-terminal
	// state (SUCCEEDED / FAILED_PERMANENT) so a caller cannot silently
	// re-patch an already-terminal artifact row (godlike/07 fail-closed:
	// re-patching a terminal artifact is observably a no-op; throwing
	// ErrFinalizeAttemptArtifactStale surfaces the bug to operators).
	if cmd.ArtifactState != nil {
		artRes, artErr := tx.ExecContext(ctx,
			`UPDATE artifact_stages SET state = ?, updated_at = ? WHERE id = ? AND job_id = ? AND state NOT IN ('SUCCEEDED', 'FAILED_PERMANENT')`,
			cmd.ArtifactState.NewState, nowStr, cmd.ArtifactState.ArtifactID, cmd.JobID,
		)
		if artErr != nil {
			return domjob.FinalizeAttemptResult{}, fmt.Errorf("finalizeAttempt: artifact-state patch (artifact=%s job=%s state=%s): %w",
				cmd.ArtifactState.ArtifactID, cmd.JobID, cmd.ArtifactState.NewState, artErr)
		}
		artAffected, _ := artRes.RowsAffected()
		if artAffected == 0 {
			return domjob.FinalizeAttemptResult{}, fmt.Errorf("%w: artifact=%s job=%s target_state=%s",
				ErrFinalizeAttemptArtifactStale, cmd.ArtifactState.ArtifactID, cmd.JobID, cmd.ArtifactState.NewState)
		}
	}

	// ── Step 7: optional outbox events ────────────────────────────────
	// Canonical outbox_events schema (event_type, aggregate_id,
	// aggregate_type, payload_json, event_key, created_at, updated_at) +
	// ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	// idempotency. We surface the event_key as the canonical reference
	// (outbox_events.id is INT64; the caller can join via
	// outboxevents.Repository.GetByEventKey if it needs the int64 id).
	// Forward-pointer: Push 4.5 may route this through the typed
	// outboxevents.Repository.Enqueue port for canonical Inserted/Suppressed
	// feedback; today the inline-INSERT mirror is the SQL-layer
	// implementation.
	outboxWritten := make([]string, 0, len(cmd.OutboxEvents))
	for _, evt := range cmd.OutboxEvents {
		payloadJSON := string(evt.Payload)
		if payloadJSON == "" {
			payloadJSON = "{}"
		}
		aggregateID := cmd.JobID
		aggregateType := jobType
		if _, outErr := tx.ExecContext(ctx,
			`INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING`,
			evt.Type, aggregateID, aggregateType, payloadJSON, evt.EventKey, nowStr, nowStr,
		); outErr != nil {
			return domjob.FinalizeAttemptResult{}, fmt.Errorf("finalizeAttempt: outbox insert (type=%s event_key=%s): %w", evt.Type, evt.EventKey, outErr)
		}
		outboxWritten = append(outboxWritten, evt.EventKey)
	}

	// ── Step 8: optional job_events audit row ─────────────────────────
	// Mirrors the pre-Fase-4 pattern at lifecycle_complete.go:113 for
	// the {complete, fail, schedule_retry} trio. EventData is encoded
	// as JSON; nil becomes "{}". EventType empty short-circuits (no row).
	if cmd.EventType != "" {
		dataJSON := "{}"
		if cmd.EventData != nil {
			if b, jmErr := json.Marshal(cmd.EventData); jmErr == nil {
				dataJSON = string(b)
			}
		}
		evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
		if _, evErr := tx.ExecContext(ctx,
			`INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			evtID, cmd.JobID, cmd.EventType, errorMessage, dataJSON, nowStr,
		); evErr != nil {
			return domjob.FinalizeAttemptResult{}, fmt.Errorf("finalizeAttempt: job_events insert (id=%s type=%s): %w", cmd.JobID, cmd.EventType, evErr)
		}
	}

	// ── Step 9: COMMIT ─────────────────────────────────────────────────
	if err := tx.Commit(); err != nil {
		return domjob.FinalizeAttemptResult{}, fmt.Errorf("finalizeAttempt: commit (id=%s): %w", cmd.JobID, err)
	}

	// godlike/06 SSOT: returned struct is the canonical post-commit surface.
	return domjob.FinalizeAttemptResult{
		JobID:               cmd.JobID,
		FinalStatus:         targetStatus,
		NewRevision:         revision + 1,
		DLQRecorded:         dlqRecorded,
		OutboxEventsWritten: outboxWritten,
	}, nil
}

// joinFinalizeClauses is a tiny helper that joins a slice of "col = ?"
// placeholders with ", " separators for use inside an UPDATE SET clause.
// Repeated here (mirrors repository_commands.go::joinClauses) to keep
// this file self-contained for review purposes. A future refactor push
// may unify both helpers in a single job_store_helper.go file.
func joinFinalizeClauses(clauses []string) string {
	out := ""
	for i, c := range clauses {
		if i > 0 {
			out += ", "
		}
		out += c
	}
	return out
}
