// Package job — schedule.go (deferred scheduling + per-stage status contracts).
//
// Two concerns live here, both owned by the kernel because they are shared
// wire facts consumed by BOTH the scheduling capability and the SQLite
// persistence adapter:
//
//   - Deferred scheduling: a job may be enqueued with a future start time.
//     ScheduleStore is the narrow persistence port (adapter in
//     internal/platform/sqlite/jobs); the canonical admission policy
//     (daily quota + concurrency cap) lives in the scheduling capability.
//
//   - Per-stage status: a job reports progress for each pipeline stage.
//     The stage vocabulary (StageName) and its status values (StageStatus)
//     already exist in stage_progress.go — THIS file stores the durable
//     per-(job, stage) projection of that vocabulary, it does not redefine
//     it. Reusing the vocabulary is what keeps the workflow-progress
//     dimension single-owned (godlike/06: one owner per fact).
//
// Layer discipline (kernel): stdlib-only imports. Interfaces reference
// intra-package types (Job, Schedule, StageName, StageStatus) or stdlib
// (context, time).
package job

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ── Per-stage status projection ─────────────────────────────────────

// JobStageStatus is the durable per-(job, stage) status projection. Stage
// and Status reuse the canonical workflow vocabulary from stage_progress.go
// (StageName + StageStatus); Progress is a 0..100 completion estimate for
// the stage, and Detail is free-form operator context.
type JobStageStatus struct {
	JobID     string      `json:"job_id"`
	Stage     StageName   `json:"stage"`
	Status    StageStatus `json:"status"`
	Progress  int         `json:"progress"`
	Detail    string      `json:"detail,omitempty"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// Validate returns nil when the projection is internally consistent.
func (s JobStageStatus) Validate() error {
	if strings.TrimSpace(s.JobID) == "" {
		return fmt.Errorf("job stage status: job_id is required")
	}
	if !s.Stage.Valid() {
		return fmt.Errorf("job stage status: stage %q is not canonical", s.Stage)
	}
	if !s.Status.Valid() {
		return fmt.Errorf("job stage status: status %q is not a canonical stage status", s.Status)
	}
	if s.Progress < 0 || s.Progress > 100 {
		return fmt.Errorf("job stage status: progress %d out of range 0..100", s.Progress)
	}
	return nil
}

// JobStageStatusStore is the narrow persistence port for the per-stage
// status projection. UpsertJobStageStatus overwrites in place on the
// (job_id, stage) key so a re-report cannot accumulate duplicates.
type JobStageStatusStore interface {
	UpsertJobStageStatus(ctx context.Context, s JobStageStatus) error
	ListJobStageStatuses(ctx context.Context, jobID string) ([]JobStageStatus, error)
}

// ── Deferred scheduling ─────────────────────────────────────────────

// Schedule is the deferred start time bound to a job still in
// StatusScheduled. One row per scheduled job.
type Schedule struct {
	JobID     string    `json:"job_id"`
	RunAt     time.Time `json:"run_at"`
	CreatedAt time.Time `json:"created_at"`
}

// ScheduledJobView is the operator-facing projection of one pending schedule:
// the schedule itself plus the queued job's type and current status, resolved
// with ONE join so a status view over a 5000/day backlog never fans out into
// per-job reads.
//
// RunAt is the earliest time the scheduler may promote the job; Status is
// StatusScheduled until that promotion happens.
//
// Due is derived by the caller from RunAt (the store must not own "now"): the
// same view renders the backlog and the due set.
type ScheduledJobView struct {
	JobID     string    `json:"job_id"`
	RunAt     time.Time `json:"run_at"`
	CreatedAt time.Time `json:"created_at"`
	JobType   string    `json:"job_type"`
	Status    Status    `json:"status"`
}

// ScheduleStore is the persistence port for deferred scheduling.
//
// It is a separate port from Store because scheduling is a distinct
// concern with its own tables: the jobs row keeps the hot lifecycle
// columns untouched, and the scheduler decides WHEN a SCHEDULED row
// becomes claimable QUEUED work.
//
// Contract notes:
//   - CreateScheduled inserts the job (StatusScheduled) AND its
//     schedule row in ONE transaction, so a crash cannot leave a
//     scheduled job with no run_at (which would strand it forever).
//     It returns job.ErrDuplicate on a UNIQUE-constraint collision so
//     the enqueue idempotency rescue works unchanged.
//   - PromoteScheduled is a compare-and-swap: it moves the row
//     SCHEDULED → QUEUED only if it is still SCHEDULED, and reports
//     whether it won. A false result means the row was cancelled or
//     already promoted — the caller must not count it.
//   - ReserveDailyPromotion atomically consumes one unit of the
//     per-UTC-day quota and reports whether a slot was granted
//     (quota <= 0 means "unbounded").
//   - CountActiveJobs counts LEASED + RUNNING + FINALIZING rows and is
//     the concurrency admission input.
type ScheduleStore interface {
	CreateScheduled(ctx context.Context, j *Job, runAt time.Time) error
	ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]Schedule, error)
	ListPendingSchedules(ctx context.Context, limit int) ([]Schedule, error)
	ListScheduledViews(ctx context.Context, limit int) ([]ScheduledJobView, error)
	NextScheduledAt(ctx context.Context) (*time.Time, error)
	PromoteScheduled(ctx context.Context, jobID string, now time.Time) (bool, error)
	CountActiveJobs(ctx context.Context) (int, error)
	ReserveDailyPromotion(ctx context.Context, day string, quota int) (bool, error)
	ScheduledPromotions(ctx context.Context, day string) (int, error)
}
