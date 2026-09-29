package scheduling

import (
	"context"
	"strconv"
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// fakeScheduleStore is a hand-rolled job.ScheduleStore for the pure-ish
// scheduler tests: promotions and quota reservations are recorded, and the
// clock is injected through SchedulerDeps.Now.
type fakeScheduleStore struct {
	due          []job.Schedule
	active       int
	counter      int
	promoted     []string
	promoteFails map[string]bool
}

func (f *fakeScheduleStore) CreateScheduled(context.Context, *job.Job, time.Time) error { return nil }

func (f *fakeScheduleStore) ListDueSchedules(_ context.Context, _ time.Time, limit int) ([]job.Schedule, error) {
	if limit > len(f.due) {
		limit = len(f.due)
	}
	out := make([]job.Schedule, limit)
	copy(out, f.due[:limit])
	return out, nil
}

func (f *fakeScheduleStore) ListPendingSchedules(context.Context, int) ([]job.Schedule, error) {
	return f.due, nil
}

func (f *fakeScheduleStore) ListScheduledViews(context.Context, int) ([]job.ScheduledJobView, error) {
	return nil, nil
}

func (f *fakeScheduleStore) NextScheduledAt(context.Context) (*time.Time, error) { return nil, nil }

func (f *fakeScheduleStore) PromoteScheduled(_ context.Context, jobID string, _ time.Time) (bool, error) {
	if f.promoteFails[jobID] {
		return false, nil
	}
	f.promoted = append(f.promoted, jobID)
	// A promoted row leaves the due set, like the CAS UPDATE in the SQLite
	// adapter (status SCHEDULED -> QUEUED). Faking that here is what makes
	// the serial-lane test observe queue ADVANCE rather than a re-promotion
	// of the same row.
	for i, sched := range f.due {
		if sched.JobID == jobID {
			f.due = append(f.due[:i], f.due[i+1:]...)
			break
		}
	}
	return true, nil
}

func (f *fakeScheduleStore) CountActiveJobs(context.Context) (int, error) { return f.active, nil }

func (f *fakeScheduleStore) ReserveDailyPromotion(_ context.Context, _ string, quota int) (bool, error) {
	if quota > 0 && f.counter >= quota {
		return false, nil
	}
	f.counter++
	return true, nil
}

func (f *fakeScheduleStore) ScheduledPromotions(context.Context, string) (int, error) {
	return f.counter, nil
}

func dueSchedules(ids ...string) []job.Schedule {
	out := make([]job.Schedule, 0, len(ids))
	for _, id := range ids {
		out = append(out, job.Schedule{JobID: id, RunAt: time.Now().Add(-time.Minute)})
	}
	return out
}

func newTestScheduler(t *testing.T, store *fakeScheduleStore, policy AdmissionPolicy) *Scheduler {
	t.Helper()
	s, err := NewScheduler(SchedulerDeps{Store: store, Policy: policy, Now: func() time.Time { return time.Now() }})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return s
}

func TestSchedulerPromotesEveryDueJobWhenUnbounded(t *testing.T) {
	store := &fakeScheduleStore{due: dueSchedules("a", "b", "c")}
	s := newTestScheduler(t, store, AdmissionPolicy{})

	res, err := s.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Due != 3 || res.Promoted != 3 || res.Deferred != 0 || res.Skipped != 0 {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestSchedulerDefersWhenDailyQuotaSpent(t *testing.T) {
	store := &fakeScheduleStore{due: dueSchedules("a", "b", "c")}
	s := newTestScheduler(t, store, AdmissionPolicy{DailyQuota: 2})

	res, err := s.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Promoted != 2 || res.Deferred != 1 {
		t.Fatalf("unexpected result %+v", res)
	}
	if store.counter != 2 {
		t.Fatalf("quota reserved %d times, want 2", store.counter)
	}
	if len(store.promoted) != 2 {
		t.Fatalf("promoted %v, want 2 entries", store.promoted)
	}
}

func TestSchedulerDefersWhenConcurrencyLaneFull(t *testing.T) {
	store := &fakeScheduleStore{due: dueSchedules("a", "b", "c"), active: 1}
	s := newTestScheduler(t, store, AdmissionPolicy{MaxConcurrent: 2})

	res, err := s.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Promoted != 1 || res.Deferred != 2 {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestSchedulerSkipsLostCompareAndSwap(t *testing.T) {
	store := &fakeScheduleStore{
		due:          dueSchedules("a", "b"),
		promoteFails: map[string]bool{"a": true},
	}
	s := newTestScheduler(t, store, AdmissionPolicy{})

	res, err := s.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Promoted != 1 || res.Skipped != 1 {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestNewSchedulerRequiresStore(t *testing.T) {
	if _, err := NewScheduler(SchedulerDeps{}); err == nil {
		t.Fatal("NewScheduler without a store must fail")
	}
	if _, err := NewScheduler(SchedulerDeps{Store: &fakeScheduleStore{}, Policy: AdmissionPolicy{DailyQuota: -1}}); err == nil {
		t.Fatal("NewScheduler must reject an invalid policy")
	}
}

// TestSchedulerSerialLanePromotesOneJobBehindTheOther pins the operator's
// "5000 job/giorno uno dietro l'altro" requirement: with MaxConcurrent=1 the
// scheduler hands out exactly ONE job per free lane, so the queue drains in
// order instead of fanning out. The promoted job then occupies the lane
// (active=1), and the next tick must promote nothing until it finishes.
func TestSchedulerSerialLanePromotesOneJobBehindTheOther(t *testing.T) {
	store := &fakeScheduleStore{due: dueSchedules("j1", "j2", "j3")}
	s := newTestScheduler(t, store, AdmissionPolicy{MaxConcurrent: 1, DailyQuota: 5000})

	// Tick 1: the lane is free, exactly one job goes through.
	res, err := s.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if res.Promoted != 1 || res.Deferred != 2 {
		t.Fatalf("tick 1 = %+v, want 1 promoted and 2 deferred", res)
	}

	// Tick 2: the promoted job now occupies the single lane, so nothing else
	// may start — this is the "one behind the other" guarantee.
	store.active = 1
	res, err = s.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if res.Due != 2 || res.Promoted != 0 || res.Deferred != 2 {
		t.Fatalf("tick 2 = %+v, want 0 promoted and 2 deferred while the lane is busy", res)
	}

	// Tick 3: the lane frees up again and the queue advances by exactly one,
	// always the OLDEST due row (FIFO — "uno dietro l'altro", not "in
	// qualunque ordine").
	store.active = 0
	res, err = s.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	if res.Promoted != 1 || res.Deferred != 1 {
		t.Fatalf("tick 3 = %+v, want 1 promoted and 1 deferred", res)
	}
	if len(store.promoted) != 2 || store.promoted[0] != "j1" || store.promoted[1] != "j2" {
		t.Fatalf("promoted %v across the run, want exactly [j1 j2] (one per free lane, FIFO)", store.promoted)
	}
}

// TestSchedulerAdmitsAFull5000JobDay pins the daily-quota arithmetic at the
// requirement's own scale: a 5000-job backlog is admitted in full, and the
// 5001st job of the day is deferred rather than promoted. Backpressure is the
// point — an exhausted day must not silently overshoot.
func TestSchedulerAdmitsAFull5000JobDay(t *testing.T) {
	const quota = 5000
	ids := make([]string, 0, quota+1)
	for i := 0; i <= quota; i++ {
		ids = append(ids, "job-"+strconv.Itoa(i))
	}
	store := &fakeScheduleStore{due: dueSchedules(ids...)}
	s, err := NewScheduler(SchedulerDeps{
		Store:      store,
		Policy:     AdmissionPolicy{DailyQuota: quota},
		BatchLimit: quota + 1,
		Now:        func() time.Time { return time.Now() },
	})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	res, err := s.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Due != quota+1 {
		t.Fatalf("Due = %d, want %d", res.Due, quota+1)
	}
	if res.Promoted != quota {
		t.Fatalf("Promoted = %d, want the full daily quota %d", res.Promoted, quota)
	}
	if res.Deferred != 1 {
		t.Fatalf("Deferred = %d, want 1 (over-quota job rolls into the next day)", res.Deferred)
	}
	if store.counter != quota {
		t.Fatalf("daily counter = %d, want %d", store.counter, quota)
	}
}

func TestDayKeyIsUTC(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*3600)
	local := time.Date(2026, 9, 29, 1, 0, 0, 0, loc) // 2026-09-28T22:00Z
	if got := DayKey(local); got != "2026-09-28" {
		t.Fatalf("DayKey(%v) = %q, want 2026-09-28", local, got)
	}
}
