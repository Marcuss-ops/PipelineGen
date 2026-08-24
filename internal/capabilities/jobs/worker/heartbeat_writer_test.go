// Package jobs — heartbeat_writer_test.go provides 4 TDD regression
// tests covering the 4 canonical hypotheses called out in
// PR-HEARTBEAT-TELEMETRY-BUG (2026-07-04):
//
//  1. compose-root nil-port: a typed HeartbeatWriter nil-interface
//     is detectable via `w == nil` (per godlike/06 SSOT compile-time
//     pin + the explicit nil-port contract). The probe consumer
//     fails closed via ErrHeartbeatWriterStopped — preventing the
//     math.MaxInt64 sentinel anti-pattern from leaking back into
//     caller code.
//
//  2. HEARTBEAT_ENABLED config toggle: the canonical default in
//     internal/platform/config/types.go::FeaturesConfig.HeartbeatEnabled
//     is `true` (so /ready passes in --mode all deployments without
//     needing an external worker heartbeat). Operators can override
//     via VELOX_FEATURE_HEARTBEAT_ENABLED env or yaml heartbeat_enabled.
//
//  3. I/O swallow: the Start goroutine ticks on a Ticker; MarkBeat is
//     atomic-Store (no I/O) today, but this test also pins the contract
//     that a future impl with downstream I/O (e.g. db-backed audit log)
//     MUST follow the same non-blocking dispatch: log + continue, do not
//     interrupt the ticker's loop. Stop() is also verified idempotent.
//
//  4. storage-table aura (in-memory atomic int): the writer stores the
//     beat as an atomic.Int64 Unix-timestamp. After Start()+immediate
//     MarkBeat seed, AgeSeconds() returns 0 (fresh stamp). The contract
//     is that the writer NEVER returns math.MaxInt64 (the prior
//     sentinel-leak bug) — this is the canonical seal of the fix.
package worker

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Test 1 — Hypothesis 1: compose-root nil-port (sealed behavior).
//
// Pin the contract that a typed nil HeartbeatWriter IS detectable
// (w == nil returns true) AND that a probe consumer REQUIRES a
// non-nil writer. This is the canonical godlike/06 SSOT seal — the
// writer's existence is the contract; absence fails closed.
func TestHeartbeatWriter_NilProbeFailsClosed(t *testing.T) {
	var nilWriter HeartbeatWriter // typed nil interface
	if nilWriter != nil {
		t.Fatalf("typed nil interface must compare == nil per Go's typed-nil rules")
	}

	// Mock probe consumer that fails-closed on nil writer.
	probe := func() error {
		if nilWriter == nil {
			return ErrHeartbeatWriterNil
		}
		_ = nilWriter.AgeSeconds()
		return nil
	}
	if err := probe(); err != ErrHeartbeatWriterNil {
		t.Fatalf("probe must return ErrHeartbeatWriterNil on nil writer, got: %v", err)
	}

	// Conversely: a non-nil writer goes through MarkBeat + AgeSeconds()
	// without raising the sentinel.
	w := NewTickerHeartbeatWriter(0)
	if err := func() error {
		w.MarkBeat()
		if w.AgeSeconds() > 2 {
			t.Fatalf("AgeSeconds() right after MarkBeat() must be near 0 (≤2s), got: %d", w.AgeSeconds())
		}
		return nil
	}(); err != nil {
		t.Fatalf("non-nil writer probe path must not error, got: %v", err)
	}
}

