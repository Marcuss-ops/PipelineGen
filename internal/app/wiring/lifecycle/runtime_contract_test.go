// Package app — lifecycle tests (lifecycle-runtime-ownership, June 2026).
//
// These tests exercise the lifecycle.Runtime.Start/Stop contract. They are
// deterministic (no network, no goroutine racing) by using fn probes and
// lifecycle.StartupStep entries the test controls directly, and they verify:
//
//  1. Pre-cancelled ctx → Start returns an error WITHOUT firing any
//     startup step (no goroutine leak on shutdown).
//  2. Failing probe → Start returns an error WITHOUT firing the steps
//     (fail-closed semantics).
//  3. All probes pass → steps are invoked IN ORDER. Required steps that
//     fail abort the sequence; optional failures are skipped.
//  4. Stop is idempotent: calling Stop before Start, after a failed
//     Start, and twice in succession are all safe (no panic, no double-
//     cleanup of partially-initialised state).
//  5. Job runner is always the last step.
package lifecycle_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	lifecycle "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// recorder tracks the order in which probes + steps are invoked.
// The mutex protects concurrent writes from probes running in parallel
// via pkg/concurrent.Group.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) record(name string) {
	r.mu.Lock()
	r.calls = append(r.calls, name)
	r.mu.Unlock()
}

func (r *recorder) hasCall(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c == name {
			return true
		}
	}
	return false
}

func (r *recorder) stepCallsOnly() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		switch c {
		case "dbProbe", "vectorProbe", "driveProbe":
			continue
		default:
			out = append(out, c)
		}
	}
	return out
}

// newLifecycleForTest wires a lifecycle.Runtime whose probes + startup steps
// record invocations into rec. The plan is built from stepNames in order;
// steps fire their Start closures synchronously (no goroutines).
//
// QDRANT-005 (June 2026) closure (lifecycle-runtime-ownership):
// lifecycle.Runtime no longer exposes fixed dbProbe/vectorProbe/driveProbe
// fields — the readiness barrier is unified into a `probes []*probeEntry`
// slice and probes are registered through `AddProbe(name, fn)`. The test
// helper therefore routes each entry through AddProbe instead of direct
// field assignment. The probe NAMES recorded by the test (dbProbe /
// vectorProbe / driveProbe) are unchanged — they're just strings logged
// into rec.calls, not field accesses.
func newLifecycleForTest(plan []lifecycle.StartupStep, probes map[string]func(context.Context) error, cleanup func()) *lifecycle.Runtime {
	rt := lifecycle.NewRuntime(plan, cleanup, zap.NewNop())
	if probes != nil {
		if p := probes["db"]; p != nil {
			rt.AddProbe("db", p)
		}
		if p := probes["vector"]; p != nil {
			rt.AddProbe("vector", p)
		}
		if p := probes["drive"]; p != nil {
			rt.AddProbe("drive", p)
		}
	}
	return rt
}

// makeRecordingStep returns a lifecycle.StartupStep that records its Name when Start
// is called and returns the given error.
func makeRecordingStep(name string, required bool, rec *recorder, err error) lifecycle.StartupStep {
	return lifecycle.StartupStep{
		Name: name, Required: required,
		Start: func(_ context.Context) error {
			rec.record(name)
			return err
		},
		Stop: func(_ context.Context) error {
			rec.record(name + ":stop")
			return nil
		},
	}
}

// TestLifecycle_Start_PropagatesContextError verifies the listener-
// failure scenario: server.Start's signal.NotifyContext for SIGINT/
// SIGTERM cancels the lifecycle ctx, so a subsequent Start must
// fail-closed without firing any step.
func TestLifecycle_Start_PropagatesContextError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	rec := &recorder{}
	plan := []lifecycle.StartupStep{
		makeRecordingStep("step1", true, rec, nil),
	}
	sl := newLifecycleForTest(plan, nil, nil)

	err := sl.Start(ctx)
	if err == nil {
		t.Fatalf("expected error from pre-cancelled ctx, got nil")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("expected zero calls on pre-cancelled ctx, got %v", rec.calls)
	}
}

