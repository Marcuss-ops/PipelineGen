// Package completion — completionbus.go (Cut 6.5, July 2026).
//
// Canonical typed-port JobCompletionBus.
//
// Cut 6.5 (the user-facing half of the canonical "no per-job polling
// cycles" goal): Cut 6.3 wired the WORKER tier's typed LeaseState
// propagation; this file ships the API/CLI's sister typed-port so any
// future handler or CLI command that needs to wait for a job to
// terminal-transition can subscribe to the bus instead of polling
// broker.Get(j.ID) in a tight loop.
//
// godlike/06 SSOT one-canonical-owner-per-fact: the canonical typed-port
// JobCompletionBus is the SOLE publish/subscribe surface for
// completion-event delivery; no other abstraction (channel, bus,
// pub/sub) may be introduced for this purpose.
//
// WAVE-3 DEMOLITION NOTE (2026-10-26): the in-process concrete
// implementation (`notifier` + `NewBus` + `subscription` envelope) had no
// production constructor callers left; it was relocated verbatim to the
// test-only file `completionbus_impl_test.go` so the e2e bus semantics
// coverage keeps working without keeping unreachable production code
// alive. Future Postgres LISTEN/NOTIFY adapter satisfies the same
// JobCompletionBus interface here.
//
// godlike/07 minimum-blast-radius: every field on JobCompletionEvent is
// optional with defensively-checked accessors (no nil-deref on a
// partially-populated event from a future Postgres adapter).
package finalize

import (
	"context"
	"errors"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ErrWaitTimedOut is the canonical typed-error sentinel returned by
// Subscription.Await when the supplied context is cancelled before the
// completion event arrives. Forwarded to callers as-is via wrap; the
// errors.Is probe matches the typed sentinel so concurrent consumers
// can branch on the typed reason (godlike/06/07 contract).
var ErrWaitTimedOut = errors.New("completion bus: subscription Await timed out (ctx cancelled before publish)")

// JobCompletionEvent is the canonical typed payload published on the
// bus. Maps to the post-finalization job snapshot — enough state for
// any API/CLI waiter to render the terminal transition without a
// follow-up DB Get.
//
// godlike/07 minimum-blast-radius: Err is nil on success-path events
// (StatusSucceeded); non-nil on StatusFailed / StatusDeadLetter.
// Revision is post-finalization (the canonical fence used by the
// typed `CompleteWithArtifacts` finalizer).
type JobCompletionEvent struct {
	JobID       string
	Attempt     int
	FinalStatus job.Status
	Err         error
	Revision    int
}

// Subscription is the typed-port envelope returned by
// JobCompletionBus.Subscribe. Pattern 0 godlike/06 SSOT: no raw
// channels escape the typed-port surface; callers consume via
// Await + Close. This eliminates an entire class of channel-misuse
// bugs (e.g. blocking on a closed channel, failing to close a
// subscription and leaking the channel reference).
type Subscription interface {
	// Await blocks until either:
	//   - a posted JobCompletionEvent arrives (returns it), OR
	//   - the supplied ctx is cancelled (returns ErrWaitTimedOut wrapping ctx.Err()).
	// Await is single-shot — once it returns, the subscription is drained
	// and Close should be called. Calling Await again returns the
	// zero-value event + ErrSubscriptionClosed (fail-closed posture
	// against reusing a drained subscription).
	Await(ctx context.Context) (JobCompletionEvent, error)

	// Close removes the subscription from the bus bookkeeping and
	// permits garbage collection of the underlying channel. Idempotent
	// (a second Close call returns silently).
	Close()
}

// ErrSubscriptionClosed is returned when Await is called on a drained
// or Closed subscription. godlike/07 fail-closed: never silently
// re-await.
var ErrSubscriptionClosed = errors.New("completion bus: subscription already closed or drained")

// JobCompletionBus is the canonical typed-port for completion-event
// publish/subscribe. Only consumers construct implementations today:
// the canonical in-process impl is a test-only relocation (see
// completionbus_impl_test.go).
//
// Godlike/06 SSOT: JobCompletionBus is the SOLE canonical typed-port
// for completion-event delivery. No other abstraction (channel, bus,
// pub/sub) may be introduced for this purpose. Future Postgres
// LISTEN/NOTIFY adapter satisfies this same interface (the
// Postgres-side impl adds a ctx-aware constructor at composition-init
// time — not a Subscribe-signature parameter); callers are agnostic to
// in-process vs distributed transport.
//
// Pattern 0 single-method discipline: Subscribe takes only the key.
// Cancellation lives at the per-Await ctx (the canonical caller-side
// boundary). A future Postgres LISTEN adapter that needs LISTEN-session
// lifecycle can re-introduce a separate constructor argument; the
// typed-port surface stays minimal.
type JobCompletionBus interface {
	Publish(evt JobCompletionEvent)
	Subscribe(jobID string) (Subscription, error)
}
