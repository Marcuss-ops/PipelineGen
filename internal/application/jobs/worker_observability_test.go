// Package jobs — worker_observability_test.go (FASE 2, August 2026).
//
// Pins the kernel-observability instrumentation contract of the
// in-process job runtime:
//
//   - TestWorker_RunJob_ProducesRunReport        — runJob with an
//     attached RunObserver produces one Run per claim: queue_wait_ms
//     from created_at→started_at, wall_time_ms > 0, SUCCEEDED status,
//     AttemptID is a distinct persistent execution ID, while the lease remains the worker fence; Counters.Retries = job.RetryCount.
//   - TestWorker_RunJob_FailedAttempt_FinishesFailed — a dispatcher
//     error closes the run as FAILED (a scheduled retry is still a
//     failed attempt).
//   - TestRunner_AttachesObserverToWorkers       — Runner.WithObserver
//     propagates the observer onto every Worker built by buildWorkers.
//   - TestEnqueue_RegistersChildOnParentRun       — Enqueue inside a
//     parent run's ctx registers the child on the parent's children
//     summary; idempotent dedup returns do NOT double-count; enqueue
//     errors register failed children.
//
// Mirrors the existing registry_wiring_test.go / worker_broker_wiring_test.go
// fixture patterns (package `jobs` internal test).
package jobs

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// obsCaptureRecorder is a thread-safe in-memory kernobs.Recorder used
// to observe the reports produced by runJob.
type obsCaptureRecorder struct {
	mu      sync.Mutex
	reports []*kernobs.RunReport
}

func (c *obsCaptureRecorder) SaveReport(_ context.Context, rep *kernobs.RunReport) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = append(c.reports, rep)
	return nil
}

func (c *obsCaptureRecorder) last() *kernobs.RunReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reports) == 0 {
		return nil
	}
	return c.reports[len(c.reports)-1]
}

func (c *obsCaptureRecorder) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reports)
}

// newObservedWorker builds a Worker on a mockCancelBroker (the shared
// FASE 4(b) stub) with an attached RunObserver + a registered handler.
func newObservedWorker(t *testing.T, rec *obsCaptureRecorder, handler Handler) (*Worker, *mockCancelBroker, *kernobs.RunObserver) {
	t.Helper()
	broker := newMockCancelBroker()
	broker.jobStatus = job.StatusRunning
	broker.revision = 1

	dispatcher := NewDispatcher()
	if err := dispatcher.Register(TypeScriptGenerate, handler); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	observer := kernobs.NewRunObserver(rec)
	worker := NewWorker(WorkerDeps{
		ID:         "observability-test-worker",
		Repo:       broker,
		Dispatcher: dispatcher,
		Notifier:   nil,
		Log:        zap.NewNop(),
		LeaseTTL:   5 * time.Minute, // renew tick = 100s — never fires during the test
		PollEvery:  2 * time.Second,
		Backoff:    BackoffConfig{},
		Types:      []string{TypeScriptGenerate},
	}).WithObserver(observer)
	return worker, broker, observer
}

