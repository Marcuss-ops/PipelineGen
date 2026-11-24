// Package jobs — finalize_attempt.go: canonical SQLiteStore.FinalizeAttempt
// implementation (Fase 4(a), July 2026).
//
// FinalizeAttempt is the SINGLE canonical writer of terminal state transitions
// out of {SUCCEEDED, FAILED, RETRY_WAIT}. It consolidates the pre-Fase-4
// Complete / Fail / ScheduleRetry paths (plus optional dead_letter_jobs
// archive, artifact_state patch, outbox_events emit and job_events audit row)
// into ONE atomic SQLite transaction.
//
// # Single-transaction flow
//
//  1. validateFinalizeAttemptCommand — pre-TX fail-closed validation.
//  2. loadFinalizeAttemptRow — row carrying CAS fence + retry + DLQ data.
//  3. checkFinalizeAttemptFence — worker_id / lease_id / revision guards.
//  4. decideFinalizeAttemptOutcome — target status and retry downgrade.
//  5. applyFinalizeAttemptUpdate — dynamic UPDATE + canonical result row.
//  6. recordFinalizeAttemptDLQ — optional DLQ archive.
//  7. recordFinalizeArtifactState — optional artifact-state patch.
//  8. recordFinalizeOutboxEvents — optional idempotent outbox events.
//  9. recordFinalizeTimelineEvent — optional job_events audit row.
//  10. COMMIT — single atomic commit; the returned struct is the post-commit
//     source of truth (callers MUST NOT re-query the jobs row).
//
// Every phase runs inside the same *sql.Tx. A failure returns a typed sentinel
// (ErrLeaseLost, ErrTransitionConflict, ErrFinalizeAttemptArtifactStale, …)
// and the deferred Rollback discards the whole transaction: no partial
// terminal state can be observed.
package jobs

import (
	"context"
	"fmt"

	domjob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── FinalizeAttempt typed-error sentinels ───────────────────────────────
//
// Fase 5(a) canonical-home alignment (July 2026): the sentinels are thin
// re-export aliases of the canonical declarations at
// internal/kernel/job/errors.go. Identity is preserved (same error value), so
// callers that probe jobs.ErrFinalizeAttempt* keep working unchanged.
var (
	ErrFinalizeAttemptOutcomeInvalid       = domjob.ErrFinalizeAttemptOutcomeInvalid
	ErrFinalizeAttemptResultMissing        = domjob.ErrFinalizeAttemptResultMissing
	ErrFinalizeAttemptErrorMissing         = domjob.ErrFinalizeAttemptErrorMissing
	ErrFinalizeAttemptArtifactStale        = domjob.ErrFinalizeAttemptArtifactStale
	ErrFinalizeAttemptOutboxEventMissing   = domjob.ErrFinalizeAttemptOutboxEventMissing
	ErrFinalizeAttemptDLQIncompatible      = domjob.ErrFinalizeAttemptDLQIncompatible
	ErrFinalizeAttemptDeferralDelayMissing = domjob.ErrFinalizeAttemptDeferralDelayMissing
)

// FinalizeAttempt is the canonical consolidated terminal-decision primitive.
//
// Implements domjob.Store.FinalizeAttempt (see internal/kernel/job/store.go).
// See internal/kernel/job/finalize_commands.go for the typed-envelope contract.
//
// Returns the canonical (FinalStatus, NewRevision, DLQRecorded,
// OutboxEventsWritten) projection. Callers MUST NOT re-query the jobs row to
// "double-check" — the returned FinalizeAttemptResult IS the post-commit
// source of truth (godlike/06 SSOT).
func (r *SQLiteStore) FinalizeAttempt(ctx context.Context, cmd domjob.FinalizeAttemptCommand) (domjob.FinalizeAttemptResult, error) {
	if err := validateFinalizeAttemptCommand(cmd); err != nil {
		return domjob.FinalizeAttemptResult{}, err
	}

	st := newFinalizeAttemptState(cmd)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domjob.FinalizeAttemptResult{}, fmt.Errorf("finalizeAttempt: begin tx: %w", err)
	}
	st.tx = tx
	defer func() { _ = tx.Rollback() }()
	if err := loadFinalizeAttemptRow(ctx, st); err != nil {
		return domjob.FinalizeAttemptResult{}, err
	}
	if err := checkFinalizeAttemptFence(st); err != nil {
		return domjob.FinalizeAttemptResult{}, err
	}
	if err := decideFinalizeAttemptOutcome(st); err != nil {
		return domjob.FinalizeAttemptResult{}, err
	}
	if err := applyFinalizeAttemptUpdate(ctx, st); err != nil {
		return domjob.FinalizeAttemptResult{}, err
	}
	dlqRecorded, err := recordFinalizeAttemptDLQ(ctx, st)
	if err != nil {
		return domjob.FinalizeAttemptResult{}, err
	}
	if err := recordFinalizeArtifactState(ctx, st); err != nil {
		return domjob.FinalizeAttemptResult{}, err
	}
	outboxWritten, err := recordFinalizeOutboxEvents(ctx, st)
	if err != nil {
		return domjob.FinalizeAttemptResult{}, err
	}
	if err := recordFinalizeTimelineEvent(ctx, st); err != nil {
		return domjob.FinalizeAttemptResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return domjob.FinalizeAttemptResult{}, fmt.Errorf("finalizeAttempt: commit (id=%s): %w", cmd.JobID, err)
	}

	return domjob.FinalizeAttemptResult{
		JobID:               cmd.JobID,
		FinalStatus:         st.decision.targetStatus,
		NewRevision:         st.revision + 1,
		DLQRecorded:         dlqRecorded,
		OutboxEventsWritten: outboxWritten,
	}, nil
}

// joinFinalizeClauses joins "col = ?" placeholders with ", " for the dynamic
// UPDATE SET clause.
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
