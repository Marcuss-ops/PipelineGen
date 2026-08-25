package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	domainremote "github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// parentStateTypedColumn is the SQL-side canonical constant for the
// `parent_state_typed` column added by migration 129 (P1.2 typed-state
// column migration, EXPAND→BACKFILL→CUTOVER sequence per
// architecture/current.yaml#PR-VO-PARENT-STATE-COLUMN).
//
// godlike/06 SSOT (one-canonical-owner-per-fact): the SQL layer
// (platform/sqlite/jobs) cannot import the voiceover
// application package due to canonical layering (application →
// infrastructure is the only legal direction). So this package-private
// constant is the SQL-side mirror of
// voiceover.JobParentStateColumn (the cross-package canonical owner).
// Any drift between the two is a build-failure on the application-layer
// constant declaration (per the godlike/07 typed-error contract).
//
// godlike/07 minimum-blast-radius: this constant is package-private
// (lowercase) — the application-layer canonical surface remains the
// single source of truth for cross-package consumers (e.g.,
// `internal/application/voiceover/jobs/parent_aggregator_state.go::JobParentStateColumn`).
// A future cross-package audit may promote this to a shared
// `internal/domain/job/parent_state_column.go` if the layering evolves.
const parentStateTypedColumn = "parent_state_typed"

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
// → repo.Complete cleared worker_id="", lease_id="", bumped revision).
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

	// PR-P1.2-SQL-DUAL-WRITE (July 2026): extract parent_state from
	// the result JSON so the typed parent_state_typed column is
	// written in the same transaction as the JSON result column.
	// The typed column is the AUTHORITATIVE source going forward
	// (godlike/06 SSOT — one canonical column per fact). When the
	// result JSON has no parent_state key, the typed column stays
	// empty (the DEFAULT '' from the migration preserves back-compat).
	//
	// godlike/07 fail-closed (no-fake-availability): if the JSON is
	// non-empty AND cannot be parsed, the write aborts with the
	// typed ErrInvalidResultJSON sentinel (silent-swallow would
	// let a corrupt split-brain state land on disk). The
	// deferred tx.Rollback ensures atomicity: the typed column is
	// NOT populated in the malformed-JSON path.
	var parentStateTyped string
	if len(result) > 0 {
		var resultMap map[string]any
		if err := json.Unmarshal(result, &resultMap); err != nil {
			return fmt.Errorf("terminalFlip: %w: %v", ErrInvalidResultJSON, err)
		}
		if ps, ok := resultMap["parent_state"].(string); ok {
			parentStateTyped = ps
		}
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
	//
	// PR-P1.2-SQL-DUAL-WRITE (July 2026): parent_state_typed column
	// is written atomically with result_json in the same UPDATE.
	// godlike/06 SSOT: the column name comes from the package-private
	// parentStateTypedColumn constant (the SQL-side mirror of
	// voiceover.JobParentStateColumn) — the literal is NEVER repeated
	// elsewhere in this package.
	query := `UPDATE jobs SET status = ?,
		completed_at = COALESCE(completed_at, ?),
		error = CASE WHEN ? = '' THEN error ELSE ? END,
		result_json = ?,
		` + parentStateTypedColumn + ` = ?,
		progress = 100, worker_id = '', lease_id = '', lease_expiry = NULL,
		revision = revision + 1, updated_at = ?
	WHERE id = ?
		AND status IN ('WAITING_CHILDREN','RUNNING','FINALIZING','SUCCEEDED')
		AND (
			json_extract(result_json,'$.parent_state') IN ('waiting_children','partial_success')
			OR json_extract(result_json,'$.data.parent_state') IN ('waiting_children','partial_success')
		)`
	args := []any{string(targetStatus), nowStr, errMsg, errMsg, resultJSON, parentStateTyped, nowStr, id}
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
		return fmt.Errorf("%w: parent %q status=%q not in (WAITING_CHILDREN|RUNNING|FINALIZING|SUCCEEDED), or parent_state not awaiting", domainremote.ErrAggregateCASConflict, id, currentStatus)
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
