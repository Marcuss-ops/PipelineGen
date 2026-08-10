// Package background provides small helpers for detaching a context
// from its parent lifetime and bounding the detached execution by a
// timeout, used by callers that must finish work after the original
// request has disconnected (post-write save, background audit-log,
// audit delivery, etc.).
//
// PR-7 (June 2026, action P1-5 of cleanup plan): pkg/background introduces
// the canonical DetachWithTimeout helper that replaces inline
// `context.WithTimeout(context.WithoutCancel(parentCtx), <duration>)`
// and `context.WithTimeout(context.Background(), <duration>)` calls in
// internal/api/** + internal/application/**. The direct invocations of
// context.Background() / context.WithoutCancel() in those packages are
// tracked under PR-CONTEXT-NO-CANCEL-CI-GATE (architecture/issues.yaml)
// with deadline 2026-07-15. From this commit onward, a dedicated CI
// gate (scripts/ci-architectural-checks.sh Check N) bans the bare
// patterns outside the documented composition-root allowlist — every
// new caller MUST route through this helper.
//
// pkg/ is leaf-only per ARCHITECTURE.md §13 — pkg/background imports
// pkg/corid (existing helper for trace-id propagation) but never
// imports internal/ itself.
package background

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// inFlight is the canonical in-memory registry of detached tasks.
// Entries are grouped by (taskName, traceID), while the count on each
// entry tracks concurrent tasks sharing that correlation key. This keeps
// the registry bounded by active task keys and lets cleanup remove exactly
// one task without deleting a newer task that reused the same key.
//
// Every registration has two cleanup paths: the returned CancelFunc and a
// goroutine watching the detached context. The sync.Once on each task makes
// those paths race-safe, so success (caller cancel), timeout, and explicit
// cancellation all release their registry slot.
var (
	inFlight   sync.Map
	inFlightMu sync.Mutex
)

type inFlightEntry struct {
	count int
}

var inFlightCount atomic.Int64

// InFlight returns the number of currently-registered detached tasks
// across all (taskName, traceID) pairs. Exported for metrics and
// dashboarding. Read-only.
func InFlight() int {
	return int(inFlightCount.Load())
}

// DetachWithTimeout returns a context that survives the cancellation
// of its parent AND carries a deadline of `timeout` from the moment
// this function is called. The trace ID extracted from `ctx` (via
// pkg/corid.FromContext) is registered against `taskName` in the
// in-memory registry so operators can correlate log/metrics lines
// across the request scope and the detached scope.
//
// Cancellation semantics:
//   - parent context cancelled → detached context is NOT cancelled
//     (it is built on context.WithoutCancel(parent)).
//   - detached context's deadline (timeout) expires → the registry entry
//     is removed automatically, even if the caller forgets CancelFunc.
//   - caller invokes CancelFunc → the registry entry is removed exactly
//     once, including when cancellation races with the timeout watcher.
//
// `taskName` must be a short, stable kebab-case identifier — e.g.
// "post-write-save", "voiceover-promo-delivery",
// "outbox-delivery-audit", "search-cache-bump". The trace ID is the
// value of pkg/corid.FromContext(ctx) at call-time ("" if
// unspecified — callers do not need to guard; the helper never
// panics on empty IDs).
//
// This is the canonical replacement for the former post-write context
// helper. Unlike a Background-based context, it preserves context values
// and correlation IDs while removing parent cancellation. The timeout is
// always applied: zero or negative values produce an immediately expired
// context and therefore cannot leave an unbounded registry entry.
func DetachWithTimeout(ctx context.Context, taskName string, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		// Defensive fallback when parentCtx is nil — same shape as
		// the former post-write helper's fallback so call sites that
		// hold a nil parent ctx in test fixtures do not nil-deref on
		// corid.FromContext.
		ctx = context.Background()
	}
	traceID := corid.FromContext(ctx)
	key := taskName + ":" + traceID

	inFlightMu.Lock()
	if existing, ok := inFlight.Load(key); ok {
		entry := existing.(*inFlightEntry)
		entry.count++
	} else {
		inFlight.Store(key, &inFlightEntry{count: 1})
	}
	inFlightCount.Add(1)
	inFlightMu.Unlock()

	detached := context.WithoutCancel(ctx)
	detached, timeoutCancel := context.WithTimeout(detached, timeout)
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			inFlightMu.Lock()
			if value, ok := inFlight.Load(key); ok {
				entry := value.(*inFlightEntry)
				entry.count--
				if entry.count <= 0 {
					inFlight.Delete(key)
				}
			}
			inFlightMu.Unlock()
			inFlightCount.Add(-1)
		})
	}
	stopCleanup := context.AfterFunc(detached, cleanup)

	return detached, func() {
		cleanup()
		timeoutCancel()
		stopCleanup()
	}
}
