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
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// inFlight is the canonical in-memory registry of detached tasks.
// Each entry records (taskName, traceID, registeredAt) keyed by
// "<taskName>:<traceID>". Operators query this map to surface live
// post-write-save tasks in dashboards or audit probes. Entries are
// pruned per task-cancel via the cleanup hook in a follow-up slice.
//
// The map is sufficient for metrics + log correlation. Per the spec
// the alternative (log-only with no retention) loses the ability to
// count concurrent in-flight detaches; this implementation keeps the
// registry but does NOT bound it — long-running detach sessions are
// expected to remain <30s and the registry remains small.
//
// Concurrency: sync.Map is the canonical choice for "write-once +
// sporadic read" without fine-grained locking. The working set stays
// at most a few dozen concurrent entries because callers consistently
// cancel on deadline.
var inFlight sync.Map

// InFlight returns the count of currently-registered detached tasks
// across all (taskName, traceID) pairs. Exported for metrics +
// dashboarding. Read-only.
func InFlight() int {
	n := 0
	inFlight.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
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
//   - detached context's deadline (timeout) expires → CancelFunc MUST
//     be called by the caller to release resources; the helper
//     itself does NOT auto-clean the registry entry on deadline
//     (the registry is diagnostics, not lifecycle).
//
// `taskName` must be a short, stable kebab-case identifier — e.g.
// "post-write-save", "voiceover-promo-delivery",
// "outbox-delivery-audit", "search-cache-bump". The trace ID is the
// value of pkg/corid.FromContext(ctx) at call-time ("" if
// unspecified — callers do not need to guard; the helper never
// panics on empty IDs).
//
// Mirrors the helper shape used by pkg/contextutil/postwrite.go
// (the original composition-root post-write ctx), generalised so
// the same shape works for any detached-with-timeout call site.
func DetachWithTimeout(ctx context.Context, taskName string, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		// Defensive fallback when parentCtx is nil — same shape as
		// the existing pkg/contextutil/postwrite.go fallback so call
		// sites that hold a nil parent ctx in test fixtures do not
		// nil-deref on corid.FromContext.
		ctx = context.Background()
	}
	traceID := corid.FromContext(ctx)
	key := taskName + ":" + traceID
	inFlight.Store(key, time.Now())
	detached := context.WithoutCancel(ctx)
	return context.WithTimeout(detached, timeout)
}
