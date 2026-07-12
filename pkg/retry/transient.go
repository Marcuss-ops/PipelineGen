// Package retry — transient.go (PR-SPLIT-RETRY-PKG, July 2026).
//
// Transient-infrastructure error taxonomy + classification + typed
// wrapping. The 5 components in this file are the canonical
// "is this a transient failure, and how do I tell the rest of the
// codebase?" surface for the whole project.
//
// ═══════════ Components ═════════════════════════════════════════════════════
//
// (1) TransientInfrastructureError — typed carrier for transient errors.
//
//   - Marks an error as transient at a typed level (not via substring).
//   - errors.As(err, &TransientInfrastructureError{}) is the canonical
//     "is this transient?" probe: preferred over string-matching because
//     the typed path is robust to upstream message-format changes.
//   - Unwrap surfaces the inner error for errors.Is / errors.As chains.
//
// (2) IsTransient — canonical "should I retry this?" predicate.
//
//   - Returns true when err is non-nil AND either:
//     (a) err is or wraps a *TransientInfrastructureError (typed path), OR
//     (b) err.Error() contains one of the canonical transient-substrings
//     in (4) below (substring fallback).
//   - Pass it to retry.Do as the IsRetryable Option. This is the SINGLE
//     canonical retry-classifier for the whole codebase.
//
// (3) WrapTransient — canonical typed-wrapping helper at infra boundaries.
//
//   - Returns err wrapped in *TransientInfrastructureError when err matches
//     the canonical taxonomy; otherwise returns err unchanged.
//   - Idempotent: already-typed errors pass through (no double-wrap).
//   - Nil-safe: returns nil for nil.
//   - Use at SQLite / HTTP / SDK boundaries so the typed path reaches
//     retry.IsTransient authoritatively on farther-up retry loops.
//
// (4) RetryableError — structural interface for errors that carry their
//
//	own retryability classification. Qdrant's *APIError satisfies
//	this interface automatically (it has IsRetryable() bool).
//
// (5) transientSubstrings taxonomy — REMOVED FROM PRODUCTION per FASE 6
//	Cut 6.1.D (July 2026).
//
//	User spec: "Rimuovi TUTTA la classificazione substring (eof, 429,
//	502, 503, 504, timeout) dal percorso di produzione di pkg/retry."
//
//	The substring taxonomy previously lived in this file (transientSubstrings
//	var + IsTransient substring loop + IsTransientString pure-substring
//	helper). They are REMOVED from the production binary. The taxonomy is
//	preserved as a TEST-ONLY fixture in pkg/retry/transient_legacy_test.go
//	so tests can pin the pre-FASE-6 surface (godlike/07 no-fake-availability:
//	legacy behavior is observable in tests, not silently lost).
//
//	Production classifiers MUST register a typed Classifier via
//	decision.go::RegisterClassifier at init() — see decision.go for the
//	canonical walker + the stdlib + internal adapter registries.
//
// Anything that needs a transient-classifier MUST route through this
// file. CI gate (Check N, July 2026 audit) bans substring-match retry
// classifiers outside pkg/retry/transient.go (Step 7 consolidation:
// monitor.isTransientEnqueueError + tagutil.IsTransientDownloadError +
// youtube/usecase.IsTransientExtractionError now flow through here).

package retry

import (
	"errors"
)

// ── TransientInfrastructureError ────────────────────────────────────────────

// TransientInfrastructureError wraps an error to mark it as
// transient (retryable) at a typed level. Callers that know an
// error is infrastructure-level (e.g. SQLite locked, HTTP 503,
// DNS timeout) can wrap the original error with this type so
// downstream IsTransient predicates don't need substring matching.
//
// Usage:
//
//	if isSQLiteLocked(err) {
//	    return &retry.TransientInfrastructureError{Err: err}
//	}
//
// Better: use the in-package `WrapTransient(err)` helper which
// idempotently wraps based on the canonical taxonomy, so callers
// don't need their own ad-hoc predicate.
//
// The Unwrap method surfaces the inner error for errors.Is / errors.As.
type TransientInfrastructureError struct {
	Err error
}

func (e *TransientInfrastructureError) Error() string {
	if e.Err == nil {
		return "transient infrastructure error"
	}
	return e.Err.Error()
}

func (e *TransientInfrastructureError) Unwrap() error {
	return e.Err
}

// ── transient-substrings taxonomy + RetryableError interface ───────────────

// transientSubstrings is REMOVED from production per FASE 6 Cut 6.1.D
// (July 2026). The pre-FASE-6 taxonomy below is preserved verbatim in
// the TEST-ONLY fixture pkg/retry/transient_legacy_test.go so tests
// can pin the legacy fallback surface (godlike/07 no-fake-availability):
//
//	timeouts, connection refused/reset/EOF, 429/502/503/504, rate limits,
//	quota-exceeded, temporarily-unavailable, database-locked, sqlite-busy,
//	plus Google API / gRPC canonical shapes
//	(userratelimitexceeded, deadlineexceeded, backenderror,
//	serviceunavailable, quotaexceeded, resource_exhausted).
//
// Production classifiers MUST register a typed Classifier via
// decision.go::RegisterClassifier at init().
//
// NOTE on sqlassets.ErrStateConflict: it is a typed *logical* sentinel
// ("row state is in conflict"). The canonical contract today is that
// this error remains TERMINAL (callers explicitly force retryable=false
// after `errors.Is(err, sqlassets.ErrStateConflict)`). The classifier
// for sqlassets.ErrStateConflict (when added to the sqljobs package
// init()) MUST emit RetryDecision{ErrValidation, Retryable: false}.

