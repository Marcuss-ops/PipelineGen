// Package retry — transient_legacy_test.go
//
// Test-only fixture preserving the pre-FASE-6 substring taxonomy as a
// TEST-ONLY Classifier. Production IsTransient uses typed probes only;
// this file lets tests assert "pre-FASE-6 would have classified X".
// The taxonomy is byte-identical to the legacy production list.

package retry

import (
	"errors"
	"strings"
)

// transientSubstringsLegacy is the canonical pre-FASE-6 substring
// taxonomy. EXACT copy of the slice that lived in production
// transient.go prior to FASE 6 Cut 6.1.D. DO NOT update this list —
// updating it would silently shift the "pre-FASE-6 classifier would
// have said X" invariant that tests pin.
//
// If a future FASE appends a NEW substring to the pre-FASE-6 canonical
// list, the parallel production addition belongs in a typed Classifier
// assembled into a ClassifierRegistry at bootstrap — not in this fixture.
var transientSubstringsLegacy = []string{
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
	// Google API / gRPC canonical shapes (Step 7, July 2026).
	"userratelimitexceeded",
	"deadlineexceeded",
	"backenderror",
	"serviceunavailable",
	"quotaexceeded",
	"resource_exhausted",
}

// classifyLegacyTransientForTest is the test-only adapter that
// pre-FASE-6 IsTransient's substring loop. Production code MUST NOT
// import or call this function; tests build a local ClassifierRegistry
// and register it explicitly to verify "what did the pre-FASE-6 classifier
// say about this error?".
//
// Contract (byte-stable — DO NOT modify without a FASE-level SSOT migration):
//  1. nil err → (zero, false)
//  2. RetryableError interface OR *TransientInfrastructureError carrier →
//     pass-through to classifyLegacyTypedPath (the typed-path #1 / #2
//     components of pre-FASE-6 IsTransient).
//  3. err.Error() lowercased + substring against transientSubstringsLegacy
//     → match → RetryDecision{ErrNetwork, Retryable: true, SafeMessage:
//     "legacy substring classification"}. The SafeMessage here is
//     intentionally generic — production callers pre-FASE-6 used the
//     boolean alone; tests of the legacy surface should not depend on
//     the SafeMessage shape.
//  4. no match → (zero, false).
//
// This is NOT registered into the default classifier chain. Tests that
// want to verify "pre-FASE-6 said transient" semantics build a local
// ClassifierRegistry, register the built-in classifiers plus this
// fixture, seal it, and call registry.Decision(err).
func classifyLegacyTransientForTest(err error) (RetryDecision, bool) {
	if err == nil {
		return RetryDecision{}, false
	}
	// Typed path #1: RetryableError interface.
	var re RetryableError
	if errors.As(err, &re) && re.IsRetryable() {
		return classifyLegacyTypedPath(err)
	}
	// Typed path #2: TransientInfrastructureError carrier.
	var te *TransientInfrastructureError
	if errors.As(err, &te) {
		return classifyLegacyTypedPath(err)
	}
	// Substring fallback against the legacy taxonomy.
	lower := strings.ToLower(err.Error())
	for _, s := range transientSubstringsLegacy {
		if strings.Contains(lower, s) {
			return RetryDecision{
				Class:       ErrNetwork,
				Retryable:   true,
				SafeMessage: "legacy substring classification",
			}, true
		}
	}
	return RetryDecision{}, false
}

// classifyLegacyTypedPath emits a RetryDecision for typed-path errors
// without consulting the substring fallback (mirrors pre-FASE-6
// IsTransient's typed path 1 + 2). The Class is conservative
// (ErrNetwork) — classification would have been richer in production
// (the typed envelope itself is the canonical surface; production
// tests of the legacy surface should not depend on Class granularity).
func classifyLegacyTypedPath(err error) (RetryDecision, bool) {
	return RetryDecision{
		Class:       ErrNetwork,
		Retryable:   true,
		SafeMessage: "legacy typed-path classification",
	}, true
}
