package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/metrics"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// newWorkerService wires a service over the in-memory worker registry with a
// controllable clock, anchored on the moment the workers registered so the
// test does not depend on the wall clock.
//
// It returns two clock controls. `advance` moves the classification clock
// relative to the registration instant, which is how a frozen worker is
// reproduced. `resync` returns the clock to the wall clock, which is required
// before calling WorkerHeartbeat: the heartbeat is stamped by the STORE with the
// real clock (a repository cannot read the service's test clock), so a test that
// heartbeats while the classification clock sits three hours in the future
// would be measuring its own offset, not a live worker.
func newWorkerService(t *testing.T) (svc *Service, m *metrics.Metrics, advance func(time.Duration), resync func()) {
	t.Helper()
	mem := memory.New(30*time.Second, 3)
	svc = New(mem)
	svc.SetWorkerRepository(mem, 90*time.Second)
	m = metrics.New()
	svc.SetMetrics(m)

	if err := svc.RegisterWorker(context.Background(), model.Worker{ID: "w1", Status: model.WorkerStatusReady}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RegisterWorker(context.Background(), model.Worker{ID: "w2", Status: model.WorkerStatusBusy}); err != nil {
		t.Fatal(err)
	}
	workers, err := svc.ListWorkers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	base := workers[0].LastHeartbeatAt
	now := base
	svc.SetLivenessClock(func() time.Time { return now })
	return svc, m,
		func(offset time.Duration) { now = base.Add(offset) },
		func() { now = time.Now() }
}

func TestServiceWorkerHealthMetrics(t *testing.T) {
	svc, m, advance, _ := newWorkerService(t)

	if got := testutil.ToFloat64(m.WorkersReady); got != 1 {
		t.Fatalf("workers_ready: want 1, got %v", got)
	}
	if got := testutil.ToFloat64(m.WorkersOffline); got != 0 {
		t.Fatalf("workers_offline: want 0, got %v", got)
	}

	if err := svc.WorkerHeartbeat(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(m.WorkersReady); got != 1 {
		t.Fatalf("workers_ready after heartbeat: want 1, got %v", got)
	}

	// Age every heartbeat past one full beat: the ready gauge must decay even
	// though every worker still REPORTS ready/busy.
	advance(40 * time.Second)
	svc.RefreshWorkerHealth(context.Background())
	if got := testutil.ToFloat64(m.WorkersReady); got != 0 {
		t.Fatalf("workers_ready after ageing: want 0, got %v", got)
	}
	if got := testutil.ToFloat64(m.WorkersDegraded); got != 2 {
		t.Fatalf("workers_degraded after ageing: want 2, got %v", got)
	}

	advance(10 * time.Minute)
	svc.RefreshWorkerHealth(context.Background())
	if got := testutil.ToFloat64(m.WorkersOffline); got != 2 {
		t.Fatalf("workers_offline when frozen: want 2, got %v", got)
	}
	if got := testutil.ToFloat64(m.WorkersStale); got != 0 {
		t.Fatalf("workers_stale when frozen: want 0, got %v", got)
	}
}

// TestFrozenWorkerIsNotReportedReady is the acceptance criterion for the defect
// found on the live host: a worker whose threads are all stopped (heartbeat
// frozen for hours) kept being served as `status: ready` by GET /workers and
// counted by GET /workers/health, so a benchmark or a certification preflight
// trusted a worker that could not claim a single job.
func TestFrozenWorkerIsNotReportedReady(t *testing.T) {
	svc, _, advance, _ := newWorkerService(t)

	// w1 says "ready" and then stops: its status is never updated again.
	workers, err := svc.ListWorkers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if workers[0].Liveness != model.WorkerLivenessReady {
		t.Fatalf("a freshly registered worker is %s, want %s", workers[0].Liveness, model.WorkerLivenessReady)
	}

	advance(3 * time.Hour)
	workers, err = svc.ListWorkers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if workers[0].Status != model.WorkerStatusReady {
		t.Fatalf("the reported status should still be ready, got %s", workers[0].Status)
	}
	if workers[0].Liveness != model.WorkerLivenessDead {
		t.Fatalf("frozen worker liveness = %s, want %s", workers[0].Liveness, model.WorkerLivenessDead)
	}

	health, err := svc.WorkerHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.Ready != 0 || health.Busy != 0 {
		t.Fatalf("health reports %d ready / %d busy for a frozen fleet, want 0/0", health.Ready, health.Busy)
	}
	if health.Offline != 2 || health.Total != 2 {
		t.Fatalf("health = %+v, want offline 2 of 2", health)
	}

	ready, err := svc.ReadyWorkers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 {
		t.Fatalf("ReadyWorkers = %v, want none: a frozen worker must not pass a preflight", ready)
	}
}

// TestFrozenWorkerRecoversOnHeartbeat pins that the classification is a live
// function of the heartbeat ledger, not a latch: a worker that beats again is
// ready again, and it is the fresh heartbeat — not the clock — that says so.
//
// (The heartbeat is stamped by the STORE with the wall clock, so the frozen
// clock is returned to the wall clock first; with both clocks agreeing, the only
// difference between the two reads below is the heartbeat itself.)
func TestFrozenWorkerRecoversOnHeartbeat(t *testing.T) {
	svc, _, advance, resync := newWorkerService(t)

	advance(3 * time.Hour)
	ready, err := svc.ReadyWorkers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 {
		t.Fatalf("precondition failed: %v should be frozen", ready)
	}

	resync()
	if err := svc.WorkerHeartbeat(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	ready, err = svc.ReadyWorkers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ready, "w1") {
		t.Fatalf("ReadyWorkers after a heartbeat = %v, want w1 to be back", ready)
	}
}

// TestWorkerLivenessBandsAreVisibleBeforeDeath pins the reason the two new
// buckets exist: a fleet that is losing its workers must be visible while it
// still has some, not only once it has none.
func TestWorkerLivenessBandsAreVisibleBeforeDeath(t *testing.T) {
	svc, _, advance, _ := newWorkerService(t)

	advance(70 * time.Second) // past two thirds of the 90s window
	health, err := svc.WorkerHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.Stale != 2 || health.Offline != 0 {
		t.Fatalf("health = %+v, want 2 stale and 0 offline", health)
	}
	workers, err := svc.ListWorkers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range workers {
		if w.Liveness != model.WorkerLivenessStale {
			t.Fatalf("worker %s liveness = %s, want %s", w.ID, w.Liveness, model.WorkerLivenessStale)
		}
	}
}

// TestWorkerSurfaceWithoutRepository fails closed instead of reporting an empty
// (i.e. healthy-looking) fleet when the worker registry is not wired.
func TestWorkerSurfaceWithoutRepository(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if _, err := svc.ListWorkers(context.Background()); err == nil {
		t.Fatal("ListWorkers without a worker repository should fail")
	}
	if _, err := svc.WorkerHealth(context.Background()); err == nil {
		t.Fatal("WorkerHealth without a worker repository should fail")
	}
	if _, err := svc.ReadyWorkers(context.Background()); err == nil {
		t.Fatal("ReadyWorkers without a worker repository should fail")
	}
}
