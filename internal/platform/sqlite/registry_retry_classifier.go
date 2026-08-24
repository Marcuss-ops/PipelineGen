// Package sqlite — registry_retry_classifier.go (FASE 6 Cut 6.1.B3, July 2026).
//
// Distributed SQLite Classifier. The retry package cannot import
// internal/ adapters (Go's pkg→internal visibility rule), so the
// SQLite typed-error Classifier registers from INSIDE the sqlite
// package — this file. pkg/retry walks the registered chain on every
// Decision(err) call; first-match-wins, so the order of registration
// matters (SQLite here → upstream callers' HTTP/SDK classifiers).
//
// SQLite-specific surface:
//
//   - mattn/go-sqlite3 *sqlite3.Error (typed result-code + carrier).
//     The Code FIELD is the canonical sqlite3 result-code typed
//     as SQLite ErrNo (a typed int); the carrier's Error() returns
//     the human-readable string.
//
//   - Map the canonical SQLITE_* codes (verified against this project's
//     pinned version of github.com/mattn/go-sqlite3 in go.mod, the
//     1.14.17 release exposes ErrBusy, ErrLocked, ErrFull, ErrIoErr,
//     ErrCorrupt, ErrSchema, ErrConstraint, ErrReadonly, ErrAuth as
//     named constants — note ErrIoErr has a capital 'E' here, a
//     difference from the snake-case-leaning ErrCorrupt/ErrSchema
//     family) to pkg/retry.ErrorCategory:
//
//     BUSY (5)        → ErrLockBusy, retryable=true (concurrent writer)
//     LOCKED (6)      → ErrLockBusy, retryable=true (cross-process file lock)
//     FULL (13)       → ErrUnknown, retryable=false (disk full — operator state)
//     IOERR (10)      → ErrUnknown, retryable=false (I/O fail disk-level — operator state)
//     CORRUPT (11)    → ErrValidation, retryable=false (page corruption — manual)
//     SCHEMA (17)     → ErrValidation, retryable=false (schema mismatch — manual)
//     CONSTRAINT (19) → ErrValidation, retryable=false (UNIQUE/CHECK violation — program bug)
//     READONLY (8)    → ErrValidation, retryable=false (write-to-readonly)
//     AUTH (23)       → ErrValidation, retryable=false (authorization fail)
//
//   - Other codes (MISMATCH, RANGE, etc. — sometimes renamed across
//     mattn/go-sqlite3 versions) are NOT claimed — the walker falls
//     back to typed-RetryableError / TransientInfrastructureError probes.
//     godlike/07 fail-closed: unmapped codes return (zero, false); a
//     retry loop sees a non-retryable error. If a pinned mattn version
//     in a downstream project exposes ErrMismatch / ErrRange etc. as
//     named constants, add a case row here following the same
//     ErrValidation + retryable=false convention.
//
// godlike/06 SSOT: this is the ONLY SQLite Classifier. Do not register
// another SQLite classifier elsewhere — first-match-wins would shadow
// the canonical one.
//
// godlike/07 fail-closed: an unmatched code returns (zero, false); the
// retry loop sees a non-retryable error. Program bugs (CONSTRAINT
// violations) are TERMINAL by design — retrying a UNIQUE-clash produces
// the same error on the next attempt.
package sqlite

import (
	"errors"
	"fmt"

	"github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// RetryClassifier is the canonical SQLite typed-error Classifier.
// It is exported so the application composition root can assemble it
// into a ClassifierRegistry and inject it via retry.Options. pkg/retry
// cannot import internal/ packages, so the classifier cannot be
// registered from inside pkg/retry.
var RetryClassifier = classifySQLiteError

// classifySQLiteError maps mattn/go-sqlite3's typed *sqlite3.Error
// carrier to RetryDecision. The Code FIELD (not method) is the
// canonical sqlite3 result-code — mattn/go-sqlite3 exposes `Code
// sqlite3.ErrNo` as a field on the value, so reading via `sqErr.Code`
// (without parentheses) is the canonical probe. Returns (zero, false)
// when errors.As fails — pass to the next classifier in the chain.
//
// Code coverage is conservative: only the codes this project pins
// in mattn/go-sqlite3 are mapped; unknown codes return (zero, false).
// The walker then probes the typed-RetryableError interface (which
// none of *sqlite3.Error implements today) and the
// TransientInfrastructureError carrier (which is how WrapTransient at
// the SQLite boundary surfaces transient shapes — call sites should
// WrapTransient at the error origin for retry-loop affordances).
func classifySQLiteError(err error) (retry.RetryDecision, bool) {
	var sqErr *sqlite3.Error
	if !errors.As(err, &sqErr) {
		return retry.RetryDecision{}, false
	}
	if sqErr == nil {
		return retry.RetryDecision{}, false
	}
	code := sqErr.Code
	switch code {
	case sqlite3.ErrBusy, sqlite3.ErrLocked:
		return retry.RetryDecision{
			Class:       retry.ErrLockBusy,
			Retryable:   true,
			SafeMessage: fmt.Sprintf("sqlite: SQLITE_%s (%s)", sqliteCodeName(code), sqErr.Error()),
		}, true
	case sqlite3.ErrFull, sqlite3.ErrIoErr:
		return retry.RetryDecision{
			Class:       retry.ErrUnknown,
			Retryable:   false,
			SafeMessage: fmt.Sprintf("sqlite: SQLITE_%s (operator-intervention required): %s", sqliteCodeName(code), sqErr.Error()),
		}, true
	case sqlite3.ErrCorrupt, sqlite3.ErrSchema, sqlite3.ErrConstraint,
		sqlite3.ErrReadonly, sqlite3.ErrAuth:
		return retry.RetryDecision{
			Class:       retry.ErrValidation,
			Retryable:   false,
			SafeMessage: fmt.Sprintf("sqlite: SQLITE_%s (program/data condition, retry will not change outcome): %s", sqliteCodeName(code), sqErr.Error()),
		}, true
	}
	return retry.RetryDecision{}, false
}

// sqliteCodeName maps the typed sqlite3.ErrNo code (mattn/go-sqlite3
// pins the named constants as a typed int, NOT a plain int — we use
// the typed ErrNo so the case constants don't need int-cast gymnastics)
// to its canonical SQLITE_<NAME> label for the SafeMessage. The
// mapping is the table documented in the package doc above
// (godlike/06 SSOT). Unknown codes fall back to a numeric label so
// the SafeMessage remains useful.
//
// The list is exhaustive for the codes this Classifier recognises;
// codes outside this list return (zero, false) from classifySQLiteError
// and never reach sqliteCodeName in production builds (the compiler
// would have flagged any dead-code path under typed-int comparison).
func sqliteCodeName(code sqlite3.ErrNo) string {
	switch code {
	case sqlite3.ErrBusy:
		return "BUSY"
	case sqlite3.ErrLocked:
		return "LOCKED"
	case sqlite3.ErrIoErr:
		return "IOERR"
	case sqlite3.ErrCorrupt:
		return "CORRUPT"
	case sqlite3.ErrFull:
		return "FULL"
	case sqlite3.ErrSchema:
		return "SCHEMA"
	case sqlite3.ErrConstraint:
		return "CONSTRAINT"
	case sqlite3.ErrReadonly:
		return "READONLY"
	case sqlite3.ErrAuth:
		return "AUTH"
	}
	return fmt.Sprintf("code=%d", code)
}
