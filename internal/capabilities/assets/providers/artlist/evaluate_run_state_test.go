// Package artlist — evaluate_run_state_test.go (Fase 3, July 2026)
//
// Pure-function table-driven tests for the canonical 4-rule state
// machine EvaluateRunState. The function has no I/O — only the
// per-bucket counts flow in and the verdict flows out. Tests pin
// the rule priority verbatim (the user-spec literal):
//
//	Rule 1: invariant_violated (Found != Processed+Skipped+Failed)
//	Rule 2: real_db_mismatch (Processed>0 ∧ RealPersisted==0)
//	Rule 3: zero work, all failures (Processed==0 ∧ Failed>0)
//	Rule 4: all work succeeded (Processed>0 ∧ Failed==0)
//	Rule 5: default (any other case) — PARTIAL_SUCCESS
//
// The user-spec test requirement ("una run con zero asset persistiti
// non può mai risultare SUCCEEDED") is pinned by:
//   - TestEvaluateRunState_ZeroAssetRunCannotSucceed (Processed>0 + RealPersisted==0)
//   - TestEvaluateRunState_ZeroProcessedAllFailed (Processed==0 + Failed>0)
//   - TestEvaluateRunState_AllZeroNoWorkDone (all-zero, falls to Rule 5)
package artlist

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEvaluateRunState_AllProcessedOk is the canonical SUCCEEDED case:
// Found == Processed + Skipped + Failed invariant holds; Processed > 0
// and Failed == 0 → SUCCEEDED, invariant_violated=false, no diagnostic.
func TestEvaluateRunState_AllProcessedOk(t *testing.T) {
	c := RunStatusCounts{Found: 5, Processed: 5, Skipped: 0, Failed: 0, RealPersisted: 5}
	status, iv, diag := EvaluateRunState(c)
	assert.Equal(t, RunStatusSucceeded, status)
	assert.False(t, iv)
	assert.Empty(t, diag)
}

// TestEvaluateRunState_ZeroProcessedAllFailed is the canonical FAILED case:
// Found == Processed + Skipped + Failed holds; Processed == 0 + Failed > 0
// → FAILED. The diagnostic must remain empty (no invariant violation).
func TestEvaluateRunState_ZeroProcessedAllFailed(t *testing.T) {
	c := RunStatusCounts{Found: 5, Processed: 0, Skipped: 0, Failed: 5, RealPersisted: 0}
	status, iv, diag := EvaluateRunState(c)
	assert.Equal(t, RunStatusFailed, status)
	assert.False(t, iv)
	assert.Empty(t, diag)
}

// TestEvaluateRunState_ZeroAssetRunCannotSucceed is the user-spec
// literal test: artlist_runs.processed_count > 0 (claim) but the real
// media_assets cross-check yields 0 rows. The state machine MUST
// reject SUCCEEDED and force PARTIAL_SUCCESS with the
// "real_db_mismatch" diagnostic. This is the godlike/07 fail-closed
// overlay that prevents the "artlist_runs lies about persisted
// assets" failure mode from returning SUCCEEDED.
func TestEvaluateRunState_ZeroAssetRunCannotSucceed(t *testing.T) {
	c := RunStatusCounts{
		Found:         10, // claim: 10 discovered
		Processed:     10, // claim: 10 processed
		Skipped:       0,
		Failed:        0,
		RealPersisted: 0, // godlike/07: real DB says ZERO assets actually persisted
	}
	status, iv, diag := EvaluateRunState(c)
	assert.Equal(t, RunStatusPartialSuccess, status,
		"zero-asset run MUST NOT be SUCCEEDED (user-spec literal: \"una run con zero asset persistiti non può mai risultare SUCCEEDED\")")
	assert.True(t, iv, "real_db_mismatch is an invariant violation per Rule 2")
	assert.Contains(t, diag, "real_db_mismatch",
		"diagnostic must name the failure mode verbatim so operators can grep for the canonical pattern")
	assert.Contains(t, diag, "processed_count=10",
		"diagnostic must surface the claimed count for operator triage")
}

