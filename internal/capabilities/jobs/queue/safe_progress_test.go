// Package jobs — safe_progress_test.go.
//
// Pins the nil-tolerant SafeProgressFn contract for the canonical
// JobExecutionTools envelope (P1 #13 unification, July 2026). The
// test exercises the three nil-tolerance postures that handlers
// may invoke SafeProgressFn under:
//
//  1. tools == nil                     → no-op closure
//  2. tools.Progress field == nil      → no-op closure
//  3. tools.Progress field non-nil     → forwards 1:1
//
// godlike/07 fail-closed contract: SafeProgressFn MUST NOT panic
// when invoked against a nil or partially-populated tools. The
// Creator-runtime wrap (internal/app/creator_runtime.go::adapted
// closure) and the worker-runtime translateToolsToExecutionTools
// helper both depend on this invariant for nil-tolerance safety
// at the worker composition root.
package queue

import (
	"testing"
)

// TestSafeProgressFn_NilTools_NoOp — nil-tolerant ProgressFn
// witness for the canonical *kerneljob.JobExecutionTools. The
// Creator-runtime wrap (internal/app/creator_runtime.go) and the
// worker.Registry.Dispatch translation helper both produce
// nil-tolerant envelopes (event no-op closures) so a handler that
// calls tools.Progress observes a safe no-op rather than a
// nil-deref panic.
//
// Three postures are exercised:
//
//  1. nil *JobExecutionTools — nilPf must be a callable no-op.
//  2. &{Progress: nil}       — nilProgressPf must be a callable no-op.
//  3. &{Progress: real}      — realPf must forward 1:1 to the inner closure.
//
// TestSafeEventFn_NilTools_NoOp mirrors the SafeProgressFn contract
// for the Event callback. Event emission must be nil-tolerant so
// handlers can call it unconditionally.
func TestSafeEventFn_NilTools_NoOp(t *testing.T) {
	// Posture 1: nil *JobExecutionTools.
	nilFn := SafeEventFn(nil)
	if nilFn == nil {
		t.Fatalf("SafeEventFn(nil) returned nil closure — must return a safe no-op callable")
	}
	nilFn("test", "no-op", map[string]any{"x": 1})

	// Posture 2: nil Event field.
	nilEventFn := SafeEventFn(&JobExecutionTools{Event: nil})
	if nilEventFn == nil {
		t.Fatalf("SafeEventFn(&{Event: nil}) returned nil — must return a no-op callable")
	}
	nilEventFn("test", "no-op", nil)

	// Posture 3: non-nil Event — forwards 1:1.
	observed := make(chan struct {
		et string
		m  string
		d  map[string]any
	}, 1)
	realFn := SafeEventFn(&JobExecutionTools{
		Event: func(eventType, message string, data map[string]any) {
			observed <- struct {
				et string
				m  string
				d  map[string]any
			}{eventType, message, data}
		},
	})
	if realFn == nil {
		t.Fatalf("SafeEventFn(&{Event: real}) returned nil — must return the wrapped closure")
	}
	realFn("clips.hydrated", "clips ok", map[string]any{"n": 3})
	got := <-observed
	if got.et != "clips.hydrated" || got.m != "clips ok" || got.d["n"] != 3 {
		t.Fatalf("forwarded Event mismatch: got {%q, %q, %v}; want {clips.hydrated, clips ok, map[n:3]}", got.et, got.m, got.d)
	}
}

func TestSafeProgressFn_NilTools_NoOp(t *testing.T) {
	// Posture 1: nil *JobExecutionTools.
	nilPf := SafeProgressFn(nil)
	if nilPf == nil {
		t.Fatalf("SafeProgressFn(nil) returned nil closure — must return a safe no-op callable")
	}
	nilPf(50, "should be a no-op (no panic)")

	// Posture 2: nil Progress field.
	nilProgressPf := SafeProgressFn(&JobExecutionTools{Progress: nil})
	if nilProgressPf == nil {
		t.Fatalf("SafeProgressFn(&{Progress: nil}) returned nil — must return a no-op callable")
	}
	nilProgressPf(75, "should be a no-op (no panic)")

	// Posture 3: non-nil Progress — forwards 1:1. We capture via
	// a channel so the assertion is deterministic even under
	// concurrent.SafeGo scheduling nondeterminism elsewhere.
	observed := make(chan struct {
		p int
		m string
	}, 1)
	realPf := SafeProgressFn(&JobExecutionTools{
		Progress: func(p int, msg string) {
			observed <- struct {
				p int
				m string
			}{p, msg}
		},
	})
	if realPf == nil {
		t.Fatalf("SafeProgressFn(&{Progress: real}) returned nil — must return the wrapped closure")
	}
	realPf(42, "hello")
	got := <-observed
	if got.p != 42 || got.m != "hello" {
		t.Fatalf("forwarded Progress mismatch: got {%d, %q}; want {42, \"hello\"}", got.p, got.m)
	}
}
