// Package app — lifecycle integration tests (lifecycle-runtime-ownership, June 2026).
//
// These tests exercise the serverLifecycle.Start/Stop contract end-to-end
// with realistic mock services that simulate Drive, Qdrant, outbox, scanner,
// monitor, and job-runner behavior. Goroutines are launched and verified to
// exit cleanly; ordering and error propagation are asserted.
//
// The tests are sequential (t.Parallel disabled where goroutine counting
// or shared channels are used) to avoid cross-test interference.
package wiring

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// ── Mock service helpers ────────────────────────────────────────────────

// mockService simulates a real background service that starts, runs until
// context cancellation, and stops cleanly. It signals readiness via a
// channel.
type mockService struct {
	name    string
	started chan struct{}
	stopped chan struct{}
}

func newMockService(name string) *mockService {
	return &mockService{
		name:    name,
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

// start launches the service in a goroutine via SafeGo.
func (m *mockService) start(ctx context.Context) {
	concurrent.SafeGo(m.name, func() {
		m.started <- struct{}{}
		<-ctx.Done()
		close(m.stopped)
	})
}

// ── Integration: Full Happy Path ────────────────────────────────────────

// TestLifecycleIntegration_FullHappyPath verifies the complete lifecycle
// with mock Drive, Qdrant, outbox, scanner, monitor, sweepers, and job-runner.
// All probes pass, all services start in order, and Stop reverses the order
// while all goroutines exit cleanly.
func TestLifecycleIntegration_FullHappyPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseline := runtime.NumGoroutine()

	// Build mock services.
	// PR-QDRANT-FINAL-DECISION (2026-07-04): qdrant-cleaner + qdrant-health-monitor
	// are removed (3 background-cleanup steps retired earlier per the
	// stale-cleaner / ghost-sweeper / health-monitor audit at
	// go:117-128). qdrant-collection is RETAINED — it is a REAL
	// production step (wire_services.go:170 calls cm.EnsureSchema).
	driveSvc := newMockService("drive-init")
	qdrantSvc := newMockService("qdrant-collection")
	outboxSvc := newMockService("outbox-pool")
	scannerSvc := newMockService("job-scanner")
	monitorSvc := newMockService("channel-monitor")

	rec := &recorder{}

	// Build the startup plan: prerequisite required services first,
	// then optional background services, then job-runner last.
	plan := []StartupStep{
		// Prerequisite required services.
		{
			Name: "drive-init", Required: true,
			Start: func(startCtx context.Context) error {
				rec.record("drive-init")
				driveSvc.start(startCtx)
				return nil
			},
			Stop: func(_ context.Context) error { rec.record("drive-init:stop"); return nil },
		},
		{
			Name: "qdrant-collection", Required: true,
			Start: func(startCtx context.Context) error {
				rec.record("qdrant-collection")
				qdrantSvc.start(startCtx)
				return nil
			},
			Stop: func(_ context.Context) error { rec.record("qdrant-collection:stop"); return nil },
		},
		{
			Name: "outbox-pool", Required: true,
			Start: func(startCtx context.Context) error {
				rec.record("outbox-pool")
				outboxSvc.start(startCtx)
				return nil
			},
			Stop: func(_ context.Context) error { rec.record("outbox-pool:stop"); return nil },
		},
		// Optional background services.
		{
			Name: "job-scanner", Required: false,
			Start: func(startCtx context.Context) error {
				rec.record("job-scanner")
				scannerSvc.start(startCtx)
				return nil
			},
			Stop: func(_ context.Context) error { rec.record("job-scanner:stop"); return nil },
		},
		{
			Name: "channel-monitor", Required: false,
			Start: func(startCtx context.Context) error {
				rec.record("channel-monitor")
				monitorSvc.start(startCtx)
				return nil
			},
			Stop: func(_ context.Context) error { rec.record("channel-monitor:stop"); return nil },
		},
		// PR-QDRANT-FINAL-DECISION (2026-07-04): qdrant-cleaner + qdrant-health-monitor
		// steps removed (background-cleanup topology retired per
		// go:117-128). qdrant-collection above is the only Qdrant step
		// remaining in the production startup plan (wire_services.go:170).
		// Job runner: always last, required.
		{
			Name: "job-runner", Required: true,
			Start: func(startCtx context.Context) error {
				rec.record("job-runner")
				return nil
			},
			Stop: func(_ context.Context) error { rec.record("job-runner:stop"); return nil },
		},
	}

	// All probes pass.
	probes := map[string]func(context.Context) error{
		"db":     func(ctx context.Context) error { rec.record("dbProbe"); return nil },
		"vector": func(ctx context.Context) error { rec.record("vectorProbe"); return nil },
		"drive":  func(ctx context.Context) error { rec.record("driveProbe"); return nil },
	}

	cleanups := 0
	sl := newLifecycleForTest(plan, probes, func() { cleanups++ })

	// ── Start ─────────────────────────────────────────────────────────
	if err := sl.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify all probes fired.
	for _, want := range []string{"dbProbe", "vectorProbe", "driveProbe"} {
		if !rec.hasCall(want) {
			t.Fatalf("probe %q did not fire", want)
		}
	}

	// Verify startup order: prerequisite → optional → job runner.
	// PR-QDRANT-FINAL-DECISION (2026-07-04): qdrant-cleaner + qdrant-health-monitor
	// removed from the expected order.
	stepOrder := rec.stepCallsOnly()
	wantOrder := []string{
		"drive-init", "qdrant-collection", "outbox-pool",
		"job-scanner", "channel-monitor",
		"job-runner",
	}
	if len(stepOrder) != len(wantOrder) {
		t.Fatalf("expected %d step calls, got %d (%v)", len(wantOrder), len(stepOrder), stepOrder)
	}
	for i, name := range wantOrder {
		if stepOrder[i] != name {
			t.Fatalf("step order mismatch at index %d: want %q, got %q (full=%v)", i, name, stepOrder[i], stepOrder)
		}
	}

	// Verify job-runner is the last step.
	if len(stepOrder) == 0 || stepOrder[len(stepOrder)-1] != "job-runner" {
		t.Fatalf("job-runner must be last step, got %v", stepOrder)
	}

	// Wait for all mock goroutines to signal they started.
	// PR-QDRANT-FINAL-DECISION (2026-07-04): sweeperSvc + healthSvc removed.
	waitStarted(t, driveSvc, qdrantSvc, outboxSvc, scannerSvc, monitorSvc)

	// Verify goroutines are running.
	mid := runtime.NumGoroutine()
	if mid <= baseline {
		t.Fatalf("expected goroutines to be running (baseline=%d, mid=%d)", baseline, mid)
	}

	// ── Stop ──────────────────────────────────────────────────────────
	rec.mu.Lock()
	rec.calls = nil
	rec.mu.Unlock()

	if err := sl.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Verify cleanup was called exactly once.
	if cleanups != 1 {
		t.Fatalf("cleanup expected 1 call, got %d", cleanups)
	}

	// Verify stop order: reverse of start order.
	rec.mu.Lock()
	stopOrder := make([]string, len(rec.calls))
	copy(stopOrder, rec.calls)
	rec.mu.Unlock()

	// PR-QDRANT-FINAL-DECISION (2026-07-04): qdrant-cleaner:stop +
	// qdrant-health-monitor:stop removed from the expected stop order.
	wantStops := []string{
		"job-runner:stop",
		"channel-monitor:stop",
		"job-scanner:stop",
		"outbox-pool:stop",
		"qdrant-collection:stop",
		"drive-init:stop",
	}
	if len(stopOrder) != len(wantStops) {
		t.Fatalf("expected %d stop calls, got %d (%v)", len(wantStops), len(stopOrder), stopOrder)
	}
	for i, name := range wantStops {
		if stopOrder[i] != name {
			t.Fatalf("stop order mismatch at index %d: want %q, got %q", i, name, stopOrder[i])
		}
	}

	// Cancel context to signal remaining goroutines, then wait for them.
	// PR-QDRANT-FINAL-DECISION (2026-07-04): sweeperSvc + healthSvc removed.
	cancel()
	waitStopped(t, driveSvc, qdrantSvc, outboxSvc, scannerSvc, monitorSvc)

	// No goroutine leak.
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > baseline+2 {
		t.Fatalf("goroutine leak: baseline=%d, after=%d", baseline, after)
	}
}

// ── Integration: Drive Probe Failure ────────────────────────────────────

// TestLifecycleIntegration_DriveProbeFailure verifies that when the Drive
// probe fails, no startup steps execute and Stop remains safe.
func TestLifecycleIntegration_DriveProbeFailure(t *testing.T) {
	rec := &recorder{}
	plan := []StartupStep{
		makeRecordingStep("drive-init", true, rec, nil),
		makeRecordingStep("job-runner", true, rec, nil),
	}
	probes := map[string]func(context.Context) error{
		"db":    func(ctx context.Context) error { rec.record("dbProbe"); return nil },
		"drive": func(ctx context.Context) error { rec.record("driveProbe"); return errors.New("drive unreachable") },
	}
	cleanups := 0
	sl := newLifecycleForTest(plan, probes, func() { cleanups++ })

	err := sl.Start(context.Background())
	if err == nil {
		t.Fatal("expected error from drive probe failure, got nil")
	}
	if !strings.Contains(err.Error(), "drive unreachable") {
		t.Fatalf("expected error containing 'drive unreachable', got %v", err)
	}

	// No steps should have fired.
	if rec.hasCall("drive-init") || rec.hasCall("job-runner") {
		rec.mu.Lock()
		snap := make([]string, len(rec.calls))
		copy(snap, rec.calls)
		rec.mu.Unlock()
		t.Fatalf("fail-closed violation: step fired despite probe failure: %v", snap)
	}

	// Stop must be safe after failed Start.
	if err := sl.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after probe failure must be safe: %v", err)
	}
	if cleanups != 1 {
		t.Fatalf("cleanup expected 1 call after probe failure, got %d", cleanups)
	}
}