// TestLifecycle_BarrierFails_ReturnsError verifies the fail-closed path:
// when ANY probe returns non-nil error, Start returns that error and
// NONE of the startup steps fire.
func TestLifecycle_BarrierFails_ReturnsError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		probeErr string // "db", "vector", or "drive"
	}{
		{name: "db-fails", probeErr: "db"},
		{name: "vector-fails", probeErr: "vector"},
		{name: "drive-fails", probeErr: "drive"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			plan := []lifecycle.StartupStep{
				makeRecordingStep("step1", true, rec, nil),
				makeRecordingStep("step2", true, rec, nil),
			}
			probes := map[string]func(context.Context) error{
				"db":     func(ctx context.Context) error { rec.record("dbProbe"); return nil },
				"vector": func(ctx context.Context) error { rec.record("vectorProbe"); return nil },
				"drive":  func(ctx context.Context) error { rec.record("driveProbe"); return nil },
			}
			probes[tt.probeErr] = func(ctx context.Context) error {
				rec.record(tt.probeErr + "Probe")
				return errors.New(tt.probeErr + " down")
			}
			sl := newLifecycleForTest(plan, probes, nil)

			err := sl.Start(context.Background())
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			// No step should have fired.
			for _, c := range rec.calls {
				if c == "step1" || c == "step2" {
					t.Fatalf("fail-closed violation: step %q fired despite barrier failure: %v", c, rec.calls)
				}
			}
		})
	}
}

// TestLifecycle_BarrierPasses_RunsStepsInOrder verifies the happy
// path: all probes pass → steps fire in declaration order.
func TestLifecycle_BarrierPasses_RunsStepsInOrder(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	plan := []lifecycle.StartupStep{
		makeRecordingStep("drive-init", true, rec, nil),
		makeRecordingStep("qdrant-collection", true, rec, nil),
		makeRecordingStep("outbox-pool", true, rec, nil),
		makeRecordingStep("job-scanner", false, rec, nil),
		makeRecordingStep("job-runner", true, rec, nil),
	}
	probes := map[string]func(context.Context) error{
		"db":     func(ctx context.Context) error { rec.record("dbProbe"); return nil },
		"vector": func(ctx context.Context) error { rec.record("vectorProbe"); return nil },
		"drive":  func(ctx context.Context) error { rec.record("driveProbe"); return nil },
	}
	sl := newLifecycleForTest(plan, probes, nil)

	if err := sl.Start(context.Background()); err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}

	// Probes: all 3 must have fired (concurrent.Group may interleave).
	for _, want := range []string{"dbProbe", "vectorProbe", "driveProbe"} {
		found := false
		for _, got := range rec.calls {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("probe %q did not fire (calls=%v)", want, rec.calls)
		}
	}

	// Steps: must appear in the declared order after probes.
	stepNames := []string{}
	for _, c := range rec.calls {
		if c == "dbProbe" || c == "vectorProbe" || c == "driveProbe" {
			continue
		}
		stepNames = append(stepNames, c)
	}
	want := []string{"drive-init", "qdrant-collection", "outbox-pool", "job-scanner", "job-runner"}
	if len(stepNames) != len(want) {
		t.Fatalf("expected %d step calls, got %d (%v)", len(want), len(stepNames), stepNames)
	}
	for i, name := range want {
		if stepNames[i] != name {
			t.Fatalf("step order mismatch at index %d: want %q, got %q (full=%v)", i, name, stepNames[i], rec.calls)
		}
	}
}

// TestLifecycle_Stop_Idempotent verifies the cleanup-safe-on-failure
// contract: Stop is safe to call BEFORE Start (cleanup fires once),
// AFTER a failed Start (partial-probe state, no steps fired), and
// twice in succession (defensive double-Stop is safe).
func TestLifecycle_Stop_Idempotent(t *testing.T) {
	t.Parallel()

	t.Run("stop-before-start", func(t *testing.T) {
		t.Parallel()
		cleanups := 0
		cleanup := func() { cleanups++ }
		sl := newLifecycleForTest(nil, nil, cleanup)
		if err := sl.Stop(context.Background()); err != nil {
			t.Fatalf("Stop before Start must be safe, got %v", err)
		}
		if cleanups != 1 {
			t.Fatalf("cleanup expected exactly 1 call before Start, got %d", cleanups)
		}
	})

	t.Run("stop-after-failed-start", func(t *testing.T) {
		t.Parallel()
		cleanups := 0
		cleanup := func() { cleanups++ }
		rec := &recorder{}
		plan := []lifecycle.StartupStep{
			makeRecordingStep("step1", true, rec, nil),
		}
		probes := map[string]func(context.Context) error{
			"db": func(ctx context.Context) error { return errors.New("db down") },
		}
		sl := newLifecycleForTest(plan, probes, cleanup)

		err := sl.Start(context.Background())
		if err == nil {
			t.Fatalf("expected barrier failure")
		}
		if err := sl.Stop(context.Background()); err != nil {
			t.Fatalf("Stop after failed Start must be safe, got %v", err)
		}
		if cleanups != 1 {
			t.Fatalf("cleanup expected exactly 1 call after failed Start, got %d", cleanups)
		}
		for _, c := range rec.calls {
			if c == "step1" {
				t.Fatalf("fail-closed violation: step %q fired despite barrier failure", c)
			}
		}
	})

	t.Run("stop-twice", func(t *testing.T) {
		t.Parallel()
		cleanups := 0
		cleanup := func() { cleanups++ }
		rec := &recorder{}
		plan := []lifecycle.StartupStep{
			makeRecordingStep("step1", true, rec, nil),
		}
		sl := newLifecycleForTest(plan, nil, cleanup)

		if err := sl.Start(context.Background()); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		if err := sl.Stop(context.Background()); err != nil {
			t.Fatalf("first Stop failed: %v", err)
		}
		if err := sl.Stop(context.Background()); err != nil {
			t.Fatalf("second Stop must be safe, got %v", err)
		}
		if cleanups != 1 {
			t.Fatalf("cleanup expected exactly 1 call across double-Stop, got %d", cleanups)
		}
	})
}

