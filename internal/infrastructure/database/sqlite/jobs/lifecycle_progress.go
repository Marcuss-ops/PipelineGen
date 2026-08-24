package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
// so job.ErrTransitionConflict returns can bump the canonical
// job_transition_conflict_total{method=<name>} counter. The two
// non-TransitionConflict paths (ErrInvalidState, job.ErrLeaseLost) do NOT
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
		return fmt.Errorf("%w: worker %q, expected %q", job.ErrLeaseLost, currentWorker, expectedWorker)
	}
	if currentLease != expectedLease {
		return fmt.Errorf("%w: lease mismatch", job.ErrLeaseLost)
	}
	if int64(currentRevision) != expectedRevision {
		observability.JobTransitionConflictTotal.WithLabelValues(method).Inc()
		return fmt.Errorf("%w: revision %d, expected %d", job.ErrTransitionConflict, currentRevision, expectedRevision)
	}
	return nil
}
