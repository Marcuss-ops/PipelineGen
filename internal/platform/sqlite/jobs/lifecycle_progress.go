package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// SetProgress updates progress percentage and emits an event when message is
// non-empty, preserving the legacy empty-message behavior.
func (r *SQLiteStore) SetProgress(ctx context.Context, jobID string, progress int, message string) error {
	return r.setProgressData(ctx, jobID, progress, message, nil, false)
}

// SetProgressData persists a progress update and its structured payload in
// the same transaction as its timeline event. Unlike legacy SetProgress, this
// explicit timeline API records an event even when message and data are empty.
func (r *SQLiteStore) SetProgressData(ctx context.Context, jobID string, progress int, message string, data map[string]any) error {
	return r.setProgressData(ctx, jobID, progress, message, data, true)
}

func (r *SQLiteStore) setProgressData(ctx context.Context, jobID string, progress int, message string, data map[string]any, alwaysRecordEvent bool) error {
	if !alwaysRecordEvent && message == "" {
		_, err := r.db.ExecContext(ctx, `UPDATE jobs SET progress = ?, updated_at = ? WHERE id = ?`, progress, timeutil.FormatRFC3339(time.Now().UTC()), jobID)
		if err != nil {
			return fmt.Errorf("setProgress: %w", err)
		}
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("setProgress: begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET progress = ?, updated_at = ? WHERE id = ?`, progress, timeutil.FormatRFC3339(now), jobID)
	if err != nil {
		return fmt.Errorf("setProgress: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return fmt.Errorf("setProgress: job %q not found", jobID)
	}
	if alwaysRecordEvent || message != "" {
		payload := data
		if payload == nil {
			payload = job.ProgressActivityData("", progress, message)
		}
		payload = cloneTimelineData(payload)
		payload["progress"] = progress
		if nested, ok := payload["payload"].(map[string]any); ok {
			nested = cloneTimelineData(nested)
			nested["progress"] = progress
			nested["message"] = message
			payload["payload"] = nested
		} else if _, enveloped := payload["kind"]; enveloped {
			payload["payload"] = map[string]any{"progress": progress, "message": message}
		}
		if err := insertJobTimelineEvent(ctx, tx, jobID, "progress", message, payload, now); err != nil {
			observability.WorkerEventDropsTotal.WithLabelValues("").Inc()
			return fmt.Errorf("setProgress: insert timeline event: %w", err)
		}
	}
	// The explicit SetProgressData API always writes its timeline event.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("setProgress: commit: %w", err)
	}
	return nil
}

// ── Convenience Wrappers ─────────────────────────────────────────────────

// MarkRunningJobsOlderThanFailed moves stale leased/running jobs to failed
// if their lease has expired beyond the given cutoff.
func (r *SQLiteStore) MarkRunningJobsOlderThanFailed(ctx context.Context, cutoff time.Time, reason string) (int, error) {
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)
	cutoffStr := timeutil.FormatRFC3339(cutoff)
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, revision FROM jobs WHERE status IN ('LEASED', 'RUNNING', 'FINALIZING') AND lease_expiry < ? ORDER BY lease_expiry, id`, cutoffStr)
	if err != nil {
		return 0, fmt.Errorf("markRunningJobsOlderThanFailed: select: %w", err)
	}
	type staleJob struct {
		id       string
		revision int
	}
	var stale []staleJob
	for rows.Next() {
		var item staleJob
		if err := rows.Scan(&item.id, &item.revision); err != nil {
			rows.Close()
			return 0, fmt.Errorf("markRunningJobsOlderThanFailed: scan: %w", err)
		}
		stale = append(stale, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("markRunningJobsOlderThanFailed: rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("markRunningJobsOlderThanFailed: close: %w", err)
	}
	if len(stale) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("markRunningJobsOlderThanFailed: begin tx: %w", err)
	}
	defer tx.Rollback()
	changed := 0
	for _, item := range stale {
		res, err := tx.ExecContext(ctx,
			`UPDATE jobs SET status = 'FAILED', completed_at = ?, error = ?,
			 worker_id = '', lease_id = '', lease_expiry = NULL,
			 revision = revision + 1, updated_at = ?
			 WHERE id = ? AND status IN ('LEASED', 'RUNNING', 'FINALIZING') AND lease_expiry < ? AND revision = ?`,
			nowStr, reason, nowStr, item.id, cutoffStr, item.revision)
		if err != nil {
			return 0, fmt.Errorf("markRunningJobsOlderThanFailed: update %s: %w", item.id, err)
		}
		if mustRowsAffected(res) == 0 {
			continue
		}
		if err := insertJobTimelineEvent(ctx, tx, item.id, "job_failed", reason, map[string]any{
			"status": string(job.StatusFailed), "error": reason, "recovery": "stale_lease",
		}, now); err != nil {
			return 0, fmt.Errorf("markRunningJobsOlderThanFailed: event %s: %w", item.id, err)
		}
		changed++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("markRunningJobsOlderThanFailed: commit: %w", err)
	}
	return changed, nil
}

// AddEvent records a human-readable event on the job timeline.
type jobEventQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func timelineActivityData(ctx context.Context, queryer jobEventQueryer, jobID, eventType, message string, data map[string]any) string {
	if data == nil {
		data = map[string]any{}
	}
	var jobType, correlationID string
	if err := queryer.QueryRowContext(ctx, `SELECT type, COALESCE(correlation_id, '') FROM jobs WHERE id = ?`, jobID).Scan(&jobType, &correlationID); err != nil {
		jobType = stringValue(data["kind"])
		correlationID = stringValue(data["correlation_id"])
	}
	if jobType == "" {
		jobType = "job"
	}
	subKind := canonicalMicroKind(eventType)
	for _, key := range []string{"micro_kind", "sub_kind", "stage", "phase"} {
		if value := stringValue(data[key]); value != "" {
			subKind = value
			break
		}
	}
	trace := job.ActivityTraceFromData(data, ctx)
	if trace.CorrelationID == "" {
		trace.CorrelationID = correlationID
	}
	normalized := job.ActivityDataWithTrace(jobType, subKind, job.ActivityStatus(eventType, data), message, data, trace)
	if stringValue(normalized["correlation_id"]) == "" && correlationID != "" {
		normalized["correlation_id"] = correlationID
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func cloneTimelineData(data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for key, value := range data {
		out[key] = value
	}
	return out
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func canonicalMicroKind(eventType string) string {
	switch eventType {
	case "queued", "job_queued":
		return "job.queue"
	case "leased":
		return "worker.claim"
	case "job_running":
		return "worker.execute"
	case "job_completed":
		return "job.complete"
	case "job_failed":
		return "job.fail"
	case "job_retry_wait":
		return "job.retry.wait"
	case "job_cancelled":
		return "job.cancel"
	case "job_deferred":
		return "job.defer"
	case "job.aggregate_completed":
		return "job.aggregate.complete"
	case "job.aggregate_failed":
		return "job.aggregate.fail"
	default:
		return eventType
	}
}

func (r *SQLiteStore) AddEvent(ctx context.Context, id string, eventType, message string, data map[string]any) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("addEvent: begin tx: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if err := insertJobTimelineEvent(ctx, tx, id, eventType, message, data, now); err != nil {
		return fmt.Errorf("addEvent: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("addEvent: commit: %w", err)
	}
	return nil
}

func insertJobTimelineEvent(ctx context.Context, tx *sql.Tx, jobID, eventType, message string, data map[string]any, createdAt time.Time) error {
	if trace, ok := data["trace"].(map[string]any); ok {
		data = cloneTimelineData(data)
		data["run_id"] = trace["run_id"]
		data["attempt_id"] = trace["attempt_id"]
		data["parent_run_id"] = trace["parent_run_id"]
		data["correlation_id"] = trace["correlation_id"]
		data["sequence"] = trace["sequence"]
	}
	evtID := fmt.Sprintf("evt_%d_%s", createdAt.UnixNano(), hashutil.RandomString(6))
	dataJSON := timelineActivityData(ctx, tx, jobID, eventType, message, data)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, jobID, eventType, message, dataJSON, timeutil.FormatRFC3339(createdAt))
	if err != nil {
		return fmt.Errorf("insert job timeline event: %w", err)
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