// TestWorker_RunJob_ProducesRunReport — the FASE 2 core contract: a
// claimed job produces exactly one Run with queue_wait_ms, wall_time_ms,
// status, attempts. The runtime's canonical per-attempt token (the
// lease) lands in AttemptID; job.RetryCount lands in Counters.Retries.
func TestWorker_RunJob_ProducesRunReport(t *testing.T) {
	rec := &obsCaptureRecorder{}
	handler := HandlerFunc(func(_ context.Context, _ *job.Job, _ *JobTools) (map[string]any, error) {
		// Small sleep so wall_time_ms truncates to a non-zero value
		// (the run finishes in sub-ms otherwise).
		time.Sleep(5 * time.Millisecond)
		return map[string]any{"ok": true}, nil
	})
	worker, _, _ := newObservedWorker(t, rec, handler)

	createdAt := time.Now().Add(-10 * time.Second)
	startedAt := time.Now().Add(-5 * time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.runJob(context.Background(), &job.Job{
			ID:         "obs-job-1",
			Type:       TypeScriptGenerate,
			Status:     job.StatusRunning,
			LeaseID:    "lease-obs-1",
			RetryCount: 2,
			MaxRetries: 5,
			CreatedAt:  createdAt,
			StartedAt:  &startedAt,
		})
	}()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("runJob did not complete within 8s")
	}

	if rec.count() != 1 {
		t.Fatalf("persisted reports = %d, want 1", rec.count())
	}
	rep := rec.last()
	if rep.JobID != "obs-job-1" || rep.JobType != TypeScriptGenerate {
		t.Fatalf("identity = %q/%q", rep.JobID, rep.JobType)
	}
	if rep.Status != kernobs.StatusSucceeded {
		t.Fatalf("status = %q, want SUCCEEDED", rep.Status)
	}
	if rep.AttemptID == "" || rep.AttemptID == "lease-obs-1" {
		t.Fatalf("attempt_id = %q, want a distinct persistent execution ID", rep.AttemptID)
	}
	if rep.QueueWaitMs != 5000 {
		t.Fatalf("queue_wait_ms = %d, want 5000 (started_at − created_at)", rep.QueueWaitMs)
	}
	if rep.WallTimeMs < 0 {
		t.Fatalf("wall_time_ms = %d, want >= 0", rep.WallTimeMs)
	}
	if rep.Counters.Retries != 2 {
		t.Fatalf("counters.retries = %d, want 2 (job.RetryCount snapshot)", rep.Counters.Retries)
	}
	if rep.RunID == "" {
		t.Fatal("run_id must be populated")
	}
}

// TestWorker_RunJob_FailedAttempt_FinishesFailed — a dispatcher error
// must close the attempt as FAILED with the typed error. A scheduled
// retry (RetryCount < MaxRetries) is still a failed attempt from the
// observability perspective.
func TestWorker_RunJob_FailedAttempt_FinishesFailed(t *testing.T) {
	rec := &obsCaptureRecorder{}
	handler := HandlerFunc(func(_ context.Context, _ *job.Job, _ *JobTools) (map[string]any, error) {
		return nil, context.DeadlineExceeded
	})
	worker, _, _ := newObservedWorker(t, rec, handler)

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.runJob(context.Background(), &job.Job{
			ID:         "obs-job-fail",
			Type:       TypeScriptGenerate,
			Status:     job.StatusRunning,
			LeaseID:    "lease-obs-fail",
			RetryCount: 1,
			MaxRetries: 5,
			CreatedAt:  time.Now(),
		})
	}()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("runJob did not complete within 8s")
	}

	rep := rec.last()
	if rep == nil {
		t.Fatal("no run report persisted")
	}
	if rep.Status != kernobs.StatusFailed {
		t.Fatalf("status = %q, want FAILED for a failed attempt", rep.Status)
	}
	if rep.Error == "" {
		t.Fatal("failed run must carry the error message")
	}
}

// TestWorker_WithObserver_NilIsTolerant — a nil observer keeps the
// legacy un-instrumented runJob path (test fixtures without an
// observer must keep working). Mirrors the WithRegistry/WithBroker
// nil-tolerance pins.
func TestWorker_WithObserver_NilIsTolerant(t *testing.T) {
	w := NewWorker(WorkerDeps{
		ID:        "nil-observer-worker",
		Log:       zap.NewNop(),
		LeaseTTL:  time.Minute,
		PollEvery: time.Second,
		Backoff:   BackoffConfig{},
	})
	if w.observer != nil {
		t.Fatalf("NewWorker must leave observer nil by default, got %v", w.observer)
	}
	if w.WithObserver(nil) != w {
		t.Fatal("WithObserver must return the receiver for chaining")
	}
	if w.observer != nil {
		t.Fatalf("WithObserver(nil) must leave observer nil, got %v", w.observer)
	}
}

// TestRunner_AttachesObserverToWorkers — Runner.WithObserver propagates
// the observer onto every Worker built by buildWorkers (mirrors the
// TestRunner_AttachesRegistryToWorkers pin).
func TestRunner_AttachesObserverToWorkers(t *testing.T) {
	observer := kernobs.NewRunObserver(nil)
	const poolSize = 2
	runner := NewRunner(
		nil,
		nil,
		zap.NewNop(),
		RunnerConfig{
			Workers:   poolSize,
			PollEvery: 2 * time.Second,
			LeaseTTL:  5 * time.Minute,
			Backoff:   BackoffConfig{},
		},
	).WithObserver(observer)

	workers := runner.buildWorkers()
	if len(workers) != poolSize {
		t.Fatalf("buildWorkers: got %d workers, want %d", len(workers), poolSize)
	}
	for i, w := range workers {
		if w.observer != observer {
			t.Errorf("worker[%d] observer: not the RunObserver attached to Runner", i)
		}
	}
}