// ── Integration: Qdrant Required Step Failure ───────────────────────────

// TestLifecycleIntegration_QdrantCollectionFailure verifies that when a
// required prerequisite service (Qdrant EnsureCollection) fails, the
// sequence aborts and no subsequent steps execute.
func TestLifecycleIntegration_QdrantCollectionFailure(t *testing.T) {
	rec := &recorder{}
	plan := []StartupStep{
		{
			Name: "drive-init", Required: true,
			Start: func(_ context.Context) error { rec.record("drive-init"); return nil },
			Stop:  func(_ context.Context) error { rec.record("drive-init:stop"); return nil },
		},
		{
			Name: "qdrant-collection", Required: true,
			Start: func(_ context.Context) error {
				rec.record("qdrant-collection")
				return fmt.Errorf("qdrant collection v3 not found")
			},
			Stop: func(_ context.Context) error { rec.record("qdrant-collection:stop"); return nil },
		},
		{
			Name: "outbox-pool", Required: true,
			Start: func(_ context.Context) error { rec.record("outbox-pool"); return nil },
			Stop:  func(_ context.Context) error { rec.record("outbox-pool:stop"); return nil },
		},
		{
			Name: "job-runner", Required: true,
			Start: func(_ context.Context) error { rec.record("job-runner"); return nil },
			Stop:  func(_ context.Context) error { rec.record("job-runner:stop"); return nil },
		},
	}
	sl := newLifecycleForTest(plan, nil, nil)

	err := sl.Start(context.Background())
	if err == nil {
		t.Fatal("expected error from qdrant-collection failure")
	}

	// drive-init fired, qdrant-collection fired and failed; outbox and job-runner must NOT fire.
	if !rec.hasCall("drive-init") {
		t.Fatal("drive-init should have fired")
	}
	if !rec.hasCall("qdrant-collection") {
		t.Fatal("qdrant-collection should have fired (and failed)")
	}
	if rec.hasCall("outbox-pool") {
		t.Fatal("outbox-pool must NOT fire after required step failure")
	}
	if rec.hasCall("job-runner") {
		t.Fatal("job-runner must NOT fire after required step failure")
	}

	// Stop: reverse order of started steps only.
	rec.mu.Lock()
	rec.calls = nil
	rec.mu.Unlock()
	_ = sl.Stop(context.Background())
	rec.mu.Lock()
	got := make([]string, len(rec.calls))
	copy(got, rec.calls)
	rec.mu.Unlock()

	if len(got) != 2 || got[0] != "qdrant-collection:stop" || got[1] != "drive-init:stop" {
		t.Fatalf("expected reverse stop of started steps only, got %v", got)
	}
}