// TestLifecycle_RequiredFailure_AbortsSequence verifies that when a
// Required step fails, subsequent steps (including the job runner)
// are NOT started.
func TestLifecycle_RequiredFailure_AbortsSequence(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	plan := []lifecycle.StartupStep{
		makeRecordingStep("drive-init", true, rec, errors.New("drive folder missing")),
		makeRecordingStep("qdrant-collection", true, rec, nil),
		makeRecordingStep("outbox-pool", true, rec, nil),
		makeRecordingStep("job-runner", true, rec, nil),
	}
	sl := newLifecycleForTest(plan, nil, nil)

	err := sl.Start(context.Background())
	if err == nil {
		t.Fatalf("expected required step failure error")
	}
	// Only drive-init should have fired.
	if len(rec.calls) != 1 || rec.calls[0] != "drive-init" {
		t.Fatalf("expected only drive-init, got %v", rec.calls)
	}
}

// TestLifecycle_OptionalFailure_Continues verifies that optional steps
// that fail do NOT block subsequent steps.
func TestLifecycle_OptionalFailure_Continues(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	plan := []lifecycle.StartupStep{
		makeRecordingStep("job-scanner", false, rec, errors.New("scanner failed")),
		makeRecordingStep("channel-monitor", false, rec, nil),
		makeRecordingStep("job-runner", true, rec, nil),
	}
	sl := newLifecycleForTest(plan, nil, nil)

	err := sl.Start(context.Background())
	if err != nil {
		t.Fatalf("expected nil (optional failures don't abort), got %v", err)
	}
	// All steps should have fired.
	want := []string{"job-scanner", "channel-monitor", "job-runner"}
	if len(rec.calls) != len(want) {
		t.Fatalf("expected %d calls, got %d (%v)", len(want), len(rec.calls), rec.calls)
	}
	for i, name := range want {
		if rec.calls[i] != name {
			t.Fatalf("step order mismatch at %d: want %q, got %q", i, name, rec.calls[i])
		}
	}
}

// TestLifecycle_Stop_ReverseOrder verifies that Stop calls step stops
// in reverse order.
func TestLifecycle_Stop_ReverseOrder(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	plan := []lifecycle.StartupStep{
		makeRecordingStep("step-a", true, rec, nil),
		makeRecordingStep("step-b", true, rec, nil),
		makeRecordingStep("step-c", true, rec, nil),
	}
	sl := newLifecycleForTest(plan, nil, nil)

	if err := sl.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	rec.calls = nil // reset after Start
	if err := sl.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	// Stops should fire in reverse: c:stop, b:stop, a:stop.
	want := []string{"step-c:stop", "step-b:stop", "step-a:stop"}
	if len(rec.calls) != len(want) {
		t.Fatalf("expected %d stops, got %d (%v)", len(want), len(rec.calls), rec.calls)
	}
	for i, name := range want {
		if rec.calls[i] != name {
			t.Fatalf("stop order mismatch at %d: want %q, got %q", i, name, rec.calls[i])
		}
	}
}

// TestLifecycle_JobRunnerLast verifies the structural invariant that
// the job runner is always the last step in the plan.
func TestLifecycle_JobRunnerLast(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	plan := []lifecycle.StartupStep{
		makeRecordingStep("drive-init", true, rec, nil),
		makeRecordingStep("job-scanner", false, rec, nil),
		makeRecordingStep("job-runner", true, rec, nil),
	}
	sl := newLifecycleForTest(plan, nil, nil)

	if err := sl.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Job runner must be the last entry.
	if rec.calls[len(rec.calls)-1] != "job-runner" {
		t.Fatalf("job-runner must be last step, got %v", rec.calls)
	}
}

