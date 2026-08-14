// Package search — universe_test.go pins the PR-SEARCH-UNIVERSE
// contract: RetrievalMode (how) is a separate axis from SearchUniverse
// (where), and BackendRegistry.Eligible routes by universe so catalog
// queries never hit live providers and discovery queries never touch
// Qdrant.
package search

import (
	"reflect"
	"sort"
	"testing"
)

// TestParseUniverse pins the canonical wire mapping: empty + unknown
// default to catalog (never silently widened to blended).
func TestParseUniverse(t *testing.T) {
	cases := []struct {
		in   string
		want SearchUniverse
	}{
		{"catalog", SearchCatalog},
		{"discovery", SearchDiscovery},
		{"blended", SearchBlended},
		{"", SearchCatalog},
		{"  ", SearchCatalog},
		{"CATALOG", SearchCatalog},
		{"Discovery", SearchDiscovery},
		{"BLENDED", SearchBlended},
		{"bogus", SearchCatalog},
	}
	for _, tc := range cases {
		if got := ParseUniverse(tc.in); got != tc.want {
			t.Errorf("ParseUniverse(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestIsValidUniverse pins the SSOT closed-set predicate used by
// transport code to reject a non-empty unknown value with 400.
func TestIsValidUniverse(t *testing.T) {
	for _, s := range []string{"catalog", "discovery", "blended", "CATALOG", "Blended"} {
		if !IsValidUniverse(s) {
			t.Errorf("IsValidUniverse(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "   ", "bogus", "catalog,discovery"} {
		if IsValidUniverse(s) {
			t.Errorf("IsValidUniverse(%q) = true, want false", s)
		}
	}
}

// TestQueryEffectiveUniverse pins the empty→catalog default.
func TestQueryEffectiveUniverse(t *testing.T) {
	if got := (Query{}).EffectiveUniverse(); got != SearchCatalog {
		t.Errorf("zero Query effective universe = %q, want catalog", got)
	}
	if got := (Query{Universe: SearchDiscovery}).EffectiveUniverse(); got != SearchDiscovery {
		t.Errorf("discovery Query effective universe = %q, want discovery", got)
	}
	if got := (Query{Universe: SearchBlended}).EffectiveUniverse(); got != SearchBlended {
		t.Errorf("blended Query effective universe = %q, want blended", got)
	}
}

// TestRetrievalModeSeparateFromSearchUniverse pins the naming
// separation: RetrievalMode (ANN/hybrid) is distinct from
// SearchUniverse, and the deprecated SearchMode aliases keep the same
// Go-level identity so existing callers compile unchanged.
func TestRetrievalModeSeparateFromSearchUniverse(t *testing.T) {
	if RetrievalModeANN != SearchModeANN {
		t.Errorf("SearchModeANN must alias RetrievalModeANN; got %q vs %q", SearchModeANN, RetrievalModeANN)
	}
	if RetrievalModeHybrid != SearchModeHybrid {
		t.Errorf("SearchModeHybrid must alias RetrievalModeHybrid; got %q vs %q", SearchModeHybrid, RetrievalModeHybrid)
	}
	if ParseMode("ann") != RetrievalModeANN {
		t.Errorf("ParseMode(ann) = %q, want RetrievalModeANN", ParseMode("ann"))
	}
	if ParseRetrievalMode("") != RetrievalModeHybrid {
		t.Errorf("ParseRetrievalMode(\"\") = %q, want hybrid default", ParseRetrievalMode(""))
	}
}

// TestEligibleFiltersByUniverse pins the routing invariant:
//   - catalog   → only catalog backends (no live provider call)
//   - discovery → only discovery backends (no Qdrant call)
//   - blended   → both
//   - empty     → catalog (canonical default)
func TestEligibleFiltersByUniverse(t *testing.T) {
	reg := NewBackendRegistry()
	for _, b := range []*fakeBackend{
		{name: "semantic", universe: SearchCatalog, caps: []Capability{CapVideo}},
		{name: "local", universe: SearchCatalog, caps: []Capability{CapVideo}},
		{name: "youtube", universe: SearchDiscovery, caps: []Capability{CapVideo}},
		{name: "artlist", universe: SearchDiscovery, caps: []Capability{CapVideo}},
	} {
		if err := reg.Register(b); err != nil {
			t.Fatalf("register %s: %v", b.name, err)
		}
	}
	reg.Freeze()

	cases := []struct {
		name     string
		q        Query
		want     []string
	}{
		{"empty_defaults_to_catalog", Query{}, []string{"local", "semantic"}},
		{"catalog", Query{Universe: SearchCatalog}, []string{"local", "semantic"}},
		{"discovery", Query{Universe: SearchDiscovery}, []string{"artlist", "youtube"}},
		{"blended", Query{Universe: SearchBlended}, []string{"artlist", "local", "semantic", "youtube"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := backendNames(reg.Eligible(tc.q))
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Eligible(%+v) = %v, want %v", tc.q, got, want)
			}
		})
	}
}

// TestEligibleDiscoveryNeverIncludesSemantic pins that a discovery
// query with an explicit source filter still excludes the semantic
// backend (the "always include semantic" special case is overridden
// by the universe filter — a discovery search must not touch Qdrant).
func TestEligibleDiscoveryNeverIncludesSemantic(t *testing.T) {
	reg := NewBackendRegistry()
	for _, b := range []*fakeBackend{
		{name: "semantic", universe: SearchCatalog, caps: []Capability{CapVideo}},
		{name: "youtube", universe: SearchDiscovery, caps: []Capability{CapVideo}},
	} {
		if err := reg.Register(b); err != nil {
			t.Fatalf("register %s: %v", b.name, err)
		}
	}
	reg.Freeze()

	got := backendNames(reg.Eligible(Query{Sources: []string{"youtube"}, Universe: SearchDiscovery}))
	if !reflect.DeepEqual(got, []string{"youtube"}) {
		t.Fatalf("discovery + sources=[youtube] = %v, want [youtube] (semantic must be excluded)", got)
	}
}