// ── Integration: Optional Step Failure Continues ────────────────────────

// TestLifecycleIntegration_OptionalStepFailure verifies that optional
// service failures are logged but do not prevent subsequent steps.
func TestLifecycleIntegration_OptionalStepFailure(t *testing.T) {
	rec := &recorder{}
	plan := []StartupStep{
		{
			Name: "job-scanner", Required: false,
			Start: func(_ context.Context) error {
				rec.record("job-scanner")
				return errors.New("scanner DB busy")
			},
			Stop: func(_ context.Context) error { rec.record("job-scanner:stop"); return nil },
		},
		{
			Name: "channel-monitor", Required: false,
			Start: func(_ context.Context) error { rec.record("channel-monitor"); return nil },
			Stop:  func(_ context.Context) error { rec.record("channel-monitor:stop"); return nil },
		},
		{
			Name: "job-runner", Required: true,
			Start: func(_ context.Context) error { rec.record("job-runner"); return nil },
			Stop:  func(_ context.Context) error { rec.record("job-runner:stop"); return nil },
		},
	}
	sl := newLifecycleForTest(plan, nil, nil)

	// Optional failure must NOT abort.
	if err := sl.Start(context.Background()); err != nil {
		t.Fatalf("optional failure must not abort Start, got %v", err)
	}

	// All three steps must fire (including the failing optional one).
	if !rec.hasCall("job-scanner") {
		t.Fatal("job-scanner should have fired (and failed)")
	}
	if !rec.hasCall("channel-monitor") {
		t.Fatal("channel-monitor should have fired after optional failure")
	}
	if !rec.hasCall("job-runner") {
		t.Fatal("job-runner must fire even after optional failure")
	}
}

