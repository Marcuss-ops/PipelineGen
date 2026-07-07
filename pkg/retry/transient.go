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
// (5) transientSubstrings taxonomy — canonical substring fallback.
//
//	timeouts, connection refused/reset/EOF, 429/502/503/504, rate limits,
//	quota-exceeded, temporarily-unavailable, database-locked, sqlite-busy,
//	plus Google API / gRPC canonical shapes (userratelimitexceeded,
//	deadlineexceeded, backenderror, serviceunavailable, quotaexceeded,
//	resource_exhausted).
//
//	These are the substring-path fallback. Where possible, prefer typed
//	wrapping (1+3) for new code; the substring path is retained as a
//	safety net for raw SDK errors not yet tagged at the typed layer.
//
// Anything that needs a transient-classifier MUST route through this
// file. CI gate (Check N, July 2026 audit) bans substring-match retry
// classifiers outside pkg/retry/transient.go (Step 7 consolidation:
// monitor.isTransientEnqueueError + tagutil.IsTransientDownloadError +
// youtube/usecase.IsTransientExtractionError now flow through here).

package retry

import (
	"errors"
	"strings"
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

// transientSubstrings is the canonical taxonomy of transient-infrastructure
// error substrings. Mirrors the taxonomy previously duplicated in
// monitor/enqueue.go::isTransientEnqueueError (removed Step 7, July 2026).
// Azione 4/8 (July 2026) added the SQLite-canonical markers below:
//
//   - "database is locked"   — SQLite busy marker (5.x: SQLITE_BUSY)
//   - "sqlite busy"          — mattn/go-sqlite3 prefix
//   - "connection is already closed" — sql.ErrConnDone.Error()
//
// NOTE on sqlassets.ErrStateConflict: it is a typed *logical* sentinel
// ("row state is in conflict"). The canonical contract today is that
// this error remains TERMINAL (callers explicitly force retryable=false
// after `errors.Is(err, sqlassets.ErrStateConflict)`). It is therefore
// not added to transientSubstrings — doing so would silently flip a
// terminal logical error to a transient infra error.
var transientSubstrings = []string{
	"timeout",
	"connection refused",
	"connection reset",
	"connection is already closed",
	"eof",
	"429",
	"503",
	"502",
	"504",
	"rate limit",
	"quota exceeded",
	"temporarily unavailable",
	"resource temporarily unavailable",
	"database is locked",
	"sqlite busy",
	// Google API / gRPC canonical shapes (Azione 8/8F di Step 7, July 2026).
	// Each entry is the lowercase no-space form; case-insensitive matching
	// against lowercased err.Error() accepts upstream camelCase / SNAKE_CASE
	// shapes (userRateLimitExceeded, deadlineExceeded, backendError,
	// serviceUnavailable, quotaExceeded, Resource_Exhausted).
	"userratelimitexceeded",
	"deadlineexceeded",
	"backenderror",
	"serviceunavailable",
	"quotaexceeded",
	"resource_exhausted",
}

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
//   - err is (or wraps) a *TransientInfrastructureError, OR
//   - err.Error() contains one of the canonical transient-infrastructure
//     substrings (timeout, connection refused, 429, 503, etc.).
//
// Decision order (typed wins over substring):
//  1. nil → false
//  2. RetryableError interface → IsRetryable() (typed authoritative path)
//  3. *TransientInfrastructureError via errors.As → true
//  4. Substring fallback against transientSubstrings
//  5. Everything else → false
//
// This is the single canonical "should I retry this?" predicate for
// the whole codebase. Callers that previously implemented their own
// substring matcher (monitor.isTransientEnqueueError,
// tagutil.IsTransientDownloadError, youtube/usecase.IsTransientExtractionError)
// should migrate to this function and, where typed wrapping is feasible,
// use WrapTransient.
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
	// Substring fallback.
	lower := strings.ToLower(err.Error())
	for _, s := range transientSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// IsTransientString is the string-only version of IsTransient. It performs
// the substring-fallback check against the canonical transientSubstrings
// taxonomy without requiring an error type. Useful when the caller already
// has an error message string (e.g., from an ExtractItem.Error field).
//
// Does NOT check RetryableError or TransientInfrastructureError — this
// is a pure substring matcher. For typed errors, use IsTransient.
func IsTransientString(s string) bool {
	lower := strings.ToLower(s)
	for _, token := range transientSubstrings {
		if strings.Contains(lower, token) {
			return true
		}
	}
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