// TestEnqueue_RegistersChildOnParentRun — the canonical child-creation
// hook: an Enqueue issued with a parent run bound in ctx registers the
// child on the parent's children summary. A second idempotent enqueue
// (same correlation) returns the existing row WITHOUT double-counting.
func TestEnqueue_RegistersChildOnParentRun(t *testing.T) {
	reg := newWiringRegistry(t, time.Minute, 3)
	store, cleanup := newSqliteStoreForTest(t)
	defer cleanup()
	store.SetProducesArtifacts(reg.ProducesArtifactsMap())

	svc, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Bind a parent run to the ctx (as a job handler would see it).
	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{
		JobID:   "parent-1",
		JobType: TypeScriptGenerate,
	})
	ctx := kernobs.WithRun(context.Background(), run)

	req := &job.EnqueueRequest{
		Type:          wiringTestType,
		CorrelationID: "obs-child-1",
		Payload:       map[string]any{"item": 1},
	}
	first, err := svc.Enqueue(ctx, req)
	if err != nil {
		t.Fatalf("Enqueue #1: %v", err)
	}

	// Second Enqueue with the same (type, correlation) — dedup returns
	// the existing row, must NOT re-register a child.
	req2 := *req
	req2.Payload = map[string]any{"item": 2}
	second, err := svc.Enqueue(ctx, &req2)
	if err != nil {
		t.Fatalf("Enqueue #2: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("dedup returned a different job: %q vs %q", second.ID, first.ID)
	}

	rep := run.Finish()
	if rep.Children == nil {
		t.Fatal("children summary must be populated")
	}
	if rep.Children.Requested != 1 {
		t.Fatalf("children.requested = %d, want 1 (dedup must not double-count)", rep.Children.Requested)
	}
	if rep.Children.Failed != 0 || rep.Children.Completed != 0 {
		t.Fatalf("children failed/completed = %d/%d, want 0/0", rep.Children.Failed, rep.Children.Completed)
	}
}

// TestEnqueue_EnqueueErrorRegistersFailedChild — an Enqueue that fails
// inside a parent run's ctx registers a FAILED child (the "children:
// {requested: N, failed: M}" accounting of the global contract).
func TestEnqueue_EnqueueErrorRegistersFailedChild(t *testing.T) {
	reg := newWiringRegistry(t, time.Minute, 3)
	store, cleanup := newSqliteStoreForTest(t)
	defer cleanup()
	store.SetProducesArtifacts(reg.ProducesArtifactsMap())

	svc, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: "parent-2", JobType: TypeScriptGenerate})
	ctx := kernobs.WithRun(context.Background(), run)

	// Unregistered job type → strict typed lookup returns
	// ErrMaxRetriesUnknown (PR-jobs-retry-contract fail-closed).
	_, err = svc.Enqueue(ctx, &job.EnqueueRequest{Type: "totally.unknown.child.type", Payload: map[string]any{}})
	if err == nil {
		t.Fatal("Enqueue of an unregistered type must fail")
	}

	rep := run.Finish()
	if rep.Children == nil {
		t.Fatal("children summary must be populated")
	}
	if rep.Children.Requested != 1 || rep.Children.Failed != 1 {
		t.Fatalf("children requested/failed = %d/%d, want 1/1", rep.Children.Requested, rep.Children.Failed)
	}
}

// TestEnqueue_NoRunBound_NoChildRegistered — an Enqueue issued without a
// parent run in ctx (API-triggered) must not touch any children summary.
func TestEnqueue_NoRunBound_NoChildRegistered(t *testing.T) {
	reg := newWiringRegistry(t, time.Minute, 3)
	store, cleanup := newSqliteStoreForTest(t)
	defer cleanup()
	store.SetProducesArtifacts(reg.ProducesArtifactsMap())

	svc, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	got, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{
		Type:    wiringTestType,
		Payload: map[string]any{"item": 1},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got == nil || got.ID == "" {
		t.Fatal("Enqueue returned a malformed job")
	}
}
