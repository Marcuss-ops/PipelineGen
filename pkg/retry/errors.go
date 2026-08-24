// Package retry — errors.go (audit P1 #2, July 2026).
//
// Adds a typed ErrorCategory taxonomy + Classify helper that
// differentiates retryable infrastructure failures (Network/Timeout/
// LockBusy) from terminal domain failures (Validation/MissingHandler/
// BadPayload) for callers that want a richer shape than the binary
// "transient-or-not" provided by retry.IsTransient.
//
// Compatibility: Classify(err) is a SUPERSET of IsTransient — any err
// for which IsTransient returns true is classified as one of Network/
// Timeout/LockBusy with retryable=true; any err for which IsTransient
// returns false goes through the terminal-classifier pass and may map
// to Validation/MissingHandler/BadPayload or fall back to ErrUnknown
// (conservative retryable=false).
//
// Typical usage as the IsRetryable predicate in a retry loop:
//
//     err := retry.Do(ctx, fn, retry.Options{
//         IsRetryable: func(err error) bool {
//             _, retryable := retry.Classify(err)
//             return retryable
//         },
//     })
//
// Or binary-shaped via Retryable(err) (helper):
//
//     err := retry.Do(ctx, fn, retry.Options{
//         IsRetryable: retry.Retryable,
//     })
//
// HONEST LIMITATION (godlike/07):
//
//   - The user spec for this audit cited `internal/jobs/worker.go`
//     as the migration target for `time.Sleep → pkg/retry.Do`. The
//     file does NOT exist on disk — the actual retry sites on
//     origin/main are distributed across outbox pool
//     (internal/platform/sqlite/outboxevents/pool.go,
//     out of scope here — see forward-pointer below), monitor
//     scheduler (internal/application/assets/monitor/scheduler.go,
//     uses custom backoff math), and workerruntime preflight
//     (internal/app/workerruntime/preflight.go, ALREADY MIGRATED
//     to pkg/retry.Do per audit P1-3 with `BackoffFactor=1.0`
//     constant-poll pattern + ctx deadline short-circuit).
//   - The preflight migration is verified by inspecting
//     preflight.go::PreflightMasterHealth (line ~85-130): it
//     already routes through retry.Do with ctx.WithDeadline +
//     MaxAttempts=int(preflightTimeout/preflightInterval)+2 + the
//     canonical 1s sleep cadence. This P1 #2 commit does NOT
//     re-touch preflight.go.
//   - Forward-pointer (out of scope for this commit):
//     outboxevents/pool.go::Pool.computeNextAttempt + Pool's
//     jittered-backoff path can migrate to a typed-pkg/retry.Do
//     pattern in a follow-up PR, gated by a typed-transient
//     classifier for the outbox-specific error shapes (the
//     current substring classification lives in pool.go's
//     `computeNextAttempt` body).

package retry

import "strings"

// ErrorCategory labels the broad shape of a retry error so log
// surfaces can differentiate retryable infrastructure failures
// (Network/Timeout/LockBusy) from terminal domain failures
// (Validation/MissingHandler/BadPayload) without re-implementing
// the substring taxonomy in the caller.
//
// Values are lower-case strings (wire-friendly; no spaces); the
// empty-string ErrUnknown is the conservative fallback for
// unrecognised shapes (retryable=false → caller MUST NOT retry).
type ErrorCategory string

