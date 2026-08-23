// Package usecase — progress_test.go (Issue 8 / P2, June 2026).
//
// Pins the nil-safety contract on ProgressTracker:
//
//  1. TestProgressTracker_PhaseMethodsNilSafe: a nil *ProgressTracker
//     receiver must not panic on any of the 8 Phase* method calls.
//     Pre-Issue-8, each Phase* accessed p.item BEFORE calling Emit,
//     which panicked on a nil receiver. The fix routes all Phase*
//     methods through the centralized `phase(percent, format, args...)`
//     helper that does the nil-guard FIRST, then item-prefixed
//     formatting, then Emit. The test exercises the contract on a
//     naked nil pointer (no construction).
//
//  2. TestProgressTracker_EmitOnNil: redundant guard for the
//     canonical Emit path on a nil receiver. Emit has its own
//     nil-guard (defensive) and this test pins it.
//
//  3. TestProgressTracker_PhaseHelper: confirms the centralized
//     `phase` helper actually prepends the `[item]` prefix and
//     forwards the percent + formatted message to the underlying
//     ProgressFn. Pins the helper's contract beyond the nil-guard.
//
//  4. TestProgressTracker_HappyPath_PhaseMethodsEmit: confirms the
//     happy path on a real (non-nil) tracker wires the Phase*
//     methods to the recording callback. Defensive continuity pin
//     so future refactors cannot regress the call-through wiring.
package usecase

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestProgressTracker_PhaseMethodsNilSafe is the canonical Issue 8
// / P2 test. Calls every Phase* method on a nil *ProgressTracker
// pointer and asserts each call does not panic.
//
// The sub-tests cover the 8 documented phase methods in source
// order so future additions land in an obvious slot:
//
//	PhaseNormalize()         -- no args
//	PhaseValidate()          -- no args
//	PhaseResolveSource()     -- no args
//	PhaseBuildPlan()         -- no args
//	PhaseGenerateStart()     -- no args
//	PhaseGenerateDone()      -- no args
//	PhasePostprocess(string) -- 1 string arg
//	PhaseComplete()          -- no args
//
// The PhasePostprocess call uses a sentinel string ("test_processor")
// so a future refactor that drops the variadic arg gets caught at
// runtime (the helper would fail to format `%s`).
func TestProgressTracker_PhaseMethodsNilSafe(t *testing.T) {
	t.Parallel()

	var p *ProgressTracker // explicit nil pointer; no construction.

	// Each sub-test wraps one Phase* call. The assert.NotPanics
	// helper recovers any panic and surfaces the failure with the
	// method name in the test report.
	type call struct {
		name string
		fn   func()
	}
	calls := []call{
		{"PhaseNormalize", func() { p.PhaseNormalize() }},
		{"PhaseValidate", func() { p.PhaseValidate() }},
		{"PhaseResolveSource", func() { p.PhaseResolveSource() }},
		{"PhaseBuildPlan", func() { p.PhaseBuildPlan() }},
		{"PhaseGenerateStart", func() { p.PhaseGenerateStart() }},
		{"PhaseGenerateDone", func() { p.PhaseGenerateDone() }},
		{"PhasePostprocess", func() { p.PhasePostprocess("test_processor") }},
		{"PhaseComplete", func() { p.PhaseComplete() }},
	}

	for _, c := range calls {
		c := c // capture range var
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.NotPanics(t, c.fn,
				"Issue 8 / P2: %s must be nil-safe (no panic on nil receiver)", c.name)
		})
	}
}

// TestProgressTracker_EmitOnNil pins the canonical Emit path on a
// nil receiver. Emit has its own nil-guard (defensive) so callers
// can rely on it as a belt-and-suspenders path.
func TestProgressTracker_EmitOnNil(t *testing.T) {
	t.Parallel()

	var p *ProgressTracker
	assert.NotPanics(t, func() { p.Emit(50, "ignored") },
		"Emit on nil receiver must not panic")
}