// TestLifecycle_ContextCancelledDuringStartup verifies that context
// cancellation during startup interrupts the sequence.
func TestLifecycle_ContextCancelledDuringStartup(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())

	plan := []lifecycle.StartupStep{
		{
			Name: "step1", Required: true,
			Start: func(_ context.Context) error {
				rec.record("step1")
				cancel() // Cancel context mid-startup.
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		},
		{
			Name: "step2", Required: true,
			Start: func(ctx context.Context) error {
				rec.record("step2")
				// Check if context is already cancelled.
				return ctx.Err()
			},
			Stop: func(_ context.Context) error { return nil },
		},
		makeRecordingStep("job-runner", true, rec, nil),
	}
	sl := newLifecycleForTest(plan, nil, nil)

	err := sl.Start(ctx)
	if err == nil {
		t.Fatalf("expected error from cancelled context, got nil")
	}
	// step1 fired, step2 fired (and returned ctx.Err), job-runner should NOT fire.
	for _, c := range rec.calls {
		if c == "job-runner" {
			t.Fatalf("job-runner fired despite mid-startup cancellation: %v", rec.calls)
		}
	}
}

// TestLifecycle_NoGoroutinesLeaked verifies that after Stop completes,
// the number of running goroutines returns to the baseline (no leaked
// goroutines). This test launches real goroutines via SafeGo and verifies
// they all exit after context cancellation.
func TestLifecycle_NoGoroutinesLeaked(t *testing.T) {
	// This test must run sequentially — goroutine counting is global.
	ctx, cancel := context.WithCancel(context.Background())

	baseline := runtime.NumGoroutine()

	goroutinesStarted := make(chan struct{}, 2)
	goroutinesDone := make(chan struct{}, 2)

	plan := []lifecycle.StartupStep{
		{
			Name: "leaky-service-1", Required: false,
			Start: func(startCtx context.Context) error {
				concurrent.SafeGo("leaky-service-1", func() {
					goroutinesStarted <- struct{}{}
					<-startCtx.Done()
					goroutinesDone <- struct{}{}
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		},
		{
			Name: "leaky-service-2", Required: false,
			Start: func(startCtx context.Context) error {
				concurrent.SafeGo("leaky-service-2", func() {
					goroutinesStarted <- struct{}{}
					<-startCtx.Done()
					goroutinesDone <- struct{}{}
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		},
	}
	sl := newLifecycleForTest(plan, nil, func() { cancel() })

	if err := sl.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for goroutines to actually start.
	<-goroutinesStarted
	<-goroutinesStarted

	// Verify goroutines did start.
	mid := runtime.NumGoroutine()
	if mid <= baseline {
		t.Fatalf("expected goroutines to have started (baseline=%d, mid=%d)", baseline, mid)
	}

	// Stop: cancels context, goroutines should exit.
	if err := sl.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Wait for goroutines to finish.
	<-goroutinesDone
	<-goroutinesDone

	// Give a moment for goroutine cleanup.
	time.Sleep(50 * time.Millisecond)

	after := runtime.NumGoroutine()
	if after > baseline+2 {
		t.Fatalf("goroutine leak detected: baseline=%d, after=%d (max allowed=%d)",
			baseline, after, baseline+2)
	}
}

// TestSafeCall verifies the panic-to-error helper directly. nil-fn is
// a no-op, normal return is nil, panic returns a named error.
func TestSafeCall(t *testing.T) {
	t.Parallel()

	t.Run("nil-fn", func(t *testing.T) {
		t.Parallel()
		if err := lifecycle.SafeCall("nil", nil); err != nil {
			t.Fatalf("lifecycle.SafeCall(nil) must return nil, got %v", err)
		}
	})

	t.Run("normal-return", func(t *testing.T) {
		t.Parallel()
		called := 0
		err := lifecycle.SafeCall("ok", func() { called++ })
		if err != nil {
			t.Fatalf("lifecycle.SafeCall ok-path must return nil, got %v", err)
		}
		if called != 1 {
			t.Fatalf("lifecycle.SafeCall ok-path must invoke fn once, got %d", called)
		}
	})

	t.Run("panic-return", func(t *testing.T) {
		t.Parallel()
		err := lifecycle.SafeCall("explode", func() { panic("boom") })
		if err == nil {
			t.Fatalf("lifecycle.SafeCall panic must return non-nil error")
		}
		if err.Error() != `lifecycle closure "explode" panicked: boom` {
			t.Fatalf("lifecycle.SafeCall panic error format mismatch: %v", err)
		}
	})
}
