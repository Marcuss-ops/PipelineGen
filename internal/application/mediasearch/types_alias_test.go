// Wave 21 PR 8 — type-alias identity regression test.
//
// Wave 19 cross-capability rule: a capability may import another
// capability only via canonical channel (shared port, typed event,
// composition-only bridge in internal/app). The search ↔ mediasearch
// alias pair crosses capabilities; the legal direction is
// mediasearch → search (this file's host package imports search).
//
// The reverse-direction test (`search_test.go` importing mediasearch)
// would violate Wave 19 and is therefore NOT shipped here.
//
// This file proves that mediasearch.MediaSearchFilter and
// mediasearch.SearchMode are the SAME Go type as search.Filters
// and search.SearchMode (Go-level aliases, not shape-compatible
// redeclarations). A drift to shape-compatible redeclaration would
// silently fork the wire shape and break /internal/v1/media/search
// byte-equivalence at PR 10 — the test fails loudly on any such drift.
package mediasearch

import (
	"reflect"
	"testing"

	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// TestTypeAliasesAreIdentity (Wave 21 PR 8).
func TestTypeAliasesAreIdentity(t *testing.T) {
	t.Run("Filters alias", func(t *testing.T) {
		want := reflect.TypeOf(search.Filters{})
		got := reflect.TypeOf(MediaSearchFilter{})
		if got != want {
			t.Fatalf("Filters alias broken:\n  got:  %v\n  want: %v", got, want)
		}
	})
	t.Run("SearchMode alias", func(t *testing.T) {
		want := reflect.TypeOf(search.SearchMode(""))
		got := reflect.TypeOf(SearchMode(""))
		if got != want {
			t.Fatalf("SearchMode alias broken:\n  got:  %v\n  want: %v", got, want)
		}
	})
	t.Run("Constants resolve through alias", func(t *testing.T) {
		// SearchModeANN is declared via the alias in this package;
		// the underlying value must equal search.SearchModeANN.
		if string(SearchModeANN) != string(search.SearchModeANN) {
			t.Fatalf("SearchModeANN mismatch: mediasearch=%q search=%q",
				string(SearchModeANN), string(search.SearchModeANN))
		}
		if string(SearchModeHybrid) != string(search.SearchModeHybrid) {
			t.Fatalf("SearchModeHybrid mismatch: mediasearch=%q search=%q",
				string(SearchModeHybrid), string(search.SearchModeHybrid))
		}
	})
}