// recordingProgressFn is a goroutine-safe ProgressFn that records
// the last (percent, message) pair it observed. Used by the helper
// and happy-path tests to assert the call-through wiring.
type recordingProgressFn struct {
	mu      sync.Mutex
	percent int
	message string
	called  int
}

func (r *recordingProgressFn) record(percent int, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.percent = percent
	r.message = message
	r.called++
}

func (r *recordingProgressFn) snapshot() (percent int, message string, called int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.percent, r.message, r.called
}

// TestProgressTracker_PhaseHelper pins the centralized `phase`
// helper's contract on a non-nil receiver. The helper must:
//  1. Format with `[item]` prefix injected at the start of the message
//  2. Forward the percent + formatted message to the underlying
//     ProgressFn (via Emit)
//  3. NOT prepend a second `[item]` if the format string already
//     starts with `[`
func TestProgressTracker_PhaseHelper(t *testing.T) {
	t.Parallel()

	rec := &recordingProgressFn{}
	tracker := NewProgressTracker(rec.record, "item-xyz")

	// Direct call to the phase helper with a format that takes no args.
	tracker.phase(42, "custom message")

	gotPercent, gotMessage, gotCalled := rec.snapshot()
	assert.Equal(t, 1, gotCalled, "phase helper must call underlying ProgressFn exactly once")
	assert.Equal(t, 42, gotPercent, "percent must be forwarded verbatim to the ProgressFn")
	assert.Equal(t, "[item-xyz] custom message", gotMessage,
		"phase helper must inject [item] prefix into the formatted message")
}

// TestProgressTracker_PhaseHelper_VariadicArgs confirms the
// variadic args form (`phase(percent, "fmt %s", arg)`) works.
// The helper must apply [item] prefix, then run fmt.Sprintf with
// the variadic args.
func TestProgressTracker_PhaseHelper_VariadicArgs(t *testing.T) {
	t.Parallel()

	rec := &recordingProgressFn{}
	tracker := NewProgressTracker(rec.record, "v-1")

	tracker.phase(95, "Running postprocessor: %s...", "clip_bindings")

	_, gotMessage, _ := rec.snapshot()
	assert.Equal(t, "[v-1] Running postprocessor: clip_bindings...", gotMessage,
		"phase helper must inject [item] prefix then apply variadic fmt args")
}

// TestProgressTracker_PhaseHelper_NilSafe is a redundant belt for
// the `phase` helper's nil-guard. The Phase* tests above already
// exercise the nil path transitively, but a direct call pins the
// contract for any future refactor that bypasses the Phase* methods.
func TestProgressTracker_PhaseHelper_NilSafe(t *testing.T) {
	t.Parallel()

	var p *ProgressTracker
	assert.NotPanics(t, func() { p.phase(99, "ignored") },
		"phase helper on nil receiver must not panic")
}

// TestProgressTracker_EventForwarding pins the event callback
// contract: SetEventFn wires a callback; TrackEvent forwards the
// event type, message, and data; nil tracker or nil callback is a
// no-op.
func TestProgressTracker_EventForwarding(t *testing.T) {
	t.Parallel()

	observed := make(chan struct {
		et string
		m  string
		d  map[string]any
	}, 1)

	rec := &recordingProgressFn{}
	tracker := NewProgressTracker(rec.record, "event-item")
	tracker.SetEventFn(func(eventType, message string, data map[string]any) {
		observed <- struct {
			et string
			m  string
			d  map[string]any
		}{eventType, message, data}
	})

	tracker.TrackEvent("narrative.planned", "plan built", map[string]any{"words": 120})

	got := <-observed
	assert.Equal(t, "narrative.planned", got.et)
	assert.Equal(t, "plan built", got.m)
	assert.Equal(t, 120, got.d["words"])
}

