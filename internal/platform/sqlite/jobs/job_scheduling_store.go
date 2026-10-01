// Package jobs — job_scheduling_store.go: deferred scheduling + per-stage
// status persistence (SQLite jobs plane).
//
// Canonical owner (godlike/06 SSOT) of the three scheduling tables added by
// migration 005_job_scheduling.sql: job_schedules, job_scheduler_counters
// and job_stage_status. The port contracts live in
// internal/kernel/job/schedule.go (job.ScheduleStore + job.StageStatusStore);
// *SQLiteStore implements both, and *Broker re-maps the write error so
// capability code only ever branches on the kernel sentinel.
//
// The jobs hot row is deliberately NOT extended: a scheduled job is a
// normal jobs row in StatusScheduled, and the claim query (repository_claims.go)
// only ever selects QUEUED rows, so a SCHEDULED job cannot be leased before
// the scheduler promotes it.
package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Compile-time pins for the two scheduling ports.
//
// Both ports are DERIVED by type assertion rather than declared in the
// composition root (internal/capabilities/jobs/service.go::NewService and
// internal/app/wiring::buildJobSchedulerStep), so a drifted method set would
// not break the build — it would silently degrade to "scheduling unavailable":
// every scheduled_at enqueue would fail closed at runtime and every SCHEDULED
// row would sit unpromoted with nothing to promote it. Pinning both adapter
// shapes here turns that drift into a compile error at the storage boundary.
var (
	_ job.ScheduleStore       = (*SQLiteStore)(nil)
	_ job.JobStageStatusStore = (*SQLiteStore)(nil)
	_ job.ScheduleStore       = (*Broker)(nil)
	_ job.JobStageStatusStore = (*Broker)(nil)
)

