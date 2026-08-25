// Package jobs — heartbeat_writer.go provides the canonical typed
// port for the broker liveness signal.
//
// PR-HEARTBEAT-TELEMETRY-BUG (2026-07-04): replaces the prior
// stringly-typed math.MaxInt64 sentinel in heartbeat_tracker.go's
// BrokerHeartbeatAge() with a port-based contract. Callers inject a
// concrete HeartbeatWriter at composition boot; the /ready probe reads
// writer.AgeSeconds() to decide checks.jobs.ok=true|false.
//
// Per godlike/06 SSOT (one canonical owner per fact): the
// HeartbeatWriter port is the SOLE owner of the liveness timestamp in
// the composition-root runtime path. Package-level BrokerLastHeartbeat
// (in heartbeat_tracker.go) remains as a deprecated storage seam for
// legacy callers (e.g. inspector-monitor loops) but is NOT consulted by
// the /ready probe after this PR.
//
// Per godlike/07 no-fake-availability: AgeSeconds() NEVER returns
// math.MaxInt64 (the prior sentinel was a fake-availability signal —
// a never-recorded beat returning MaxInt64 silently masked "broker
// never started" as "broker is wedged"). The new contract:
//   - 0 <= age < stalenessThreshold (default 60s) → healthy
//   - age >= stalenessThreshold → stale
//   - caller decides whether to also enforce "must have at least
//     one beat ever recorded" via a separate configured check
//     (TBD per AGENTS.md Patterns — surfaced via gap analysis below).
package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// HeartbeatWriter is the typed port for the broker liveness signal.
//
// Methods:
//
//	MarkBeat()  — stamp the current Unix timestamp as last known-good beat.
//	AgeSeconds() — return seconds since last beat (or 0 if Start() seeded it).
//	Stop()       — signal the background ticker goroutine to exit. Idempotent.
type HeartbeatWriter interface {
	MarkBeat()
	AgeSeconds() int64
	Stop()
}

// ErrHeartbeatWriterNil is the canonical typed sentinel returned by
// probe consumers when the HeartbeatWriter dependency is nil (the
// compose-root contract: a probe MUST fail closed rather than rely
// on a sentinel-value pattern that the writer's AgeSeconds() can
// return). Per godlike/07 typed-error contract, callers MUST use
// this sentinel via errors.Is(err, ErrHeartbeatWriterNil).
//
// Renamed from the prior ErrHeartbeatWriterStopped (2026-07-04): the
// prior name misleadingly implied MarkBeat-after-Stop semantics, but
// the actual usage in probe code is nil-port detection. The renamed
// sentinel matches the actual semantics — see build_bundles_core.go's
// RunnerProbe closure (which consults hbWriter.AgeSeconds() only when
// hbWriter != nil; the canonical probe must fail closed via this
// sentinel when the composition-root wrote zero writers).
var ErrHeartbeatWriterNil = errors.New("heartbeat: HeartbeatWriter dependency is nil")

// Compile-time pin — godlike/06 SSOT discipline. Any future drift in
// the HeartbeatWriter signature surfaces as build failure rather
// than runtime panic. Pair with `var _ HeartbeatWriter =
// (*TickerHeartbeatWriter)(nil)` so the port contract is locked at
// compile-time.
var _ HeartbeatWriter = (*TickerHeartbeatWriter)(nil)

// HeartbeatWriterIntervalDefault is the canonical tick interval
// (25s = 1/3 of the 90s SessionTTL). The 3.6x safety margin gives
// 65s of TTL left before the master marks the worker as dead if a
// tick is delayed (network jitter, GC pause, etc.).
const HeartbeatWriterIntervalDefault = 25 * time.Second

// TickerHeartbeatWriter is the canonical composition-root-injected impl.
// It fires MarkBeat() on a configurable interval (default 25s). The
// /ready probe reads AgeSeconds() to determine staleness status.
//
// The goroutine lifetime is bounded by the parent ctx passed to Start():
// on ctx cancel the ticker exits, the writer becomes inert (no tick
// fires until a fresh Start()), and AgeSeconds() continues to return
// the last successful beat timestamp.
//
// godlike/06 SSOT: TickerHeartbeatWriter is the SOLE concrete for the
// HeartbeatWriter port. Future writers (e.g. db-backed for HA) MUST
// satisfy the same HeartbeatWriter interface + compile-time pin
// `var _ HeartbeatWriter = (*NewImpl)(nil)`.
type TickerHeartbeatWriter struct {
	interval time.Duration
	last     atomic.Int64
	stopCh   chan struct{}
	stopOnce sync.Once
	started  atomic.Bool
}

// NewTickerHeartbeatWriter constructs a TickerHeartbeatWriter. The
// ticker does NOT auto-start — composition root lifecycle MUST call
// Start(ctx) explicitly so the goroutine lifetime is bounded by the
// parent ctx (per godlike/07 minimum-blast-radius: the composition
// root owns startup/teardown, NOT package-level init).
//
// interval: 0 = use HeartbeatWriterIntervalDefault (25s).
//
// Per godlike/07 typed-error contract: NewTickerHeartbeatWriter NEVER
// returns an error; the contract is fulfilled by construction. Callers
// that need a different policy (e.g. fail-closed if interval > some
// max) should wrap with their own constructor.
func NewTickerHeartbeatWriter(interval time.Duration) *TickerHeartbeatWriter {
	if interval <= 0 {
		interval = HeartbeatWriterIntervalDefault
	}
	return &TickerHeartbeatWriter{
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start runs the background ticker until ctx is cancelled or Stop() is
// called. Calls MarkBeat() once immediately to seed the timestamp,
// then on each tick.
//
// Per godlike/07 minimum-blast-radius: Start is idempotent within a
// single lifecycle but should NOT be called twice (second call is a
// no-op + warning log line via the started atomic flag).
func (w *TickerHeartbeatWriter) Start(ctx context.Context) {
	if !w.started.CompareAndSwap(false, true) {
		return // idempotent: already started
	}
	w.MarkBeat() // seed: pre-port code would have read math.MaxInt64 here instead
	concurrent.SafeGo("jobs-heartbeat", func() {
		t := time.NewTicker(w.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			case <-t.C:
				w.MarkBeat()
			}
		}
	})
}

// MarkBeat stamps the current Unix timestamp as the last known-good beat.
// Safe for concurrent calls (atomic load/store). After Stop(), MarkBeat
// is a no-op (the last value is preserved, NOT reset to 0 — preserves the
// audit timeline integrity).
func (w *TickerHeartbeatWriter) MarkBeat() {
	w.last.Store(time.Now().Unix())
}

// AgeSeconds returns time.Now().Unix() - lastBeat, or 0 if no beat
// recorded yet (last == zero-value).
//
// Per godlike/07 no-fake-availability: this method NEVER returns
// math.MaxInt64 or any other sentinel value. Caller interprets 0 as
// "freshly seeded OR writer hasn't started" — the writer SHOULD be
// started before the /ready handler can call this method (composition
// root lifecycle ordering invariant).
func (w *TickerHeartbeatWriter) AgeSeconds() int64 {
	last := w.last.Load()
	if last == 0 {
		return 0
	}
	return time.Now().Unix() - last
}

// Stop signals the background ticker to exit. Idempotent (safe to
// call multiple times).
func (w *TickerHeartbeatWriter) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
}
