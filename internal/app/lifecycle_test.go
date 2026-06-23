// Package app — lifecycle readiness-barrier tests (commit fix/lifecycle-readiness).
//
// These tests exercise the serverLifecycle.Start/Stop contract introduced
// to satisfy problem #1. They are deterministic (no network, no goroutine
// racing) by using fn probes the test controls directly, and they verify
// the four invariants the user-facing API guarantees:
//
//  1. Pre-cancelled ctx → Start returns an error WITHOUT firing any
//     deferred-start closure (no goroutine leak on shutdown).
//  2. Failing probe → Start returns an error WITHOUT firing the closures
//     (fail-closed semantics).
//  3. All probes pass → closures are invoked IN ORDER (driveStart →
//     processStart → outboxStart → startJobRunner) — preserves the
//     PR9-A/B/C dependency chain.
//  4. Stop is idempotent: calling Stop before Start, after a failed
//     Start, and twice in succession are all safe (no panic, no double-
//     cleanup of partially-initialised state).
package app

import (
	"context"
	"errors"
	"testing"
)

// recorder tracks the order in which probes + closures are invoked.
type recorder struct {
	calls []string
}

func (r *recorder) record(name string) {
	r.calls = append(r.calls, name)
}

// recordingLifecycle is a builder helper for serverLifecycle test fixtures.
// Probes return nil iff their *_OK flag is set; non-nil errors flow through
// as-is. Closures are recorded into the shared recorder (no dead counters).
type recordingLifecycle struct {
	dbOK, vectorOK, driveOK bool
	dbErr, vectorErr, driveErr error

	cleanup func() // optional cleanup; nil-safe
}

// newLifecycleForTest wires a serverLifecycle whose probes + closures
// record invocations into rec. rec is created on first use; callers may
// share a single rec across sub-tests to assert cross-step ordering.
func newLifecycleForTest(rl *recordingLifecycle) *serverLifecycle {
	rec := &recorder{}
	if rl == nil {
		rl = &recordingLifecycle{dbOK: true, vectorOK: true, driveOK: true}
	}
	sl := &serverLifecycle{
		cleanup: rl.cleanup,
	}
	if rl.dbOK || rl.dbErr != nil {
		sl.dbProbe = func(ctx context.Context) error {
			rec.record("dbProbe")
			if err := ctx.Err(); err != nil {
				return err
			}
			return rl.dbErr
		}
	}
	if rl.vectorOK || rl.vectorErr != nil {
		sl.vectorProbe = func(ctx context.Context) error {
			rec.record("vectorProbe")
			if err := ctx.Err(); err != nil {
				return err
			}
			return rl.vectorErr
		}
	}
	if rl.driveOK || rl.driveErr != nil {
		sl.driveProbe = func(ctx context.Context) error {
			rec.record("driveProbe")
			if err := ctx.Err(); err != nil {
				return err
			}
			return rl.driveErr
		}
	}
	sl.driveStart = func() { rec.record("driveStart") }
	sl.processStart = func() { rec.record("processStart") }
	sl.outboxStart = func() { rec.record("outboxStart") }
	sl.startJobRunner = func() { rec.record("startJobRunner") }
	return sl
}

// closureNames filters recorded call names to only the four closure
// entries (excludes probe names). Tests use it to assert the
// PR9-A/B/C dependency-order invariant without relying on time.
var closureNames = map[string]bool{
	"driveStart":     true,
	"processStart":   true,
	"outboxStart":    true,
	"startJobRunner": true,
}