// RetryableError is a structural interface for errors that carry their
// own retryability classification. Any error type with an IsRetryable()
// bool method satisfies this interface — no import of pkg/retry required.
//
// Example: Qdrant's *APIError has IsRetryable() bool (returns
// APIError.Retryable), so it satisfies RetryableError automatically.
// Image retrieval's ErrImageTransient can implement IsRetryable() bool
// { return true } to signal transient semantics without importing pkg/retry.
//
// Checked by IsTransient BEFORE the substring-fallback path so typed
// errors always win over heuristic matching.
type RetryableError interface {
	IsRetryable() bool
}

// ── IsTransient ─────────────────────────────────────────────────────────────

// IsTransient returns true when err is non-nil AND either:
//   - err implements RetryableError and IsRetryable() returns true, OR
//   - err is (or wraps) a *TransientInfrastructureError.
//
// FASE 6 Cut 6.1.D (July 2026): production IsTransient is a PURE TYPED
// PROBE. The pre-FASE-6 substring fallback was REMOVED from production
// per the user spec ("Rimuovi TUTTA la classificazione substring dal
// percorso di produzione"). The substring taxonomy is preserved in the
// TEST-ONLY fixture pkg/retry/transient_legacy_test.go for backwards-
// compat test fixtures; production classifiers MUST register a typed
// Classifier via decision.go::RegisterClassifier at init().
//
// Decision order (typed probe, no substring):
//  1. nil → false
//  2. RetryableError interface → IsRetryable() (typed authoritative path)
//  3. *TransientInfrastructureError via errors.As → true
//  4. Everything else → false (conservative fail-closed terminal)
//
// This function remains the CANONICAL pure-typed-probe "should I retry
// this?" predicate in production. Callers that want a richer shape
// (Class + RetryAfter + SafeMessage) should use retry.Decision
// (decision.go) which walks the registered Classifier chain.
//
// The classic migration path for raw SDK errors not yet wrapped is
// to call retry.WrapTransient(err) at the call site — if the err
// carries a typed RetryableError implementation (most modern SDKs
// provide one), WrapTransient wraps it in *TransientInfrastructureError
// and IsTransient returns true on the next iteration of the retry loop.
// Errors that carry NO typed marker AND NO substring match in the
// pre-FASE-6 surface are now treated as TERMINAL — register a typed
// Classifier for the adapter's error shape.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	// Typed path #1: RetryableError interface (authoritative).
	var re RetryableError
	if errors.As(err, &re) && re.IsRetryable() {
		return true
	}
	// Typed path #2: TransientInfrastructureError carrier.
	var te *TransientInfrastructureError
	if errors.As(err, &te) {
		return true
	}
	// FASE 6 Cut 6.1.D: production IsTransient is a pure typed probe.
	// The pre-FASE-6 substring fallback against transientSubstrings was
	// removed from production per the user spec. The substring taxonomy
	// lives in pkg/retry/transient_legacy_test.go as a TEST-ONLY helper
	// for tests that pin the legacy surface; production callers MUST
	// register a typed Classifier (decision.go::RegisterClassifier)
	// for any custom error shape.
	return false
}

// IsTransientString is DEPRECATED as of FASE 6 Cut 6.1.D (July 2026).
//
// Always returns false. The pre-FASE-6 substring fallback against
// transientSubstrings was removed from production per the user spec;
// the substring taxonomy is preserved verbatim in the TEST-ONLY
// fixture pkg/retry/transient_legacy_test.go.
//
// The function REMAINS exported for backward compat with the 1 known
// external caller (internal/application/youtube/jobs/classify.go).
// Production callers MUST migrate to one of:
//   - retry.IsTransient(err) on a *typed* error (RetryableError /
//     TransientInfrastructureError carrier)
//   - retry.Decision(err) for a richer Class + RetryAfter + SafeMessage
//   - retry.WrapTransient(err) at the SDK boundary so the typed path
//     reaches retry.IsTransient on farther-up retry loops
//
// The deprecation is an explicit "0 returns true" stop-gap so callers
// expecting the legacy substring match surface as a "transient? bool"
// see "false" instead of silently flipping to terminal. Forward-pointer
// for migration: registered typed Classifiers for the adapter-specific
// shapes (filesystemNotify, GoSubtitlesParser, etc.).
//
// Returns false unconditionally so the production contract is
// observably terminal for any unmapped raw-string input.
func IsTransientString(s string) bool {
	_ = s
	// FASE 6 Cut 6.1.D: removed substring matcher (returns false
	// always). Use retry.Decision / IsTransient(err) on a typed
	// error shape; use retry.WrapTransient at the SDK boundary to
	// label raw SDK errors as transient at the typed layer.
	return false
}

// ── WrapTransient ───────────────────────────────────────────────────────────

// WrapTransient returns err wrapped in *TransientInfrastructureError
// when err is already transient (per IsTransient), otherwise returns
// err unchanged. Idempotency: if err is already or wraps a
// *TransientInfrastructureError, returns err unchanged (no double
// wrap). Nil-safe: returns nil for nil input.
//
// Typical usage at SQLite / HTTP / DB boundary:
//
//	if err := db.Exec(...); err != nil {
//	    return retry.WrapTransient(err)  // typed-transient only if transient
//	}
//
// Pair with IsTransient for retry control flow:
//
//	err = retry.WrapTransient(rawErr)
//	if retry.IsTransient(err) { ... }
//
// WrapTransient is the canonical migration target for ad-hoc inline
// substring matchers at infra boundaries (Azione 4/8 di Step 7).
func WrapTransient(err error) error {
	if err == nil {
		return nil
	}
	var te *TransientInfrastructureError
	if errors.As(err, &te) {
		return err
	}
	if IsTransient(err) {
		return &TransientInfrastructureError{Err: err}
	}
	return err
}
