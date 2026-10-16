package model

import "github.com/Marcuss-ops/RenderingGen/queue/client"

// DefaultMaxAttempts is the retry policy default for a render job. It is the
// SINGLE definition: the CLI flag default and the migration's
// `render_jobs.max_attempts DEFAULT` are pinned to it by
// state_schema_test.go, so the in-memory and PostgreSQL backends cannot start
// with a different policy.
const DefaultMaxAttempts = 3

// ShouldFailPermanently reports whether an attempt that just ended must move
// the job to its terminal failed state instead of being requeued. It is the
// single definition of the max-attempts rule: the in-memory store, the
// PostgreSQL backend and the lease-expiry sweep all call it, so they can never
// disagree about when a job gives up.
func ShouldFailPermanently(attempts, maxAttempts int) bool {
	return maxAttempts > 0 && attempts >= maxAttempts
}

// IsTerminalState reports whether a state ends a job's lifecycle. It delegates
// to the canonical wire predicate, so the queue service and the producers
// share one terminal-state set.
func IsTerminalState(state State) bool { return client.IsTerminalState(state) }

// IsClaimable reports whether a worker may be handed a job in this state.
// Pending is normal render work; rendered is a publication-only retry whose
// artifact is already durable.
func IsClaimable(state State) bool {
	return state == StatePending || state == StateRendered
}

// ClaimableStates returns the claimable vocabulary in a stable order. The
// PostgreSQL claim filter and the in-memory store both derive from it.
func ClaimableStates() []State { return []State{StatePending, StateRendered} }

// ── Assembly anchors ────────────────────────────────────────────────────────
//
// A job that OWNS chunk children is an ASSEMBLY ANCHOR: its output is the
// concatenation of its children, each of which covers a disjoint half-open
// FrameRange (see processor.ParentFinalizer + ValidateChildren). Handing such a
// parent to a render worker would render the FULL plan on top of the children
// that already cover it — the double work the half-open FrameRange split
// exists to avoid, paid on the GPU lane.
//
// The rule is "owns children", not a job type or a producer flag, so it holds
// for every producer: both repositories encode it (PostgreSQL as
// `NOT EXISTS (SELECT 1 FROM render_jobs child WHERE child.parent_job_id = id)`,
// the in-memory store by scanning its parent links) and ClaimFinalization is
// the only claim that may take an anchor.

// IsCancelable reports whether a producer cancel may move a job from this
// state to cancelled. Cancelled is terminal and idempotent (the caller handles
// the re-cancel case separately); completed and failed are terminal.
func IsCancelable(state State) bool {
	return !IsTerminalState(state)
}
