// Package retry — registry_google.go (FASE 6 Cut 6.1.B5, July 2026).
//
// Centralized Google API Classifier. The Google API SDK shape
// (google.golang.org/api/googleapi.Error) carries HTTP status + body
// + Retry-After header. The Drive SDK inherits this typed surface —
// every Drive SDK call returns *googleapi.Error on exit. Pre-FASE-6
// the retry surface classified these errors via substring matching,
// which drifted on Google API message-format changes. P1.5 (July 2026)
// introduced the typed *GoogleAPIError envelope with 6 Kind sentinels
// (Throttled/Server/Permission/NotFound/Client/Unknown); FASE 6 Cut
// 6.1.B5 wires the RAW SDK shape (*googleapi.Error) into the new
// init()-registered classifier chain so audit logs can grep by
// canonical ErrorCategory without re-parsing HTTP status.
//
// Why this file is CENTRALIZED in pkg/retry (vs DISTRIBUTED like the
// qdrant + sqlite classifiers): googleapi is an external public
// SDK with stable import path; pkg/retry can reliably import it. The
// internal/ adapter classifiers are distributed ONLY because Go's
// pkg→internal visibility rule prevents pkg/retry from importing
// internal/... packages. The OLD *GoogleAPIError typed-envelope
// also lives here (in google_api_error.go, P1.5); the new
// classifier chains into the same canonical classifyGoogleAPIErrorInfo
// helper for consistency — both raw SDK shape and pre-wrapped
// envelope share the same 6-Kind taxonomy.
//
// Shape-by-shape classifier contract:
//
//   classifyGoogleAPIError(*googleapi.Error):
//     - HTTP 429 (Too Many Requests) → RetryDecision{ErrNetwork,
//       Retryable: true, RetryAfter: parsed Retry-After header,
//       SafeMessage: "Google API rate limit (429)"}. The RetryAfter
//       field is honored by retry.DoWithValue via the RetryAfterError
//       interface (search upstream for the parser, expressed in
//       classifyGoogleAPIErrorInfo).
//     - HTTP 5xx + 408 (server error, request timeout) →
//       RetryDecision{ErrNetwork, Retryable: true}.
//     - HTTP 403 → RetryDecision{ErrValidation, Retryable: false}
//       (permission denied — retrying with the same principal does
//       not change the auth state).
//     - HTTP 404 → RetryDecision{ErrValidation, Retryable: false}
//       (resource gone; caller's only remediation is to recreate).
//     - HTTP 4xx-other → RetryDecision{ErrValidation, Retryable:
//       false} (request shape is wrong; retrying with the same
//       parameters re-emits the same 400/401/409).
//     - Anything else (including 0=network fail / off-spec status) →
//       RetryDecision{ErrUnknown, Retryable: false}. Conservative
//       fail-closed at the typed-probe boundary.
//
// The previously-wrapped *GoogleAPIError envelope falls through
// this Classifier (errors.As for *googleapi.Error returns false on
// a *GoogleAPIError-wrapped chain because *GoogleAPIError is a
// distinct type) and is processed by the typed-RetryableError
// probe in the Decision walker fallback (which honors
// *GoogleAPIError.IsRetryable()).
//
// godlike/06 SSOT: this is the ONLY Google API classifier. Do NOT
// register another googleapi classifier from elsewhere — the chain
// is first-match-wins and a second registration would shadow the
// canonical one.
//
// godlike/07 fail-closed: an unmatched status code returns (zero,
// false); the retry loop sees a non-retryable error. Permission
// and Client classes are TERMINAL by design — retrying a 403 with
// the same Service account does NOT change the auth state.

package retry

import (
	"errors"
	"fmt"

	"google.golang.org/api/googleapi"
)

// classifyGoogleAPIError is wired into the default ClassifierRegistry
// in decision.go at init time. The classifier handles the raw
// *googleapi.Error shape; the *GoogleAPIError envelope
// (already-wrapped) is handled by the typed-RetryableError probe in
// the walker fallback.

// classifyGoogleAPIError maps *googleapi.Error (the raw SDK exit
// shape from Google API/Drive SDK calls) to RetryDecision. The
// shape is the canonical Google API surface — Files.Create,
// Files.Get, docs.Documents.Create all return this type on exit.
// Returns (zero, false) when err is not a *googleapi.Error carrier
// — pass to the next classifier in the chain.
//
// Code coverage mirrors google_api_error.go::classifyGoogleAPIErrorInfo
// exactly: the SAME 6-Kind taxonomy is enforced so an envelope-wrapped
// *GoogleAPIError and a raw *googleapi.Error produce Equivalent
// audit-log SafeMessages.
//
// Retry-After parsing is delegated to the canonical
// classifyGoogleAPIErrorInfo helper. The RetryAfterDuration on the
// typed envelope (used by retry.DoWithValue) is computed there; this
// classifier just emits the duration on the RetryDecision.RetryAfter
// field for the canonical RetryDecision wire shape.
func classifyGoogleAPIError(err error) (RetryDecision, bool) {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return RetryDecision{}, false
	}
	if gerr == nil {
		return RetryDecision{}, false
	}
	kind, statusCode, retryAfter := classifyGoogleAPIErrorInfo(gerr)
	class := retryClassFromGoogleAPIKind(kind)
	// Body is truncated to 60 chars to bound audit-log cardinality
	// (godlike/07 no-fake-availability: audit logs that grep by
	// SafeMessage must not be unbounded).
	body := truncateAuditMessage(gerr.Body, 60)
	return RetryDecision{
		Class:       class,
		Retryable:   kind == ErrGoogleAPIThrottled || kind == ErrGoogleAPIServer,
		RetryAfter:  retryAfter,
		SafeMessage: fmt.Sprintf("googleapi: HTTP %d %s: %s", statusCode, kind.Error(), body),
	}, true
}

// retryClassFromGoogleAPIKind maps the canonical google_api_error.go
// 6-Kind sentinel set to the canonical pkg/retry.ErrorCategory for
// audit-log categorization. Mirrors the existing google_api_error.go
// behavior — a Kind+Class combo of the same retryability yields the
// same Class shape on both surfaces (raw SDK here, wrapped envelope
// in google_api_error.go).
//
//	Throttled + Server → ErrNetwork (transient infra-class)
//	Permission + NotFound + Client → ErrValidation (terminal
//	  client-class)
//	Unknown → ErrUnknown (conservative fail-closed)
func retryClassFromGoogleAPIKind(kind error) ErrorCategory {
	switch kind {
	case ErrGoogleAPIThrottled, ErrGoogleAPIServer:
		return ErrNetwork
	case ErrGoogleAPIPermission, ErrGoogleAPINotFound, ErrGoogleAPIClient:
		return ErrValidation
	}
	return ErrUnknown
}

// truncateAuditMessage bounds a string to maxLen chars for SafeMessage
// audit-log emission. Mirrors the helper in
// internal/infrastructure/qdrant/transport/registry_retry_classifier.go
// (same shape; pkg/retry cannot import internal/ so the helper is
// duplicated locally — the 12-char truncation sentinel is preserved
// canonically across both surfaces for consistent operator grep).
//
// Kept local here because pkg/retry may not import every internal
// adapter, and the canonical helper would create a cycle. The
// duplication is logged in this comment for anyone diffing both
// audit-log scanners (grep algorithms MUST tolerate slight wording
// drift between the two truncation labels — the canonical invariant
// is "max length" not "exact suffix text").
func truncateAuditMessage(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen < 12 {
		return s[:maxLen] + "..."
	}
	return s[:maxLen-12] + "... (truncated)"
}