// TestEvaluateRunState_MixedPartialSuccess is the canonical mixed
// case: Processed > 0 AND Failed > 0. Rule 4 does NOT fire (Failed > 0).
// Rule 3 does NOT fire (Processed > 0). The default Rule 5 returns
// PARTIAL_SUCCESS. Diagnostic must remain empty (no invariant
// violation, no real-DB mismatch).
func TestEvaluateRunState_MixedPartialSuccess(t *testing.T) {
	c := RunStatusCounts{Found: 8, Processed: 5, Skipped: 0, Failed: 3, RealPersisted: 5}
	status, iv, diag := EvaluateRunState(c)
	assert.Equal(t, RunStatusPartialSuccess, status)
	assert.False(t, iv)
	assert.Empty(t, diag)
}

// TestEvaluateRunState_InvariantViolation pins Rule 1 priority:
// Found != Processed + Skipped + Failed → PARTIAL_SUCCESS with
// InvariantViolated=true. The diagnostic must surface the
// undercounting pattern (4 silently lost items).
func TestEvaluateRunState_InvariantViolation(t *testing.T) {
	c := RunStatusCounts{
		Found:         10, // claimed: 10 discovered
		Processed:     5,  // tallied: 5 processed
		Skipped:       0,
		Failed:        0, // tallied: 0 failed
		RealPersisted: 5, // real DB matches the claim
		// 5 + 0 + 0 = 5 ≠ 10 → invariant violation
	}
	status, iv, diag := EvaluateRunState(c)
	assert.Equal(t, RunStatusPartialSuccess, status,
		"Found(10) != Processed(5) + Skipped(0) + Failed(0) is an invariant violation")
	assert.True(t, iv, "invariant_violated flag MUST be true")
	assert.Contains(t, diag, "invariant_violated",
		"diagnostic must name the failure mode verbatim")
	assert.Contains(t, diag, "Found(10)",
		"diagnostic must surface the found count")
	assert.Contains(t, diag, "Found(10) != Processed(5)",
		"diagnostic must surface the mismatch pattern for operator triage")
}

// TestEvaluateRunState_AllSkipped is a rare edge case: zero work, zero
// failures, all skipped. Rule 3 (Failed>0) does NOT fire. Rule 4
// (Processed>0) does NOT fire. The default Rule 5 returns
// PARTIAL_SUCCESS. The "all skipped" case is informative for
// operators triaging "why did nothing happen?" — could be a
// config issue, a scraper misfire, or an empty input.
func TestEvaluateRunState_AllSkipped(t *testing.T) {
	c := RunStatusCounts{Found: 0, Processed: 0, Skipped: 0, Failed: 0, RealPersisted: 0}
	status, iv, diag := EvaluateRunState(c)
	assert.Equal(t, RunStatusPartialSuccess, status,
		"all-zero counts fall to the default Rule 5 → PARTIAL_SUCCESS (no work, no failure, all-skipped is a degenerate case the state machine refuses to call SUCCEEDED)")
	assert.False(t, iv)
	assert.Empty(t, diag)
}

// TestEvaluateRunState_AllSkippedNonZero covers the canonical
// "Processed==0 + Failed==0 + Skipped>0" case which falls to Rule 5
// (default) → PARTIAL_SUCCESS. The diagnostic remains empty.
func TestEvaluateRunState_AllSkippedNonZero(t *testing.T) {
	c := RunStatusCounts{Found: 3, Processed: 0, Skipped: 3, Failed: 0, RealPersisted: 0}
	status, iv, diag := EvaluateRunState(c)
	assert.Equal(t, RunStatusPartialSuccess, status,
		"all-skipped case (zero work, zero failure) → PARTIAL_SUCCESS per Rule 5 default")
	assert.False(t, iv)
	assert.Empty(t, diag)
}

