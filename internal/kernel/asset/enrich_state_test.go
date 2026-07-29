// Package asset — enrich_state_test.go is the canonical TDD coverage
// for the typed 4-state media_assets.enrich_state column
// (PR-ENRICHMENT-STATE-MACHINE, July 2026, migration 123).
//
// 5 contract tests pin the canonical 4-state closed set per
// godlike/06 SSOT (one owner per fact):
//  1. TestEnrichStateChromaticValues — every named constant carries
//     its documented wire string (PENDING/ENRICHING/ENRICHED/FAILED).
//  2. TestCanonicalEnrichStateValues — closed-set enumeration
//     returns exactly 4 values, in PENDING-first order.
//  3. TestIsValidEnrichStateExhaustive — Valid() accepts only the 4
//     canonical values + rejects empty + rejects unknown.
//  4. TestEnrichStateIsTerminal — IsTerminal() returns true for
//     ENRICHED + FAILED (the 2 terminal sinks).
//  5. TestEnrichStateIsScrapeCandidate — IsScrapeCandidate() returns
//     true for PENDING only (FAILED is terminal and must be reset to
//     PENDING by an admin before it can be picked up again).
package asset

import "testing"

func TestEnrichStateChromaticValues(t *testing.T) {
	cases := []struct {
		got  EnrichState
		want string
	}{
		{EnrichStatePending, "PENDING"},
		{EnrichStateEnriching, "ENRICHING"},
		{EnrichStateEnriched, "ENRICHED"},
		{EnrichStateFailed, "FAILED"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("chromatic mismatch: got %q, want %q", string(c.got), c.want)
		}
	}
}

func TestCanonicalEnrichStateValues(t *testing.T) {
	got := CanonicalEnrichStateValues()
	want := []EnrichState{
		EnrichStatePending,
		EnrichStateEnriching,
		EnrichStateEnriched,
		EnrichStateFailed,
	}
	if len(got) != len(want) {
		t.Fatalf("canonical set length: got %d, want %d", len(got), len(want))
	}
	for i, g := range got {
		if g != want[i] {
			t.Errorf("canonical set[%d]: got %q, want %q", i, g, want[i])
		}
	}
}

func TestIsValidEnrichStateExhaustive(t *testing.T) {
	accepts := []EnrichState{
		EnrichStatePending,
		EnrichStateEnriching,
		EnrichStateEnriched,
		EnrichStateFailed,
	}
	for _, s := range accepts {
		if !s.Valid() {
			t.Errorf("Valid() should accept %q", s)
		}
	}
	rejects := []EnrichState{
		"",
		EnrichState("pending"),  // lowercase typo
		EnrichState("enriched"), // lowercase typo
		EnrichState("UNKNOWN"),
		EnrichState("INDEXED"), // valid IndexState but not EnrichState
		EnrichState("DELETED"), // valid IndexState but not EnrichState
	}
	for _, s := range rejects {
		if s.Valid() {
			t.Errorf("Valid() should reject %q", s)
		}
	}
}

func TestEnrichStateIsTerminal(t *testing.T) {
	cases := []struct {
		state    EnrichState
		terminal bool
	}{
		{EnrichStatePending, false},
		{EnrichStateEnriching, false},
		{EnrichStateEnriched, true},
		{EnrichStateFailed, true},
	}
	for _, c := range cases {
		if got := c.state.IsTerminal(); got != c.terminal {
			t.Errorf("IsTerminal(%q): got %v, want %v", c.state, got, c.terminal)
		}
	}
}

func TestEnrichStateIsScrapeCandidate(t *testing.T) {
	cases := []struct {
		state     EnrichState
		candidate bool
	}{
		{EnrichStatePending, true},    // canonical scrape candidate
		{EnrichStateEnriching, false}, // claim held; not scrape-eligible
		{EnrichStateEnriched, false},  // terminal success; not scrape-eligible
		{EnrichStateFailed, false},    // terminal failure; requires admin reset to PENDING
	}
	for _, c := range cases {
		if got := c.state.IsScrapeCandidate(); got != c.candidate {
			t.Errorf("IsScrapeCandidate(%q): got %v, want %v", c.state, got, c.candidate)
		}
	}
}
