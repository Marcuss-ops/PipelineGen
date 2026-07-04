// Test fixture for Check 60 (ci-architectural-checks.sh).
//
// PR-PR6-TEST-REACTIVATE (Wave 1 P0 #3, CODE-QUALITY-CLEANUP-2026-07-04,
// deadline 2026-07-15) — forward-prevention gate that bans NEW
// t.Skip(...) markers in
// internal/application/scripts/adapters/processor_persistence_test.go
// that don't carry a godlike/07 honest-limitation comment.
//
// The fixture documents the regex surface that Check 60 MUST match:
//   1. t.Skip("reason")     — bare call, no comment → regex MUST match (fail)
//   2. t.Skipf("...%s", x)  — bare call, no comment → regex MUST match (fail)
//   3. t.SkipNow()          — bare call, no comment → regex MUST match (fail)
//
// And the exclusion surface (the honest-limitation comment pattern):
//   4. // godlike/07 honest-limitation: <reason>
//      t.Skip("reason")    — allowed, regex MUST NOT match (pass)
//
// godlike/07 no-fake-availability: t.Skip is a silent-success that hides
// regressions. The 2 t.Skip markers in processor_persistence_test.go were
// already removed in PR-PERSIST-PR6-CANONICAL (commit d17c78ae). This
// gate is the forward-prevention rule that locks the contract at pre-CI
// time so a future contributor cannot reintroduce the silent-success
// pattern.
//
// This file is intentionally placed under tests/fixtures/zero_legacy/ so
// the production rg scan never touches it (the production gate scopes to
// processor_persistence_test.go ONLY). The self-check mode scans this
// fixture to verify the regex still catches the bare-t.Skip patterns.
//
// NOT a production file — never compiled, never linked. The build excludes
// tests/fixtures/ via the standard go tooling (it's not under any Go
// package).

package fixtures

import "testing"

// badSkipWithNoComment must MATCH the Check 60 regex (fail in production).
// The fixture is the canonical "this pattern is forbidden" example.
func badSkipWithNoComment(t *testing.T) {
	// No godlike/07 honest-limitation comment above — the gate MUST flag this.
	t.Skip("Needs SQLite DB") //nolint
}

// badSkipfWithNoComment must MATCH the Check 60 regex (fail in production).
func badSkipfWithNoComment(t *testing.T) {
	// No godlike/07 honest-limitation comment above — the gate MUST flag this.
	t.Skipf("reason: %s", "missing fixture") //nolint
}

// badSkipNowWithNoComment must MATCH the Check 60 regex (fail in production).
func badSkipNowWithNoComment(t *testing.T) {
	// No godlike/07 honest-limitation comment above — the gate MUST flag this.
	t.SkipNow() //nolint
}

// goodSkipWithHonestLimitation must NOT MATCH the Check 60 regex (pass in production).
// The godlike/07 honest-limitation comment on the line immediately above
// the t.Skip call excludes the call from the failing-set.
func goodSkipWithHonestLimitation(t *testing.T) {
	// godlike/07 honest-limitation: skip when running hermetic without SQLite
	// (e.g. unit-test isolation; the production code uses the in-memory
	// idemFakeRepo path so this skip is reserved for legacy manual debug).
	t.Skip("Hermetic test isolation") //nolint
}