// TestEvaluateRunState_PartialSuccessWithFailure exercises the
// "Processed>0 + Failed>0 + Skipped>0" case where all three buckets
// are non-zero. The invariant holds (5+2+3=10). Rule 4 does NOT fire
// (Failed>0). Rule 5 (default) returns PARTIAL_SUCCESS.
func TestEvaluateRunState_PartialSuccessWithFailure(t *testing.T) {
	c := RunStatusCounts{Found: 10, Processed: 5, Skipped: 2, Failed: 3, RealPersisted: 5}
	status, iv, diag := EvaluateRunState(c)
	assert.Equal(t, RunStatusPartialSuccess, status)
	assert.False(t, iv)
	assert.Empty(t, diag)
}

// TestEvaluateRunState_RulePriorityInvariantFirst is the
// canary test for rule priority: when BOTH Rule 1 (invariant
// violation) AND Rule 2 (real-DB mismatch) would apply, Rule 1
// wins (it has higher priority per the function comment). This
// pins the priority order verbatim so future refactors that
// accidentally swap them are caught.
func TestEvaluateRunState_RulePriorityInvariantFirst(t *testing.T) {
	c := RunStatusCounts{
		Found:         5, // claim: 5 discovered
		Processed:     3, // claim: 3 processed
		Skipped:       0,
		Failed:        0,
		RealPersisted: 0, // also real-DB mismatch
		// 3 + 0 + 0 = 3 ≠ 5 → Rule 1 fires first
	}
	status, iv, diag := EvaluateRunState(c)
	assert.Equal(t, RunStatusPartialSuccess, status)
	assert.True(t, iv)
	assert.Contains(t, diag, "invariant_violated",
		"Rule 1 (invariant) MUST fire before Rule 2 (real-DB mismatch); "+
			"if Rule 2 fired first the diagnostic would mention real_db_mismatch instead")
}

// TestEvaluateRunState_StringAndIsTerminal is a structural test
// pinning the RunStatus helper methods (String / IsTerminal).
// The RunStatusResponse wire shape relies on these for the
// zap.Stringer field rendering + terminal-state gate.
func TestEvaluateRunState_StringAndIsTerminal(t *testing.T) {
	cases := []struct {
		s        RunStatus
		wantStr  string
		wantTerm bool
	}{
		// Declared constants: all 4 are terminal.
		{RunStatusSucceeded, "SUCCEEDED", true},
		{RunStatusFailed, "FAILED", true},
		{RunStatusPartialSuccess, "PARTIAL_SUCCESS", true},
		{RunStatusUnknown, "UNKNOWN", true},
		// Empty string: ad-hoc, non-terminal (defensive; should
		// never appear in production code).
		{RunStatus(""), "", false},
	}
	for _, tc := range cases {
		t.Run(string(tc.s), func(t *testing.T) {
			assert.Equal(t, tc.wantStr, tc.s.String())
			assert.Equal(t, tc.wantTerm, tc.s.IsTerminal())
		})
	}
}

// TestRunStatusCounts_InvariantHolds pins the helper method
// RunStatusCounts.InvariantHolds. The state machine function
// uses this internally; exposing it on the struct lets the
// handler surface the invariant boolean without re-doing the
// arithmetic.
func TestRunStatusCounts_InvariantHolds(t *testing.T) {
	cases := []struct {
		name string
		c    RunStatusCounts
		want bool
	}{
		{"all-zero", RunStatusCounts{Found: 0, Processed: 0, Skipped: 0, Failed: 0}, true},
		{"happy-path", RunStatusCounts{Found: 5, Processed: 5, Skipped: 0, Failed: 0}, true},
		{"mixed-with-skipped", RunStatusCounts{Found: 8, Processed: 5, Skipped: 2, Failed: 1}, true},
		{"missing-3-silent-loss", RunStatusCounts{Found: 5, Processed: 2, Skipped: 0, Failed: 0}, false},
		{"extra-1-overcount", RunStatusCounts{Found: 6, Processed: 5, Skipped: 0, Failed: 0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.c.InvariantHolds())
		})
	}
}
