package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// schedulingTestSchema mirrors migration 005_job_scheduling.sql. It is kept
// next to the integration test so the two must be changed together.
const schedulingTestSchema = `
CREATE TABLE job_schedules (
    job_id TEXT PRIMARY KEY,
    run_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_job_schedules_due ON job_schedules(run_at, job_id);
CREATE TABLE job_scheduler_counters (
    day TEXT PRIMARY KEY,
    promoted INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);
CREATE TABLE job_stage_status (
    job_id TEXT NOT NULL,
    stage TEXT NOT NULL,
    status TEXT NOT NULL,
    progress INTEGER NOT NULL DEFAULT 0,
    detail TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (job_id, stage)
);
`

func newSchedulingTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(context.Background(), jobsTestSchema+schedulingTestSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return NewSQLiteStore(db, zap.NewNop())
}

func newScheduledJob(id string, runAt time.Time) *job.Job {
	now := time.Now().UTC()
	return &job.Job{
		ID:         id,
		Type:       "video.create",
		Status:     job.StatusScheduled,
		Payload:    json.RawMessage(`{"topic":"x"}`),
		MaxRetries: 2,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func TestCreateScheduledPersistsJobAndSchedule(t *testing.T) {
	store := newSchedulingTestStore(t)
	ctx := context.Background()
	runAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)

	if err := store.CreateScheduled(ctx, newScheduledJob("job-1", runAt), runAt); err != nil {
		t.Fatalf("CreateScheduled: %v", err)
	}

	got, err := store.Get(ctx, "job-1")
	if err != nil || got == nil {
		t.Fatalf("Get: job=%v err=%v", got, err)
	}
	if got.Status != job.StatusScheduled {
		t.Fatalf("status = %s, want SCHEDULED", got.Status)
	}

	pending, err := store.ListPendingSchedules(ctx, 10)
	if err != nil {
		t.Fatalf("ListPendingSchedules: %v", err)
	}
	if len(pending) != 1 || pending[0].JobID != "job-1" {
		t.Fatalf("pending = %+v, want one job-1", pending)
	}
	if !pending[0].RunAt.Equal(runAt) {
		t.Fatalf("run_at = %v, want %v", pending[0].RunAt, runAt)
	}

	// Not due yet.
	due, err := store.ListDueSchedules(ctx, runAt.Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("ListDueSchedules(before): %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("due before run_at = %+v, want none", due)
	}

	// Due now.
	due, err = store.ListDueSchedules(ctx, runAt, 10)
	if err != nil {
		t.Fatalf("ListDueSchedules(at): %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("due at run_at = %+v, want one", due)
	}
}

func TestListScheduledViewsJoinsJobTypeAndStatus(t *testing.T) {
	store := newSchedulingTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	for i, runAt := range []time.Time{now.Add(time.Hour), now.Add(2 * time.Hour)} {
		j := newScheduledJob("job-view-"+string(rune('a'+i)), runAt)
		j.Type = "video.create"
		if err := store.CreateScheduled(ctx, j, runAt); err != nil {
			t.Fatalf("CreateScheduled #%d: %v", i, err)
		}
	}

	views, err := store.ListScheduledViews(ctx, 10)
	if err != nil {
		t.Fatalf("ListScheduledViews: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("views = %+v, want 2", views)
	}
	// Ordered by run_at ASC.
	if views[0].JobID != "job-view-a" || views[1].JobID != "job-view-b" {
		t.Fatalf("view order = %s, %s; want a then b", views[0].JobID, views[1].JobID)
	}
	for _, v := range views {
		if v.JobType != "video.create" || v.Status != job.StatusScheduled {
			t.Fatalf("view = %+v, want type=video.create status=SCHEDULED", v)
		}
	}
}

func TestCreateScheduledRejectsNonScheduledStatus(t *testing.T) {
	store := newSchedulingTestStore(t)
	j := newScheduledJob("job-2", time.Now().Add(time.Hour))
	j.Status = job.StatusQueued
	if err := store.CreateScheduled(context.Background(), j, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("CreateScheduled must reject a non-SCHEDULED job")
	}
}

func TestPromoteScheduledIsCompareAndSwap(t *testing.T) {
	store := newSchedulingTestStore(t)
	ctx := context.Background()
	runAt := time.Now().UTC().Add(-time.Minute)
	if err := store.CreateScheduled(ctx, newScheduledJob("job-3", runAt), runAt); err != nil {
		t.Fatalf("CreateScheduled: %v", err)
	}

	promoted, err := store.PromoteScheduled(ctx, "job-3", time.Now())
	if err != nil || !promoted {
		t.Fatalf("PromoteScheduled = %v, %v; want true, nil", promoted, err)
	}
	got, _ := store.Get(ctx, "job-3")
	if got.Status != job.StatusQueued {
		t.Fatalf("status = %s, want QUEUED", got.Status)
	}
	pending, _ := store.ListPendingSchedules(ctx, 10)
	if len(pending) != 0 {
		t.Fatalf("schedule row survived promotion: %+v", pending)
	}

	// Second promotion loses the CAS.
	promoted, err = store.PromoteScheduled(ctx, "job-3", time.Now())
	if err != nil || promoted {
		t.Fatalf("second PromoteScheduled = %v, %v; want false, nil", promoted, err)
	}

	events, err := store.ListEvents(ctx, "job-3")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Type == "job_queued" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no job_queued event in %+v", events)
	}
}

func TestPromoteScheduledDropsCancelledSchedule(t *testing.T) {
	store := newSchedulingTestStore(t)
	ctx := context.Background()
	runAt := time.Now().UTC().Add(-time.Minute)
	if err := store.CreateScheduled(ctx, newScheduledJob("job-4", runAt), runAt); err != nil {
		t.Fatalf("CreateScheduled: %v", err)
	}
	if err := store.Cancel(ctx, "job-4"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	promoted, err := store.PromoteScheduled(ctx, "job-4", time.Now())
	if err != nil || promoted {
		t.Fatalf("PromoteScheduled on cancelled = %v, %v; want false, nil", promoted, err)
	}
	pending, _ := store.ListPendingSchedules(ctx, 10)
	if len(pending) != 0 {
		t.Fatalf("cancelled schedule row not dropped: %+v", pending)
	}
}

func TestReserveDailyPromotionEnforcesQuota(t *testing.T) {
	store := newSchedulingTestStore(t)
	ctx := context.Background()
	day := "2026-09-28"

	for i := 0; i < 2; i++ {
		ok, err := store.ReserveDailyPromotion(ctx, day, 2)
		if err != nil || !ok {
			t.Fatalf("reserve #%d = %v, %v; want true", i, ok, err)
		}
	}
	ok, err := store.ReserveDailyPromotion(ctx, day, 2)
	if err != nil || ok {
		t.Fatalf("reserve over quota = %v, %v; want false, nil", ok, err)
	}
	count, err := store.ScheduledPromotions(ctx, day)
	if err != nil || count != 2 {
		t.Fatalf("ScheduledPromotions = %d, %v; want 2", count, err)
	}
	// A different day has its own budget.
	if ok, _ := store.ReserveDailyPromotion(ctx, "2026-09-29", 2); !ok {
		t.Fatal("next day must start with a fresh quota")
	}
}

func TestCountActiveJobsCountsLeasedRunningFinalizing(t *testing.T) {
	store := newSchedulingTestStore(t)
	ctx := context.Background()
	for i, status := range []job.Status{job.StatusLeased, job.StatusRunning, job.StatusFinalizing, job.StatusQueued, job.StatusSucceeded} {
		id := "job-active-" + string(rune('a'+i))
		j := newScheduledJob(id, time.Now())
		j.Status = status
		if err := store.Create(ctx, j); err != nil {
			t.Fatalf("Create(%s): %v", status, err)
		}
	}
	n, err := store.CountActiveJobs(ctx)
	if err != nil || n != 3 {
		t.Fatalf("CountActiveJobs = %d, %v; want 3", n, err)
	}
}

func TestJobStageStatusUpsertKeepsOneRowPerStageAndOrdersCanonically(t *testing.T) {
	store := newSchedulingTestStore(t)
	ctx := context.Background()
	jobID := "job-stages"

	// Insert in reverse canonical order to prove ListJobStageStatuses orders.
	if err := store.UpsertJobStageStatus(ctx, job.JobStageStatus{
		JobID: jobID, Stage: job.StageOverlay, Status: job.StageRunning, Progress: 10,
	}); err != nil {
		t.Fatalf("upsert overlay: %v", err)
	}
	if err := store.UpsertJobStageStatus(ctx, job.JobStageStatus{
		JobID: jobID, Stage: job.StageScript, Status: job.StageCompleted, Progress: 100,
	}); err != nil {
		t.Fatalf("upsert script: %v", err)
	}
	// Re-report the overlay stage: upsert, not a duplicate row.
	if err := store.UpsertJobStageStatus(ctx, job.JobStageStatus{
		JobID: jobID, Stage: job.StageOverlay, Status: job.StageCompleted, Progress: 100,
	}); err != nil {
		t.Fatalf("re-upsert overlay: %v", err)
	}

	stages, err := store.ListJobStageStatuses(ctx, jobID)
	if err != nil {
		t.Fatalf("ListJobStageStatuses: %v", err)
	}
	if len(stages) != 2 {
		t.Fatalf("stages = %+v, want 2 (one per stage)", stages)
	}
	if stages[0].Stage != job.StageScript || stages[1].Stage != job.StageOverlay {
		t.Fatalf("stage order = %v, %v; want script then overlay", stages[0].Stage, stages[1].Stage)
	}
	if stages[1].Status != job.StageCompleted || stages[1].Progress != 100 {
		t.Fatalf("overlay stage = %+v, want completed/100 after upsert", stages[1])
	}
}

func TestUpsertJobStageStatusRejectsUnknownStage(t *testing.T) {
	store := newSchedulingTestStore(t)
	err := store.UpsertJobStageStatus(context.Background(), job.JobStageStatus{
		JobID: "job-x", Stage: job.StageName("not_a_stage"), Status: job.StageRunning,
	})
	if err == nil {
		t.Fatal("unknown stage must be rejected")
	}
}