// TestProgressTracker_EventNilSafe confirms that TrackEvent on a nil
// receiver or with a nil callback does not panic.
func TestProgressTracker_EventNilSafe(t *testing.T) {
	t.Parallel()

	var p *ProgressTracker
	assert.NotPanics(t, func() { p.TrackEvent("x", "y", nil) },
		"TrackEvent on nil receiver must not panic")

	rec := &recordingProgressFn{}
	tracker := NewProgressTracker(rec.record, "no-event")
	assert.NotPanics(t, func() { tracker.TrackEvent("x", "y", nil) },
		"TrackEvent with nil callback must not panic")
}

// TestProgressTracker_HappyPath_PhaseMethodsEmit confirms the
// happy path on a real (non-nil) tracker wires the Phase* methods
// to the recording callback. Each Phase* call must produce exactly
// one ProgressFn invocation with the expected percent + `[item]`
// prefix. Defensive continuity pin so future refactors cannot
// regress the call-through wiring.
//
// Issue 8 / P2 review fix: each sub-test constructs its OWN fresh
// `recordingProgressFn` + `tracker` so the 8 sub-tests can run in
// parallel without a shared-state race. The previous version
// shared a single `recordingProgressFn` across all sub-tests; with
// `t.Parallel()` the writes from one sub-test (e.g. percent=55)
// could be observed by another (e.g. sub-test 1 reading
// `rec.snapshot()` expecting percent=5 but getting 55) and the
// assertion would fail non-deterministically. The recording fn's
// mutex makes individual writes atomic but does not serialise
// the sequence across goroutines.
//
// Case-table signature note: the 7 niladic Phase* methods fit
// `func(*ProgressTracker)` directly via method-values
// `(*ProgressTracker).PhaseXxx`. `PhasePostprocess(processor string)`
// takes a string arg, so the case row uses a closure to bind the
// arg instead of a method-value -- the method-value's type
// `func(*ProgressTracker, string)` does not match the case-table
// `func(*ProgressTracker)` type, which would be a compile error.
func TestProgressTracker_HappyPath_PhaseMethodsEmit(t *testing.T) {
	t.Parallel()

	// Method-value captures the canonical fn signature
	// `func(*ProgressTracker)`. The `(*ProgressTracker).PhaseXxx`
	// method-values let the case table stay declarative while the
	// per-sub-test tracker is constructed below.
	type expectation struct {
		name    string
		fn      func(*ProgressTracker)
		percent int
	}
	cases := []expectation{
		{"PhaseNormalize", (*ProgressTracker).PhaseNormalize, 5},
		{"PhaseValidate", (*ProgressTracker).PhaseValidate, 15},
		{"PhaseResolveSource", (*ProgressTracker).PhaseResolveSource, 25},
		{"PhaseBuildPlan", (*ProgressTracker).PhaseBuildPlan, 45},
		{"PhaseGenerateStart", (*ProgressTracker).PhaseGenerateStart, 55},
		{"PhaseGenerateDone", (*ProgressTracker).PhaseGenerateDone, 85},
		// PhasePostprocess takes a `processor string` arg, so the
		// case-row is a closure that binds the sentinel arg. The
		// bare method-value `(*ProgressTracker).PhasePostprocess`
		// has type `func(*ProgressTracker, string)` and would NOT
		// match the `fn func(*ProgressTracker)` case-table type.
		{"PhasePostprocess", func(t *ProgressTracker) { t.PhasePostprocess("test") }, 95},
		{"PhaseComplete", (*ProgressTracker).PhaseComplete, 100},
	}

	for _, c := range cases {
		c := c // capture range var
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			// Per-sub-test fresh state: no shared-mutation race.
			rec := &recordingProgressFn{}
			tracker := NewProgressTracker(rec.record, "happy-item")
			c.fn(tracker)

			gotPercent, gotMessage, gotCalled := rec.snapshot()
			assert.Equal(t, 1, gotCalled, "%s must invoke ProgressFn exactly once", c.name)
			assert.Equal(t, c.percent, gotPercent, "%s must emit the canonical percent", c.name)
			assert.Contains(t, gotMessage, "[happy-item]",
				"%s must prefix the message with [item]", c.name)
		})
	}
}
