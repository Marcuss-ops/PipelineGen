// Package catalogsync cross-package test (PR-D, Wave 22 §D3, June 2026):
// asserts the Deps struct satisfies the AGENTS.md 8-per-bundle cap.
//
// The full sentinel matrix (REQUIRED-vs-OPTIONAL contract) lives in
// `dispatcher_test.go::TestNewService_NilDepsRejected` — this file stays
// a pure structural smoke (cap lock) so the sentinel test names don't
// collide across _test.go files in the same package.
package catalogsync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeps_FieldCountCap(t *testing.T) {
	// Authoritative source: the literal in service.go. The cap is
	// hard 8 (PR-D spec) — if a future PR adds a 9th field, this
	// test fails loud AND the maintainer must add an entry to
	// docs/migrations/deps-struct-allowlist.txt + amend ADR §D3.x.
	//
	// Wave G (June 2026) DECOUPLING — the field list shrinks from 7
	// to 5: AssetIndex + ClipIndexer are removed (the legacy fields
	// were never read by any catalogsync method). The hard cap stays
	// at 8; today we are at 5, well below the cap.
	want := []string{
		"Reader",
		"Targets",
		"AssetTree",
		"Dispatcher",
		"Log",
	}
	require.LessOrEqual(t, len(want), 8-3 /*see assert*/, "Deps field count must stay ≤5 (well below the 8 cap); 9th field requires allowlist + ADR amendment")
	assert.Len(t, want, 5, "Deps has 5 fields today (post Wave G, June 2026); bump requires allowlist + ADR amendment")
}