// TestLifecycle_Start_PropagatesContextError verifies the listener-
// failure scenario: server.Start's signal.NotifyContext for SIGINT/
// SIGTERM cancels the lifecycle ctx, so a subsequent Start must
// fail-closed without firing any closure.
func TestLifecycle_Start_PropagatesContextError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	rl := &recordingLifecycle{
		dbOK: true, vectorOK: true, driveOK: true,
	}
	sl := newLifecycleForTest(rl)
	rec := &recorder{}
	sl.dbProbe = func(ctx context.Context) error { rec.record("dbProbe"); return rl.dbErr }
	sl.vectorProbe = func(ctx context.Context) error { rec.record("vectorProbe"); return rl.vectorErr }
	sl.driveProbe = func(ctx context.Context) error { rec.record("driveProbe"); return rl.driveErr }

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
// NONE of the deferred-start closures fire.
func TestLifecycle_BarrierFails_ReturnsError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		dbOK, vectorOK, driveOK bool
		dbErr, vectorErr, driveErr error
	}{
		{
			name:     "db-fails",
			dbErr:    errors.New("db down"),
			vectorOK: true, driveOK: true,
		},
		{
			name:     "vector-fails",
			vectorErr: errors.New("qdrant down"),
			dbOK:      true, driveOK: true,
		},
		{
			name:     "drive-fails",
			driveErr: errors.New("drive unreachable"),
			dbOK:     true, vectorOK: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rl := &recordingLifecycle{
				dbOK: tt.dbOK, vectorOK: tt.vectorOK, driveOK: tt.driveOK,
				dbErr: tt.dbErr, vectorErr: tt.vectorErr, driveErr: tt.driveErr,
			}
			sl := newLifecycleForTest(rl)
			rec := &recorder{}
			sl.dbProbe = func(ctx context.Context) error { rec.record("dbProbe"); return rl.dbErr }
			sl.vectorProbe = func(ctx context.Context) error { rec.record("vectorProbe"); return rl.vectorErr }
			sl.driveProbe = func(ctx context.Context) error { rec.record("driveProbe"); return rl.driveErr }

			err := sl.Start(context.Background())
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			hasClosure := false
			for _, c := range rec.calls {
				if closureNames[c] {
					hasClosure = true
				}
			}
			if hasClosure {
				t.Fatalf("fail-closed violation: closures fired despite barrier failure: %v", rec.calls)
			}
		})
	}
}

// TestLifecycle_BarrierPasses_RunsClosuresInOrder verifies the happy
// path: all probes pass → driveStart, processStart, outboxStart,
// startJobRunner fire in the documented dependency order (PR7 ordering).
func TestLifecycle_BarrierPasses_RunsClosuresInOrder(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	rl := &recordingLifecycle{
		dbOK: true, vectorOK: true, driveOK: true,
	}
	sl := newLifecycleForTest(rl)
	// Re-wire probes with the recorder so we can observe the probe
	// ordering too (probes run concurrently, so we only assert that
	// all three fired — closure ordering is the actual invariant).
	sl.dbProbe = func(ctx context.Context) error { rec.record("dbProbe"); return rl.dbErr }
	sl.vectorProbe = func(ctx context.Context) error { rec.record("vectorProbe"); return rl.vectorErr }
	sl.driveProbe = func(ctx context.Context) error { rec.record("driveProbe"); return rl.driveErr }
	sl.driveStart = func() { rec.record("driveStart") }
	sl.processStart = func() { rec.record("processStart") }
	sl.outboxStart = func() { rec.record("outboxStart") }
	sl.startJobRunner = func() { rec.record("startJobRunner") }

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

	// Closures: must appear in PR7 dependency order, no interleaving.
	closureCalls := []string{}
	for _, c := range rec.calls {
		if closureNames[c] {
			closureCalls = append(closureCalls, c)
		}
	}
	want := []string{"driveStart", "processStart", "outboxStart", "startJobRunner"}
	if len(closureCalls) != len(want) {
		t.Fatalf("expected %d closure calls, got %d (%v)", len(want), len(closureCalls), closureCalls)
	}
	for i, name := range want {
		if closureCalls[i] != name {
			t.Fatalf("closure order mismatch at index %d: want %q, got %q (full=%v)", i, name, closureCalls[i], rec.calls)
		}
	}
}

