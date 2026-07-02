package jobs

import (
	"testing"
)

// TestSafeProgressFn_NilTools_NoPanic: a nil *JobTools must not panic.
// Calling the returned closure with any pct/msg must be a silent no-op.
func TestSafeProgressFn_NilTools_NoPanic(t *testing.T) {
	fn := SafeProgressFn(nil)
	// No defer-recover needed: the closure is itself a no-op.
	fn(5, "starting voiceover.generate_item")
	fn(100, "voiceover.generate_item execution complete")
	fn(0, "")
	fn(-1, "negative percent still safe")
}

// TestSafeProgressFn_NilProgressField_NoPanic: a non-nil *JobTools but
// with Progress==nil must produce a no-op closure (defensive guard
// against partial tool wiring).
func TestSafeProgressFn_NilProgressField_NoPanic(t *testing.T) {
	tools := &JobTools{Progress: nil}
	fn := SafeProgressFn(tools)
	fn(50, "hello")
	fn(100, "done")
}

// TestSafeProgressFn_ValidProgress_ForwardsArgs: when Progress is set,
// SafeProgressFn returns the underlying Progress closure verbatim.
// Each call forwards (pct, msg) 1:1.
func TestSafeProgressFn_ValidProgress_ForwardsArgs(t *testing.T) {
	type call struct {
		pct int
		msg string
	}
	var got []call
	tools := &JobTools{
		Progress: func(progress int, message string) {
			got = append(got, call{pct: progress, msg: message})
		},
	}
	fn := SafeProgressFn(tools)

	fn(5, "starting voiceover.generate_item")
	fn(100, "voiceover.generate_item execution complete")

	if len(got) != 2 {
		t.Fatalf("expected 2 calls to underlying Progress, got %d", len(got))
	}
	if got[0] != (call{5, "starting voiceover.generate_item"}) {
		t.Errorf("got[0] = %+v; want {5, \"starting voiceover.generate_item\"}", got[0])
	}
	if got[1] != (call{100, "voiceover.generate_item execution complete"}) {
		t.Errorf("got[1] = %+v; want {100, \"voiceover.generate_item execution complete\"}", got[1])
	}
}

// TestSafeProgressFn_NoOpIsIdempotent: calling the no-op closure 1000
// times must be silent and side-effect-free. This is the canonical
// audit-pin for the Creator-wrap nil-Tools path — the canonical safety
// gate relies on idempotent no-op semantics for the (rare) offline
// restart window when a job is enqueued but tools haven't been wired.
func TestSafeProgressFn_NoOpIsIdempotent(t *testing.T) {
	fn := SafeProgressFn(nil)
	for i := 0; i < 1000; i++ {
		fn(i, "iteration")
	}
}

// TestSafeProgressFn_NilProgressField_Idempotent: same idempotency
// audit for the partial-wiring case (Progress is nil but Event/IsCancelled
// might be wired).
func TestSafeProgressFn_NilProgressField_Idempotent(t *testing.T) {
	tools := &JobTools{Progress: nil}
	fn := SafeProgressFn(tools)
	for i := 0; i < 1000; i++ {
		fn(i, "iteration")
	}
}

// TestSafeProgressFn_ValidProgress_ExactForwardingCount: each call to
// the returned fn maps to exactly one call to the underlying Progress.
// (An off-by-one impl would re-call or skip — neither is acceptable.)
func TestSafeProgressFn_ValidProgress_ExactForwardingCount(t *testing.T) {
	calls := 0
	tools := &JobTools{
		Progress: func(progress int, message string) {
			calls++
		},
	}
	fn := SafeProgressFn(tools)
	for i := 0; i < 7; i++ {
		fn(i, "x")
	}
	if calls != 7 {
		t.Errorf("expected 7 underlying calls, got %d", calls)
	}
}