// ── Integration: Context Cancellation During Startup ─────────────────────

// TestLifecycleIntegration_ContextCancelledDuringStartup verifies that
// context cancellation mid-sequence aborts the remaining steps without
// leaking goroutines.
func TestLifecycleIntegration_ContextCancelledDuringStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseline := runtime.NumGoroutine()

	rec := &recorder{}
	startedSvc := newMockService("started-service")

	plan := []StartupStep{
		{
			Name: "drive-init", Required: true,
			Start: func(_ context.Context) error {
				rec.record("drive-init")
				return nil
			},
			Stop: func(_ context.Context) error { rec.record("drive-init:stop"); return nil },
		},
		{
			Name: "qdrant-collection", Required: true,
			Start: func(startCtx context.Context) error {
				rec.record("qdrant-collection")
				startedSvc.start(startCtx)
				cancel() // Cancel mid-startup.
				return nil
			},
			Stop: func(_ context.Context) error { rec.record("qdrant-collection:stop"); return nil },
		},
		{
			Name: "outbox-pool", Required: true,
			Start: func(startCtx context.Context) error {
				rec.record("outbox-pool")
				return startCtx.Err() // Should return context.Canceled.
			},
			Stop: func(_ context.Context) error { rec.record("outbox-pool:stop"); return nil },
		},
		{
			Name: "job-runner", Required: true,
			Start: func(_ context.Context) error { rec.record("job-runner"); return nil },
			Stop:  func(_ context.Context) error { rec.record("job-runner:stop"); return nil },
		},
	}
	sl := newLifecycleForTest(plan, nil, nil)

	err := sl.Start(ctx)
	if err == nil {
		t.Fatal("expected error from context cancellation mid-startup")
	}

	// drive-init and qdrant-collection fired; outbox-pool returned error; job-runner must NOT fire.
	if !rec.hasCall("drive-init") {
		t.Fatal("drive-init should have fired")
	}
	if !rec.hasCall("qdrant-collection") {
		t.Fatal("qdrant-collection should have fired")
	}
	if !rec.hasCall("outbox-pool") {
		t.Fatal("outbox-pool should have fired (and returned ctx.Err())")
	}
	if rec.hasCall("job-runner") {
		t.Fatal("job-runner must NOT fire after context cancellation")
	}

	// Wait for the mock service to stop.
	waitStartedThenStopped(t, startedSvc)

	// No goroutine leak.
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > baseline+2 {
		t.Fatalf("goroutine leak after context cancellation: baseline=%d, after=%d", baseline, after)
	}
}

// ── Integration: Concurrent SafeGo Services ─────────────────────────────

