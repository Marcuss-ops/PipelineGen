// Package jobs — errors.go: canonical wiring-error sentinels for
// JobHandler.Register-style composition-time hooks.
//
// AGENTS.md godlike/05 (wiring-error rule): constructors and
// registration hooks MUST panic-or-return-error on missing MANDATORY
// dependencies. The P1 #1 audit pinned the typed-sentinel pattern
// here so every handler's Register* return the SAME errors.Is-detectable
// failure mode (just under different handler-specific prefixes).
//
// Usage pattern (from the 10+ handlers refactored in P1 #1 commit 2/2):
//
//	if jobsSvc == nil {
//	    return fmt.Errorf(
//	        "%s.Register: jobsSvc is nil (composition root must wire jobs.Service): %w",
//	        handlerTypeName,
//	        ErrMissingDeps,
//	    )
//	}
//
// The dual-message pattern preserves operator-visible diagnostics
// (the prefix names WHICH dep failed and WHICH constructor was
// invoked) while making the error uniformly detectable:
//
//	if errors.Is(err, jobs.ErrMissingDeps) {
//	    // typed assertion succeeds regardless of the handler prefix
//	}
//
// Tests and the composition-root seam (internal/app/composition.go)
// use errors.Is rather than substring-matching on the message text
// so a future maintainer can rename the handler prefix without
// breaking the wiring contract.
package jobs

import "errors"

// ErrMissingDeps is the canonical typed sentinel that every
// handler.Register* method wraps via fmt.Errorf("...: %w", ErrMissingDeps).
// Composition root + tests assert via errors.Is(err, jobs.ErrMissingDeps)
// so a future refactor that swaps "info+continue" for "nil+continue"
// (silent-success class closed by audit-P0.2 cont. for voiceover
// handlers and audit-P1.1 for the remaining 9) trips the assertion
// immediately rather than silently dropping jobs onto an unsigned
// dispatcher.
//
// Per AGENTS.md godlike/07 NO_FAKE_AVAILABILITY: this sentinel is
// only emitted by handlers whose mandatory deps were missing at
// composition time. It is NOT emitted by runtime use-case failures
// (those use the typed sentinel chain at the use-case boundary).
//
// Forward-pointer: the sentinel is NOT yet covered by a dedicated
// CI gate. The P1 #1 commit adds scripts/ci-architectural-checks.sh
// Check 50 (forbid void Register* methods that take jobs.Service) so
// future maintainers see the typed-error shape required at compile
// time. The errors.Is() assertion surface is locked by per-handler
// tests that follow the spec's "Register with jobs.Service=nil -> err
// non-nil" + "Register with valid deps -> (nil, HasHandler=true)"
// pattern (see e.g. voiceover/jobs/generate_handler_test.go).
var ErrMissingDeps = errors.New("jobs: missing mandatory dependency for handler registration")

// ErrLeaseLost is the canonical typed sentinel for lease-mismatch
// failures (worker_id / lease_id / expected_revision do not match
// the canonical job row). Emitted by:
//
//   - internal/infrastructure/jobs/local.broker.ensureLease when
//     a HandleX command races a concurrent state transition (renewal,
//     cancellation, or terminal) and the revision has advanced.
//
//   - internal/infrastructure/remote/jobbrokerclient Client when the
//     server-side handler returns the typed-error envelope
//     `{kind:"lease_lost"}` (forward-prevention: the server-side
//     api/jobs handler emits the envelope in a follow-up PR; client-
//     side decode is forward-compatible today).
//
// Upstream callers benefit from errors.Is(err, jobs.ErrLeaseLost)
// over both in-process workers (*local.Broker) and remote workers
// (*jobbrokerclient.Client) without leaking the worker-execution
// boundary into the assertion surface.
//
// Per godlike/07 typed-error contract: callers wrapping the
// sentinel via `fmt.Errorf("...: %w", ErrLeaseLost)` preserve the
// errors.Is probe through the wrap chain — do not flatten to a
// string match.
var ErrLeaseLost = errors.New("jobs: lease lost (worker_id/lease_id/expected_revision mismatch with the canonical job row)")