const (
	// ErrNetwork — connection refused / reset / eof / 429 / 503 / 504 /
	// rate-limit / quota-exceeded class. retryable=true.
	ErrNetwork ErrorCategory = "network"

	// ErrTimeout — i/o timeout / context deadline / up-stream deadline.
	// retryable=true.
	ErrTimeout ErrorCategory = "timeout"

	// ErrLockBusy — SQLite "database is locked" / sqlite busy /
	// file is locked (cross-process file lock contention).
	// retryable=true.
	ErrLockBusy ErrorCategory = "lock_busy"

	// ErrValidation — payload invalid / malformed / schema mismatch
	// at the typed layer. retryable=false (terminal: shape won't
	// change on retry).
	ErrValidation ErrorCategory = "validation"

	// ErrMissingHandler — job-type not registered / handler not
	// found / unbound dispatcher. retryable=false (terminal:
	// retrying doesn't change the registry state).
	ErrMissingHandler ErrorCategory = "missing_handler"

	// ErrBadPayload — payload parse failure / invalid JSON / byte
	// offset parse error. retryable=false (terminal: parsing
	// failures don't fix themselves).
	ErrBadPayload ErrorCategory = "bad_payload"

	// ErrUnknown — conservative fallback for unrecognised error
	// shapes. retryable=false (caller treats it as terminal until
	// taxonomy updates catch up).
	ErrUnknown ErrorCategory = "unknown"
)

// Classify returns the broad ErrorCategory for err and whether the
// retry loop should retry it. nil err returns (ErrUnknown, false).
//
// Triage order:
//
//  1. IsTransient(err) — typed path OR substring path; if true,
//     route the 15-entry taxonomy into one of {ErrLockBusy,
//     ErrTimeout, ErrNetwork} by best-effort substring match.
//     LockBusy is checked BEFORE Network/Timeout so "database is
//     locked" isn't misclassified as Network.
//  2. Domain-error shaper — match against canonical terminal
//     substrings: validation/invalid/malformed/schema-mismatch →
//     ErrValidation; not-registered/no-handler/handler-not-found/
//     unbound → ErrMissingHandler; bad-payload/payload-parse/
//     invalid-json → ErrBadPayload.
//  3. Unrecognised — fall back to (ErrUnknown, false). Conservative
//     goto-terminal is preferred over retrying an unknown shape
//     (godlike/07 no fake availability).
func Classify(err error) (ErrorCategory, bool) {
	if err == nil {
		return ErrUnknown, false
	}
	if IsTransient(err) {
		msg := strings.ToLower(err.Error())
		// LockBusy first to avoid "database is locked" → Network mis-class.
		switch {
		case strings.Contains(msg, "database is locked"),
			strings.Contains(msg, "sqlite busy"),
			strings.Contains(msg, "file is locked"):
			return ErrLockBusy, true
		case strings.Contains(msg, "timeout"),
			strings.Contains(msg, "context deadline"):
			return ErrTimeout, true
		default:
			return ErrNetwork, true
		}
	}
	msg := strings.ToLower(err.Error())
	// Order matters: ErrBadPayload is checked BEFORE ErrValidation so
	// payload-specific substrings ("payload parse", "invalid json") do
	// NOT match the generic ErrValidation substrings ("validation",
	// "malformed") first. ErrBadPayload has the more specific shape.
	switch {
	case strings.Contains(msg, "payload parse"),
		strings.Contains(msg, "invalid json"),
		strings.Contains(msg, "bad payload"),
		strings.Contains(msg, "payload invalid"):
		return ErrBadPayload, false
	case strings.Contains(msg, "validation"),
		strings.Contains(msg, "malformed"),
		strings.Contains(msg, "schema mismatch"):
		// NOTE: "invalid" intentionally NOT matched as a standalone
		// ErrValidation substring — it's too generic and overlaps with
		// auth-refresh / transient-corruption shapes ("invalid token",
		// "invalid checksum") that the retry loop is intended to retry
		// (or fail-fast at a higher layer than Classify). Conservative
		// scope per godlike/07 NO_FAKE_AVAILABILITY.
		return ErrValidation, false
	case strings.Contains(msg, "not registered"),
		strings.Contains(msg, "no handler"),
		strings.Contains(msg, "handler not found"),
		strings.Contains(msg, "unbound"):
		return ErrMissingHandler, false
	}
	return ErrUnknown, false
}

// Retryable is the binary form of Classify. Equivalent to
// `_, retryable := Classify(err); return retryable`. Nil err returns
// false (canonical consistent with IsTransient).
func Retryable(err error) bool {
	_, retryable := Classify(err)
	return retryable
}
