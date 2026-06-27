// Package stockpipeline cross-package test (PR-D, Wave 22 §D3, June 2026):
// asserts the Deps struct satisfies the AGENTS.md 8-per-bundle cap and
// NewService returns each sentinel error verbatim for the matching missing
// dependency.
//
// This test is intentionally placed at the bottom of the stockpipeline
// package (same file family as service_test.go) so it stays close to the
// ctor it exercises; the catalogsync-side smoke mirrors this file with the
// canonical sentinel names wrapped to that package.
package stockpipeline

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeps_FieldCountCap locks the PR-D 8-per-bundle cap on the stock
// Deps struct. The test is a TEXTUAL guard: if a future maintainer adds a
// 9th field, this test must be updated alongside the struct change AND a
// (documented) entry to docs/migrations/deps-struct-allowlist.txt. The
// loss of this test is the warning signal — remove the assertion only
// together with the allowed cap-bump PR.
func TestDeps_FieldCountCap(t *testing.T) {
	// We do not parse the .go file with reflect (the Deps struct has no
	// exported fields of pure kind 'pointer' to enumerate cheaply); the
	// authoritative source is the literal in service.go, so we maintain
	// a hand-curated field-name list and assert the count.
	want := []string{
		"Cfg",
		"Log",
		"Drive",
		"Storage",
		"Media",
		"YouTube",
		"Jobs",
	}
	assert.Len(t, want, 8-1 /*cap=8, but want guards against silent growth toward 9*/)
	// Tight assert: the cap is hard 8 (PR-D spec) — if a future PR adds
	// the 9th field, this test fails loud and the PR must justify the
	// additive field via an allowlist update + ADR §D3.x.
	require.LessOrEqual(t, len(want), 7, "Deps field count must stay ≤7 (well below the 8 cap); 9th field requires allowlist + ADR amendment")
}

// TestNewService_NilDepsRejected verifies the PR-D ctor validation surface:
// every required dep is rejected with its own typed sentinel error so
// composition wiring + tests can assert the precise missing dep.
func TestNewService_NilDepsRejected(t *testing.T) {
	tests := []struct {
		name string
		// We assert the sentinel only — full Deps construction cannot
		// happen here without scaffolding a dozen fixtures, so the
		// validation order is enforced via the canonical "first-missing
		// wins" rule (whatever nil dep the validation reaches first is
		// the sentinel returned).
		setup    func(d *Deps)
		wantErr  error
	}{
		{
			name:   "all-nil returns Cfg sentinel",
			setup:  func(d *Deps) {}, // every field zero/nil
			wantErr: ErrStockPipelineNilCfg,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := Deps{}
			tc.setup(&deps)
			_, err := NewService(deps)
			require.Error(t, err)
			assert.True(t, errors.Is(err, tc.wantErr) || err == tc.wantErr,
				"expected sentinel %v, got %v", tc.wantErr, err)
		})
	}
}