// TestLifecycleIntegration_ConcurrentSafeGoServices verifies that services
// launched via SafeGo inside step.Start closures are properly tracked and
// exit on context cancellation during Stop.
func TestLifecycleIntegration_ConcurrentSafeGoServices(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseline := runtime.NumGoroutine()

	var wg sync.WaitGroup
	servicesStarted := make(chan struct{}, 4)
	servicesDone := make(chan struct{}, 4)

	// Create a step that launches N goroutines (simulating a pool worker).
	makePoolStep := func(name string, workers int) StartupStep {
		return StartupStep{
			Name: name, Required: false,
			Start: func(startCtx context.Context) error {
				for i := 0; i < workers; i++ {
					wg.Add(1)
					workerID := i
					concurrent.SafeGo(fmt.Sprintf("%s-worker-%d", name, workerID), func() {
						defer wg.Done()
						servicesStarted <- struct{}{}
						<-startCtx.Done()
						servicesDone <- struct{}{}
					})
				}
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		}
	}

	plan := []StartupStep{
		makePoolStep("worker-pool-a", 2),
		makePoolStep("worker-pool-b", 2),
		{
			Name: "job-runner", Required: true,
			Start: func(_ context.Context) error { return nil },
			Stop:  func(_ context.Context) error { return nil },
		},
	}

	sl := newLifecycleForTest(plan, nil, func() { cancel() })

	if err := sl.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for all 4 workers to start.
	for i := 0; i < 4; i++ {
		select {
		case <-servicesStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for workers to start")
		}
	}

	mid := runtime.NumGoroutine()
	if mid <= baseline {
		t.Fatalf("expected workers to be running (baseline=%d, mid=%d)", baseline, mid)
	}

	// Stop: cleanup cancels context, then waits for workers.
	if err := sl.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Wait for all workers to finish.
	for i := 0; i < 4; i++ {
		select {
		case <-servicesDone:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for workers to stop")
		}
	}

	// Verify WaitGroup completed (no dangling goroutines).
	wg.Wait()

	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > baseline+2 {
		t.Fatalf("goroutine leak after concurrent workers: baseline=%d, after=%d", baseline, after)
	}
}

// ── Integration: Idempotent Stop After Partial Start ────────────────────

// TestLifecycleIntegration_IdempotentStopAfterPartialStart verifies that
// calling Stop after a partially-completed Start is safe and idempotent.
func TestLifecycleIntegration_IdempotentStopAfterPartialStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := &recorder{}
	s1 := newMockService("svc-1")
	s2 := newMockService("svc-2")

	plan := []StartupStep{
		{
			Name: "svc-1", Required: true,
			Start: func(startCtx context.Context) error {
				rec.record("svc-1")
				s1.start(startCtx)
				return nil
			},
			Stop: func(_ context.Context) error { rec.record("svc-1:stop"); return nil },
		},
		{
			Name: "svc-2", Required: true,
			Start: func(startCtx context.Context) error {
				rec.record("svc-2")
				s2.start(startCtx)
				return fmt.Errorf("svc-2 crashed on init")
			},
			Stop: func(_ context.Context) error { rec.record("svc-2:stop"); return nil },
		},
		{
			Name: "job-runner", Required: true,
			Start: func(_ context.Context) error { rec.record("job-runner"); return nil },
			Stop:  func(_ context.Context) error { rec.record("job-runner:stop"); return nil },
		},
	}

	cleanups := 0
	sl := newLifecycleForTest(plan, nil, func() { cleanups++; cancel() })

	err := sl.Start(ctx)
	if err == nil {
		t.Fatal("expected svc-2 failure")
	}

	// svc-1 started, svc-2 started and failed. job-runner must NOT have fired.
	if !rec.hasCall("svc-1") {
		t.Fatal("svc-1 should have fired")
	}
	if !rec.hasCall("svc-2") {
		t.Fatal("svc-2 should have fired (and failed)")
	}
	if rec.hasCall("job-runner") {
		t.Fatal("job-runner must NOT fire after required failure")
	}

	// Wait for svc-1 to signal it started.
	waitStarted(t, s1)

	// First Stop
	rec.mu.Lock()
	rec.calls = nil
	rec.mu.Unlock()
	if err := sl.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop failed: %v", err)
	}
	if cleanups != 1 {
		t.Fatalf("cleanup expected 1 after first Stop, got %d", cleanups)
	}

	// svc-1:stop must fire (was started), svc-2:stop must fire (was started and failed).
	// job-runner:stop must NOT fire (was never started).
	if !rec.hasCall("svc-1:stop") {
		t.Fatal("svc-1:stop must fire for started service")
	}
	if !rec.hasCall("svc-2:stop") {
		t.Fatal("svc-2:stop must fire for failed-but-started service")
	}
	if rec.hasCall("job-runner:stop") {
		t.Fatal("job-runner:stop must NOT fire for never-started service")
	}

	// Wait for s1 to stop.
	waitStopped(t, s1)

	// Second Stop: idempotent.
	rec.mu.Lock()
	rec.calls = nil
	rec.mu.Unlock()
	if err := sl.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
	if cleanups != 1 {
		t.Fatalf("cleanup expected to remain 1 after second Stop, got %d", cleanups)
	}
	// Stops fire again (the signature is idempotent at the step level).
}

// ── Helpers ─────────────────────────────────────────────────────────────

func waitStarted(t *testing.T, svcs ...*mockService) {
	t.Helper()
	for _, s := range svcs {
		select {
		case <-s.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for %s to start", s.name)
		}
	}
}

func waitStopped(t *testing.T, svcs ...*mockService) {
	t.Helper()
	for _, s := range svcs {
		select {
		case <-s.stopped:
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for %s to stop", s.name)
		}
	}
}

func waitStartedThenStopped(t *testing.T, svcs ...*mockService) {
	t.Helper()
	waitStarted(t, svcs...)
	waitStopped(t, svcs...)
}