// CreateScheduled inserts a job in StatusScheduled together with its
// job_schedules row in ONE transaction, then wakes the queue. Splitting the
// two writes would leave a scheduled job with no run_at on a crash, which no
// scheduler could ever promote.
func (r *SQLiteStore) CreateScheduled(ctx context.Context, j *job.Job, runAt time.Time) error {
	if j == nil {
		return fmt.Errorf("jobs.CreateScheduled: job is nil")
	}
	if runAt.IsZero() {
		return fmt.Errorf("jobs.CreateScheduled: run_at is required")
	}
	if j.Status != job.StatusScheduled {
		return fmt.Errorf("jobs.CreateScheduled: status must be %s, got %s", job.StatusScheduled, j.Status)
	}

	payloadJSON := string(j.Payload)
	if payloadJSON == "" || payloadJSON == "null" {
		payloadJSON = "{}"
	}
	revision := j.Revision
	if revision <= 0 {
		revision = 1
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("jobs.CreateScheduled: begin: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO jobs (id, type, status, priority, project, video_name, active_key,
			correlation_id, progress, error, retry_count, max_retries,
			worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at, revision, parent_job_id, root_job_id,
			client_id, idempotency_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		j.ID, j.Type, j.Status, j.Priority, j.Project, j.VideoName, j.ActiveKey,
		j.CorrelationID,
		j.Progress, j.Error,
		j.RetryCount, j.MaxRetries, j.WorkerID, j.LeaseID,
		timeutil.FormatPtrRFC3339(j.LeaseExpiry),
		timeutil.FormatRFC3339(j.CreatedAt), timeutil.FormatRFC3339(j.UpdatedAt),
		timeutil.FormatPtrRFC3339(j.StartedAt), timeutil.FormatPtrRFC3339(j.CompletedAt), nil, revision, j.ParentJobID, j.RootJobID,
		j.ClientID, j.IdempotencyKey)
	if err != nil {
		return mapWriteError(fmt.Errorf("jobs.CreateScheduled: %w", err))
	}
	if err := persistJobPayload(ctx, tx, j.ID, payloadJSON); err != nil {
		return fmt.Errorf("jobs.CreateScheduled: payload: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO job_schedules (job_id, run_at, created_at) VALUES (?, ?, ?)`,
		j.ID, timeutil.FormatRFC3339(runAt), timeutil.FormatRFC3339(j.CreatedAt)); err != nil {
		return fmt.Errorf("jobs.CreateScheduled: schedule: %w", err)
	}
	if err := insertJobTimelineEvent(ctx, tx, j.ID, "job_queued", "job scheduled", map[string]any{
		"status": string(j.Status), "scheduled_at": runAt.UTC().Format(time.RFC3339Nano),
	}, j.CreatedAt); err != nil {
		return fmt.Errorf("jobs.CreateScheduled: queued event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("jobs.CreateScheduled: commit: %w", err)
	}

	r.queueChanged()
	return nil
}

// ListDueSchedules returns up to limit schedules whose run_at is at or before
// now, oldest first. Ordering matches the promotion order so the daily quota
// is consumed by the longest-waiting job.
func (r *SQLiteStore) ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]job.Schedule, error) {
	if limit <= 0 {
		return []job.Schedule{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT job_id, run_at, created_at FROM job_schedules
		 WHERE run_at <= ? ORDER BY run_at ASC, job_id ASC LIMIT ?`,
		timeutil.FormatRFC3339(now), limit)
	if err != nil {
		return nil, fmt.Errorf("ListDueSchedules: query: %w", err)
	}
	defer rows.Close()

	out := make([]job.Schedule, 0)
	for rows.Next() {
		var (
			rec       job.Schedule
			runAt     string
			createdAt string
			jobIDRaw  string
		)
		if err := rows.Scan(&jobIDRaw, &runAt, &createdAt); err != nil {
			return nil, fmt.Errorf("ListDueSchedules: scan: %w", err)
		}
		rec.JobID = jobIDRaw
		rec.RunAt = timeutil.ParseRFC3339(runAt)
		rec.CreatedAt = timeutil.ParseRFC3339(createdAt)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListDueSchedules: rows: %w", err)
	}
	return out, nil
}

// ListPendingSchedules returns every pending schedule (due or not), earliest
// first, for the operator/API surface that lists what is waiting.
func (r *SQLiteStore) ListPendingSchedules(ctx context.Context, limit int) ([]job.Schedule, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT job_id, run_at, created_at FROM job_schedules
		 ORDER BY run_at ASC, job_id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("ListPendingSchedules: query: %w", err)
	}
	defer rows.Close()

	out := make([]job.Schedule, 0)
	for rows.Next() {
		var (
			rec       job.Schedule
			runAt     string
			createdAt string
		)
		if err := rows.Scan(&rec.JobID, &runAt, &createdAt); err != nil {
			return nil, fmt.Errorf("ListPendingSchedules: scan: %w", err)
		}
		rec.RunAt = timeutil.ParseRFC3339(runAt)
		rec.CreatedAt = timeutil.ParseRFC3339(createdAt)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListPendingSchedules: rows: %w", err)
	}
	return out, nil
}

// ListScheduledViews returns the pending schedules joined with their job's type
// and status, earliest first. It is the read model behind the operator status
// view: one query, no per-job fan-out, so a 5000/day backlog renders in a
// single round-trip.
func (r *SQLiteStore) ListScheduledViews(ctx context.Context, limit int) ([]job.ScheduledJobView, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT s.job_id, s.run_at, s.created_at, j.type, j.status
		 FROM job_schedules s JOIN jobs j ON j.id = s.job_id
		 ORDER BY s.run_at ASC, s.job_id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("ListScheduledViews: query: %w", err)
	}
	defer rows.Close()

	out := make([]job.ScheduledJobView, 0)
	for rows.Next() {
		var (
			view      job.ScheduledJobView
			runAt     string
			createdAt string
			status    string
		)
		if err := rows.Scan(&view.JobID, &runAt, &createdAt, &view.JobType, &status); err != nil {
			return nil, fmt.Errorf("ListScheduledViews: scan: %w", err)
		}
		view.RunAt = timeutil.ParseRFC3339(runAt)
		view.CreatedAt = timeutil.ParseRFC3339(createdAt)
		view.Status = job.Status(status)
		out = append(out, view)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListScheduledViews: rows: %w", err)
	}
	return out, nil
}

// NextScheduledAt returns the earliest pending run_at, or nil when no job is
// scheduled. It lets the scheduler sleep exactly until the next due time
// instead of polling.
func (r *SQLiteStore) NextScheduledAt(ctx context.Context) (*time.Time, error) {
	var raw sql.NullString
	if err := r.db.QueryRowContext(ctx, `SELECT MIN(run_at) FROM job_schedules`).Scan(&raw); err != nil {
		return nil, fmt.Errorf("NextScheduledAt: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	return timeutil.ParseRFC3339Ptr(raw.String), nil
}

// PromoteScheduled is the compare-and-swap that turns a due, admitted
// SCHEDULED job into claimable QUEUED work. It reports false when the row was
// cancelled or already promoted (the caller must not count that as a
// promotion). The schedule row is removed in the same transaction. A
// job_queued event is appended so the timeline records the scheduler wake.
func (r *SQLiteStore) PromoteScheduled(ctx context.Context, jobID string, now time.Time) (bool, error) {
	if jobID == "" {
		return false, fmt.Errorf("PromoteScheduled: job id is required")
	}
	nowStr := timeutil.FormatRFC3339(now)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("PromoteScheduled: begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'QUEUED', worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = 'SCHEDULED'`,
		nowStr, jobID)
	if err != nil {
		return false, fmt.Errorf("PromoteScheduled: update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Not SCHEDULED anymore (cancelled or already promoted). Drop the
		// schedule row so it cannot be reconsidered, and report the loss.
		if _, err := tx.ExecContext(ctx, `DELETE FROM job_schedules WHERE job_id = ?`, jobID); err != nil {
			return false, fmt.Errorf("PromoteScheduled: cleanup schedule: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("PromoteScheduled: commit cleanup: %w", err)
		}
		return false, nil
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM job_schedules WHERE job_id = ?`, jobID); err != nil {
		return false, fmt.Errorf("PromoteScheduled: delete schedule: %w", err)
	}
	if err := insertJobTimelineEvent(ctx, tx, jobID, "job_queued", "scheduled job promoted to queue", map[string]any{
		"status": string(job.StatusQueued), "scheduled_at": "due",
	}, now); err != nil {
		return false, fmt.Errorf("PromoteScheduled: insert event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("PromoteScheduled: commit: %w", err)
	}
	r.queueChanged()
	return true, nil
}

// CountActiveJobs counts the jobs currently occupying a slot
// (LEASED / RUNNING / FINALIZING). It is the concurrency admission input.
func (r *SQLiteStore) CountActiveJobs(ctx context.Context) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE status IN ('LEASED', 'RUNNING', 'FINALIZING')`).Scan(&n); err != nil {
		return 0, fmt.Errorf("CountActiveJobs: %w", err)
	}
	return n, nil
}

// ReserveDailyPromotion atomically consumes one unit of the UTC-day quota and
// reports whether a slot was granted. quota <= 0 means unbounded (always
// granted). Backpressure is therefore a property of the promotion step: the
// 5001st job simply stays SCHEDULED.
func (r *SQLiteStore) ReserveDailyPromotion(ctx context.Context, day string, quota int) (bool, error) {
	if day == "" {
		return false, fmt.Errorf("ReserveDailyPromotion: day is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("ReserveDailyPromotion: begin: %w", err)
	}
	defer tx.Rollback()

	var promoted int
	err = tx.QueryRowContext(ctx, `SELECT promoted FROM job_scheduler_counters WHERE day = ?`, day).Scan(&promoted)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if quota > 0 && promoted >= quota {
			// Unreachable (promoted == 0 here) but kept for symmetry.
			return false, nil
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO job_scheduler_counters (day, promoted, updated_at) VALUES (?, 1, ?)`,
			day, timeutil.FormatRFC3339(time.Now())); err != nil {
			return false, fmt.Errorf("ReserveDailyPromotion: insert: %w", err)
		}
	case err != nil:
		return false, fmt.Errorf("ReserveDailyPromotion: select: %w", err)
	default:
		if quota > 0 && promoted >= quota {
			return false, nil
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE job_scheduler_counters SET promoted = promoted + 1, updated_at = ? WHERE day = ?`,
			timeutil.FormatRFC3339(time.Now()), day); err != nil {
			return false, fmt.Errorf("ReserveDailyPromotion: update: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("ReserveDailyPromotion: commit: %w", err)
	}
	return true, nil
}

// ScheduledPromotions reports how many jobs were promoted on the given UTC day.
func (r *SQLiteStore) ScheduledPromotions(ctx context.Context, day string) (int, error) {
	var promoted int
	err := r.db.QueryRowContext(ctx, `SELECT promoted FROM job_scheduler_counters WHERE day = ?`, day).Scan(&promoted)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("ScheduledPromotions: %w", err)
	}
	return promoted, nil
}

// ── Per-stage status ────────────────────────────────────────────────

// UpsertJobStageStatus overwrites the (job_id, stage) projection in place.
func (r *SQLiteStore) UpsertJobStageStatus(ctx context.Context, s job.JobStageStatus) error {
	if err := s.Validate(); err != nil {
		return err
	}
	updatedAt := s.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO job_stage_status (job_id, stage, status, progress, detail, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(job_id, stage) DO UPDATE SET
		   status = excluded.status,
		   progress = excluded.progress,
		   detail = excluded.detail,
		   updated_at = excluded.updated_at`,
		s.JobID, string(s.Stage), string(s.Status), s.Progress, s.Detail, timeutil.FormatRFC3339(updatedAt)); err != nil {
		return fmt.Errorf("UpsertJobStageStatus: %w", err)
	}
	return nil
}

// ListJobStageStatuses returns every stage row for a job in canonical stage
// order (rows not yet reported are simply absent).
func (r *SQLiteStore) ListJobStageStatuses(ctx context.Context, jobID string) ([]job.JobStageStatus, error) {
	if jobID == "" {
		return []job.JobStageStatus{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT job_id, stage, status, progress, detail, updated_at
		 FROM job_stage_status WHERE job_id = ?`, jobID)
	if err != nil {
		return nil, fmt.Errorf("ListJobStageStatuses: %w", err)
	}
	defer rows.Close()

	byStage := make(map[string]job.JobStageStatus)
	for rows.Next() {
		var (
			rec       job.JobStageStatus
			stage     string
			status    string
			updatedAt string
		)
		if err := rows.Scan(&rec.JobID, &stage, &status, &rec.Progress, &rec.Detail, &updatedAt); err != nil {
			return nil, fmt.Errorf("ListJobStageStatuses: scan: %w", err)
		}
		rec.Stage = job.StageName(stage)
		rec.Status = job.StageStatus(status)
		rec.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
		byStage[stage] = rec
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListJobStageStatuses: rows: %w", err)
	}

	out := make([]job.JobStageStatus, 0, len(byStage))
	for _, stage := range job.CanonicalStageOrder() {
		if rec, ok := byStage[string(stage)]; ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

// Compile-time pins: the SQLite adapter satisfies both scheduling ports.
var (
	_ job.ScheduleStore       = (*SQLiteStore)(nil)
	_ job.JobStageStatusStore = (*SQLiteStore)(nil)
)
