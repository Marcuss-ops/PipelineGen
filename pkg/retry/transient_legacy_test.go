// Package retry — transient_legacy_test.go (FASE 6 Cut 6.1.D, July 2026).
//
// Test-only fixture that preserves the pre-FASE-6 substring taxonomy as
// a TEST-ONLY Classifier. Production code (pkg/retry/transient.go) has
// REMOVED the substring fallback per the FASE 6 user spec:
//
//	"Rimuovi TUTTA la classificazione substring (eof, 429, 502, 503, 504,
//	timeout) dal percorso di produzione di pkg/retry."
//
// Per godlike/07 (no-fake-availability) and godlike/06 (SSOT), the
// legacy surface is preserved verbatim here so tests can pin the
// pre-FASE-6 behavior. The taxonomy is byte-identical to the slice
// that lived in production transient.go prior to FASE 6 — same entry
// order, same strings.
//
// What lives here:
//
//  1. transientSubstringsLegacy — the pre-FASE-6 canonical taxonomy.
//     Declared as a const-style var so the canonical substring list
//     is observable in the test fixture (and so test failures on
//     "what did pre-FASE-6 classify as transient?" are reproducible).
//
//  2. classifyLegacyTransientForTest — test-side Classifier that walks
//     the substring taxonomy against err.Error() (mirrors the
//     pre-FASE-6 IsTransient substring loop). Tests call this
//     adapter to assert "before FASE 6 this error WOULD have been
//     transient" — useful for cataloging call-site migration
//     (godlike/07 no-fake-availability: legacy behavior is observable,
//     not silently lost).
//
// What does NOT live here:
//
//  - production IsTransient's substring path (REMOVED, see transient.go).
//  - production IsTransientString's matcher (stubbed to return false,
//    see transient.go for the deprecation contract).
//
// The fixture is ONLY included in test builds:
//   - filename ends in _test.go → Go test-build-filter excludes from
//     production binaries (production builds NEVER link this fixture).
//   - The fixture's Classifier is registered at init() in test scope
//     so transient_test.go + decision_test.go can use it via
//     ResetClassifiersForTest + RegisterClassifier(...) for
//     back-compat tests.
//
// ─────────────────────────────────────────────────────────────────────
// Future-migration helpers (visible to tests, not production)
// ─────────────────────────────────────────────────────────────────────

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
// registered via RegisterClassifier at init() — not in this fixture.
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
// import or call this function; tests use it via ResetClassifiersForTest
// + RegisterClassifier to verify "what did the pre-FASE-6 classifier
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
// This is NOT registered into the global classifier chain by default —
// tests that want to verify "pre-FASE-6 said transient" semantics
// register it manually:
//
//	func TestSomething_LegacyTransientSurface(t *testing.T) {
//	    t.Cleanup(retry.ResetClassifiersForTest)
//	    // first-class registrations from registry_stdlib.go + adapter
//	    // init() functions are wiped by ResetClassifiersForTest; the
//	    // test then registers the legacy classifier at the END of the
//	    // chain (last-classifier-wins-by-first-match, so legacy is the
//	    // LAST in-walk-fallback).
//	    retry.RegisterClassifier(retry.classifyLegacyTransientForTest)
//	    ...
//	}
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
