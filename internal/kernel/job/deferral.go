// Package job — deferral.go: the non-consuming "come back later" contract.
//
// A handler that cannot make progress YET — because it is waiting on work that
// lives outside the jobs plane (a remote render, a provider window, a GPU lane)
// — must be able to hand its attempt back WITHOUT spending a retry. Retries are
// the budget for "the work failed, try it again"; a wait is not a failure, and
// charging it to that budget is how a long external wait turns into a terminal
// FAILED job.
//
// This file owns that contract:
//
//   - Deferral is the typed request: a reason plus how long to stay away.
//   - Deferred / DeferredAfter build it; the error always unwraps to
//     ErrDeferred, so a caller that only cares "is this a wait?" probes the
//     sentinel and a caller that needs the delay uses AsDeferral.
//   - AsDeferral is the single accessor the worker uses to translate a
//     handler's error into the canonical OutcomeDeferred finalize outcome.
//
// The persistence half lives in the store: the job goes back to RETRY_WAIT with
// retry_count UNCHANGED and a `deferred_until` instant, and the worker's
// requeue sweep re-enqueues it when that instant passes (see
// capabilities/jobs/scheduling.RetryDue). Nothing else about the lifecycle
// changes: a deferred job is non-terminal, cancellable, and visible in the
// normal RETRY_WAIT views.
//
// godlike/02 kernel rule: this file declares only stdlib types (errors, fmt,
// strings, time); no cross-package import.
package job

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrDeferred is the sentinel that marks a handler error as a WAIT rather than
// a failure. Every Deferral unwraps to it, so `errors.Is(err, job.ErrDeferred)`
// is the cheapest correct probe — and the one the worker's finalize
// classification uses.
var ErrDeferred = errors.New("job deferred")

// Deferral is a handler's request to be re-dispatched after Delay.
//
// Reason is persisted verbatim as the job's error column and in the
// job_deferred audit event, so an operator reading a RETRY_WAIT row can tell a
// wait apart from a failure without parsing timestamps.
type Deferral struct {
	// Reason is the human-readable explanation of what the job is waiting for.
	// Required: an unexplained wait is indistinguishable from a stuck job.
	Reason string
	// Delay is how long to stay away before the next attempt. Delay <= 0 means
	// "use the scheduler's own deferral delay", which is the deployment knob an
	// operator tunes; the handler states the FACT (I am waiting), not the
	// cadence.
	Delay time.Duration
}

// Error implements error. The message leads with the sentinel wording so the
// persisted text cannot be mistaken for a failure.
func (d *Deferral) Error() string {
	if d == nil {
		return ErrDeferred.Error()
	}
	reason := strings.TrimSpace(d.Reason)
	switch {
	case reason == "":
		return ErrDeferred.Error()
	case d.Delay > 0:
		return fmt.Sprintf("%s: %s (retry after %s)", ErrDeferred, reason, d.Delay)
	default:
		return fmt.Sprintf("%s: %s", ErrDeferred, reason)
	}
}

// Unwrap exposes ErrDeferred so errors.Is(err, ErrDeferred) and errors.As work
// through any wrapping the caller added.
func (d *Deferral) Unwrap() error { return ErrDeferred }

// DeferredAfter asks for a re-dispatch after delay, explaining why.
func DeferredAfter(delay time.Duration, reason string) error {
	return &Deferral{Reason: reason, Delay: delay}
}

// Deferred asks for a re-dispatch after the scheduler's own deferral delay.
func Deferred(reason string) error {
	return &Deferral{Reason: reason}
}

// AsDeferral reports whether err (or anything it wraps) is a deferral, and
// returns it. A nil error is never a deferral.
func AsDeferral(err error) (*Deferral, bool) {
	if err == nil {
		return nil, false
	}
	var deferral *Deferral
	if errors.As(err, &deferral) {
		return deferral, true
	}
	// A plain ErrDeferred (no typed wrapper) still carries the intent; the
	// scheduler's default delay applies because Delay stays zero.
	if errors.Is(err, ErrDeferred) {
		return &Deferral{}, true
	}
	return nil, false
}