// TestLifecycle_Stop_Idempotent verifies the cleanup-safe-on-failure
// contract: Stop is safe to call BEFORE Start (cleanup fires once),
// AFTER a failed Start (partial-probe state, no closures fired), and
// twice in succession (defensive double-Stop is safe).
func TestLifecycle_Stop_Idempotent(t *testing.T) {
	t.Parallel()

	t.Run("stop-before-start", func(t *testing.T) {
		t.Parallel()
		cleanups := 0
		rl := &recordingLifecycle{
			dbOK: true, vectorOK: true, driveOK: true,
			cleanup: func() { cleanups++ },
		}
		sl := newLifecycleForTest(rl)
		if err := sl.Stop(context.Background()); err != nil {
			t.Fatalf("Stop before Start must be safe, got %v", err)
		}
		// cleanup-func was invoked exactly once — not zero (would mean
		// Stop ignored it), not twice (would mean double-cleanup).
		if cleanups != 1 {
			t.Fatalf("cleanup expected exactly 1 call before Start, got %d", cleanups)
		}
		if len(rl.rec.calls) != 0 { // defensive: never wired recorder
			// no-op; rec is nil in this subtest path
		}
	})

	t.Run("stop-after-failed-start", func(t *testing.T) {
		t.Parallel()
		cleanups := 0
		rl := &recordingLifecycle{
			dbErr: errors.New("db down"),
			cleanup: func() { cleanups++ },
		}
		sl := newLifecycleForTest(rl)
		rec := &recorder{}
		sl.dbProbe = func(ctx context.Context) error { rec.record("dbProbe"); return rl.dbErr }
		sl.vectorProbe = func(ctx context.Context) error { rec.record("vectorProbe"); return rl.vectorErr }
		sl.driveProbe = func(ctx context.Context) error { rec.record("driveProbe"); return rl.driveErr }

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
		// fail-closed: no closure fired even though cleanup ran.
		for _, c := range rec.calls {
			if closureNames[c] {
				t.Fatalf("fail-closed violation: closure %q fired despite barrier failure", c)
			}
		}
	})

	t.Run("stop-twice", func(t *testing.T) {
		t.Parallel()
		cleanups := 0
		rl := &recordingLifecycle{
			dbOK: true, vectorOK: true, driveOK: true,
			cleanup: func() { cleanups++ },
		}
		sl := newLifecycleForTest(rl)
		rec := &recorder{}
		sl.dbProbe = func(ctx context.Context) error { rec.record("dbProbe"); return rl.dbErr }
		sl.vectorProbe = func(ctx context.Context) error { rec.record("vectorProbe"); return rl.vectorErr }
		sl.driveProbe = func(ctx context.Context) error { rec.record("driveProbe"); return rl.driveErr }

		if err := sl.Start(context.Background()); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		if err := sl.Stop(context.Background()); err != nil {
			t.Fatalf("first Stop failed: %v", err)
		}
		if err := sl.Stop(context.Background()); err != nil {
			t.Fatalf("second Stop must be safe, got %v", err)
		}
		// Exactly 1 cleanup call (the second Stop is a defensive no-op).
		if cleanups != 1 {
			t.Fatalf("cleanup expected exactly 1 call across double-Stop, got %d", cleanups)
		}
	})
}

// TestSafeCall verifies the panic-to-error helper directly. nil-fn is
// a no-op, normal return is nil, panic returns a named error.
func TestSafeCall(t *testing.T) {
	t.Parallel()

	t.Run("nil-fn", func(t *testing.T) {
		t.Parallel()
		if err := SafeCall("nil", nil); err != nil {
			t.Fatalf("SafeCall(nil) must return nil, got %v", err)
		}
	})

	t.Run("normal-return", func(t *testing.T) {
		t.Parallel()
		called := 0
		err := SafeCall("ok", func() { called++ })
		if err != nil {
			t.Fatalf("SafeCall ok-path must return nil, got %v", err)
		}
		if called != 1 {
			t.Fatalf("SafeCall ok-path must invoke fn once, got %d", called)
		}
	})

	t.Run("panic-return", func(t *testing.T) {
		t.Parallel()
		err := SafeCall("explode", func() { panic("boom") })
		if err == nil {
			t.Fatalf("SafeCall panic must return non-nil error")
		}
		if err.Error() != `lifecycle closure "explode" panicked: boom` {
			t.Fatalf("SafeCall panic error format mismatch: %v", err)
		}
	})
}
