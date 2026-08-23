// Package transport — registry_retry_classifier.go (FASE 6 Cut 6.1.B2, July 2026).
//
// Distributed Qdrant Classifier. The retry package (pkg/retry) cannot
// import internal/ adapters (Go's pkg→internal visibility rule), so each
// internal adapter MUST register its Classifier from a file INSIDE its
// own package — this file. pkg/retry walks the registered chain on every
// Decision(err) call; first-match-wins, so the order in which adapters
// classifier order matters (Qdrant here → HTTP/SDK
// upstream taxonomies in the stdlib registry).
//
// Qdrant-specific surface:
//
//   - *APIError (typed status-code + retryability envelope) → the
//     canonical per-call decision is preserved verbatim via the
//     APIError.Retryable field (which is computed at parse time
//     from HTTP status + operation kind). The Classifier here maps
//     HTTP status → pkg/retry.ErrorCategory (5xx → ErrNetwork, 4xx
//     → ErrValidation), preserving the typed-only contract.
//
//   - The 3 retryable sentinels (ErrCollectionNotFound, ErrAliasNotFound,
//     ErrAliasSwitchNotReady) all implement IsRetryable() bool → true
//     per godlike/06 SSOT. Each is operator-fixable (a pending schema
//     init / an alias add). The Classifier here surfaces them with a
//     typed envelope so audit logs grep the SafeMessage field.
//
// godlike/06 SSOT: this is the ONLY Qdrant Classifier. Do not register
// another Qdrant classifier from elsewhere — the chain is first-match-wins
// and a second Qdrant classifier would shadow the canonical one.
//
// godlike/07 fail-closed: errors with no qdrant-shape claim + no other
// registered classifier + no typed-RetryableError + no
// TransientInfrastructureError carrier return (zero, false) at the
// retry.Decision level — the walker does NOT silently retry unmapped
// shapes.
package transport

import (
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// RetryClassifierAPIError is the canonical Qdrant *APIError
// typed-error Classifier. It is exported so the application composition
// root can assemble it into a ClassifierRegistry and inject it via
// retry.Options. pkg/retry cannot import internal/ packages, so the
// classifier cannot be registered from inside pkg/retry.
var RetryClassifierAPIError = classifyQdrantAPIError

// RetryClassifierSentinel is the canonical Qdrant retryable-sentinel
// Classifier. It is exported so the application composition root can
// assemble it into a ClassifierRegistry and inject it via
// retry.Options.
var RetryClassifierSentinel = classifyQdrantRetryableSentinel

// classifyQdrantAPIError maps *APIError to RetryDecision. The
// APIError.Retryable field is PRESERVED verbatim (no heuristic
// re-derivation); the Class field is mapped from HTTP status to the
// canonical pkg/retry.ErrorCategory so audit logs can grep by category
// without re-parsing status codes.
//
// Returns (zero, false) when err is not a *APIError carrier — pass
// to the next classifier in the chain.
//
// The SafeMessage intentionally surfaces Operation + Status +
// a 60-char truncation of Message so audit logs are grep-friendly
// without leaking the full error body (some Qdrant responses are
// MB-scale; the SafeMessage bounds audit-log cardinality per
// godlike/07 no-fake-availability).
func classifyQdrantAPIError(err error) (retry.RetryDecision, bool) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return retry.RetryDecision{}, false
	}
	if apiErr == nil {
		return retry.RetryDecision{}, false
	}
	message := truncateAuditMessage(apiErr.Message, 60)
	return retry.RetryDecision{
		Class:       qdrantClassFromStatus(apiErr.Status),
		Retryable:   apiErr.Retryable,
		SafeMessage: fmt.Sprintf("qdrant: %s HTTP %d %s", apiErr.Operation, apiErr.Status, message),
	}, true
}

// classifyQdrantRetryableSentinel maps the explicit retryable-sentinel
// errors (ErrCollectionNotFound, ErrAliasNotFound, ErrAliasSwitchNotReady)
// to RetryDecision. Each of these implements IsRetryable() bool → true
// (godlike/06 SSOT: typed-probe Identifier at construction time).
//
// The Classifier here surfaces them with a typed envelope so audit
// logs can grep the SafeMessage field, distinct from generic transient
// shapes (different operators surface different remediation steps:
// "run reindex-qdrant" for ErrReindexRequired → not here; "add the
// missing alias" for ErrAliasNotFound → here).
//
// Returns (zero, false) when err is not one of the 3 typed sentinels —
// pass to the next classifier in the chain.
//
// FASE 6 Cut 6.1 review fix (July 2026): the original implementation
// used `switch err.(type)` to detect the 3 sentinels. That pattern is
// a top-level type assertion and DOES NOT walk the unwrap chain — a
// caller wrapping the sentinel via fmt.Errorf("%w", &ErrAliasNotFound{})
// would silently bypass this classifier. Replaced with three
// errors.As probes matching the canonical pattern used by
// classifyQdrantAPIError in this same file. errors.As walks the
// unwrap chain via the canonical Go errors-tree contract; wrapped
// sentinels are detected authoritatively.
func classifyQdrantRetryableSentinel(err error) (retry.RetryDecision, bool) {
	if err == nil {
		return retry.RetryDecision{}, false
	}
	if errors.As(err, new(*ErrCollectionNotFound)) {
		return retry.RetryDecision{
			Class:       retry.ErrLockBusy,
			Retryable:   true,
			SafeMessage: "qdrant: typed-retryable-sentinel *ErrCollectionNotFound",
		}, true
	}
	if errors.As(err, new(*ErrAliasNotFound)) {
		return retry.RetryDecision{
			Class:       retry.ErrLockBusy,
			Retryable:   true,
			SafeMessage: "qdrant: typed-retryable-sentinel *ErrAliasNotFound",
		}, true
	}
	if errors.As(err, new(*ErrAliasSwitchNotReady)) {
		return retry.RetryDecision{
			Class:       retry.ErrLockBusy,
			Retryable:   true,
			SafeMessage: "qdrant: typed-retryable-sentinel *ErrAliasSwitchNotReady",
		}, true
	}
	return retry.RetryDecision{}, false
}

// qdrantClassFromStatus maps the canonical HTTP status to the
// canonical pkg/retry.ErrorCategory (network/timeout/lock_busy/
// validation/missing_handler/bad_payload/unknown). Conservative:
//
//   - 5xx                  → ErrNetwork (transient infra-class)
//   - 4xx                  → ErrValidation (terminal client-class)
//   - 0 (network/IO fail)  → ErrNetwork
//   - anything else        → ErrUnknown (conservative fail-closed)
//
// The Retryable field on APIError is authoritative; this mapping only
// affects the Class field which audit dashboards use to category-partition
// retry-storm-vs-permission-denied visualizations.
func qdrantClassFromStatus(status int) retry.ErrorCategory {
	switch {
	case status == 0:
		// Network/IO fail upstream of Qdrant (status never reached):
		// classified as transient network class.
		return retry.ErrNetwork
	case status >= 500:
		return retry.ErrNetwork
	case status >= 400:
		return retry.ErrValidation
	default:
		return retry.ErrUnknown
	}
}

// truncateAuditMessage bounds the Body slice surfaced in the
// SafeMessage. Audit logs that grep by SafeMessage must not be
// unbounded; the 60-char truncation emits "... (truncated N chars)"
// when the original exceeds the budget. Operators can recover the
// full text from logs.Original (a separate offline stream) when full
// diagnosis is required.
func truncateAuditMessage(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen < 12 {
		return s[:maxLen] + "..."
	}
	return s[:maxLen-12] + "... (truncated)"
}