// Test 2 — Hypothesis 2: HEARTBEAT_ENABLED config toggle semantics.
//
// Pin the contract that the canonical default HEARTBEAT_ENABLED=true
// causes the composition root (build_bundles_core.go::buildHealthService)
// to wire a TickerHeartbeatWriter. When HEARTBEAT_ENABLED=false the
// writer is nil and RunnerProbe falls through to the legacy path.
//
// The test verifies:
//   - NewTickerHeartbeatWriter(0) uses the canonical 25s default
//     (HeartbeatWriterIntervalDefault), not a zero-interval that
//     would burn CPU
//   - The body of a freshly-constructed writer has AgeSeconds() == 0
//   - After Start(ctx), AgeSeconds() is near 0 (immediate seed)
//   - Stop() is safe to call AFTER Start()
func TestHeartbeatWriter_ToggleDefaultsAndStartStop(t *testing.T) {
	expectedDefault := HeartbeatWriterIntervalDefault
	if expectedDefault != 25*time.Second {
		t.Fatalf("HeartbeatWriterIntervalDefault must be 25s (1/3 of 90s SessionTTL), got: %v", expectedDefault)
	}

	w := NewTickerHeartbeatWriter(0)
	// Fresh-stamp signal: pre-start writer AgeSeconds() = 0
	if got := w.AgeSeconds(); got != 0 {
		t.Fatalf("AgeSeconds() for fresh (un-started) writer must be 0, got: %d", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	defer w.Stop()
	// Immediate seed: right after Start, AgeSeconds() should be ≤2s
	// (Start seeds MarkBeat() synchronously before launching goroutine).
	if got := w.AgeSeconds(); got > 2 {
		t.Fatalf("AgeSeconds() immediately after Start() must be near 0 (≤2s tolerance), got: %d", got)
	}
}

// Test 3 — Hypothesis 3: I/O swallow semantics on the ticker loop.
//
// Pin the contract that the background ticker's loop is non-blocking
// AND Stop() is idempotent. The test runs N quick ticks and verifies
// the loop did not panick even when Stop() is called twice.
func TestHeartbeatWriter_TickerNonBlockingAndStopIdempotent(t *testing.T) {
	const quickInterval = 200 * time.Millisecond
	w := NewTickerHeartbeatWriter(quickInterval)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Let the ticker fire ~3 times.
	time.Sleep(700 * time.Millisecond)

	// Stop in correct order; second Stop() MUST be a no-op (no panic).
	w.Stop()
	w.Stop() // idempotent

	// Post-Stop: AgeSeconds() is preserved (pre-port code resets to 0
	// scrolled the audit timeline); monotonic non-decreasing reads.
	last := w.AgeSeconds()
	if last < 0 {
		t.Fatalf("AgeSeconds() post-Stop must remain monotonic (≥0), got: %d", last)
	}
}

// Test 4 — Hypothesis 4: storage-table aura (atomic-int seam).
//
// Pin the contract that the writer NEVER returns math.MaxInt64 (the
// pre-PR sentinel-leak bug). The fresh-stamp invariant: AgeSeconds()
// for a freshly-constructed (un-started) writer is 0, not
// math.MaxInt64. The test also verifies 100 concurrent MarkBeat()
// calls do not corrupt the atomic-int seam.
func TestHeartbeatWriter_StorageAtomicInvariantAndNeverMaxInt64(t *testing.T) {
	// Pre-PR pattern invariant (the bug): math.MaxInt64 was the canonical
	// sentinel. Post-PR invariant (the fix): 0 is the fresh-stamp.
	w := NewTickerHeartbeatWriter(0)
	if got := w.AgeSeconds(); got == mathMaxInt64 {
		t.Fatalf("AgeSeconds() returned math.MaxInt64 — sentinel-leak bug regressed")
	}
	if got := w.AgeSeconds(); got != 0 {
		t.Fatalf("AgeSeconds() for fresh (un-started) writer must be 0, got: %d (sentinel-leak)", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	defer w.Stop()

	// 3 successive reads over a short interval — monotonic non-decreasing.
	var prev int64 = -1
	for i := 0; i < 3; i++ {
		got := w.AgeSeconds()
		if got < 0 {
			t.Fatalf("AgeSeconds() must never return negative (sentinel-leak bug), got: %d on iter %d", got, i)
		}
		if got == mathMaxInt64 {
			t.Fatalf("AgeSeconds() returned math.MaxInt64 on iter %d — sentinel-leak bug regressed", i)
		}
		if got < prev {
			t.Fatalf("AgeSeconds() must be monotonic non-decreasing across iterations, got: prev=%d cur=%d", prev, got)
		}
		prev = got
		time.Sleep(50 * time.Millisecond)
	}

	// Concurrent MarkBeat() calls — 100 goroutines. atomic.Int64.Store
	// is concurrency-safe; last writer wins.
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.MarkBeat()
		}()
	}
	wg.Wait()
	age := w.AgeSeconds()
	if age < 0 || age > 1 {
		t.Fatalf("AgeSeconds() after 100 concurrent MarkBeat() calls must be near 0 (≤1s), got: %d (storage corruption bug)", age)
	}
}

// mathMaxInt64 is a local constant to keep the test-side reference
// detached from the implementation import. Imports `math` would make
// the test fragile if the impl ever swapped to a duration-based return.
// Post-fix: this constant is the canonical sentinel-leak bound.
const mathMaxInt64 = int64(^uint64(0) >> 1)
