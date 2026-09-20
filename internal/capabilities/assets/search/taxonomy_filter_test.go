// Package search — taxonomy_filter_test.go pins the server-side taxonomy
// enforcement: a filters.asset_kind / filters.semantic_role selection must hold
// for EVERY candidate, so a caller no longer has to re-filter by asset-id
// prefix (the jq workaround this replaces).
package search

import (
	"context"
	"testing"
)

func TestFilterByTaxonomyNoConstraintPassesThrough(t *testing.T) {
	items := []Candidate{
		{AssetID: "yt_1", Source: "youtube", AssetKind: ""},
		{AssetID: "planner:1", Source: "youtube", AssetKind: "stock_video", SemanticRole: "stock"},
	}
	if got := FilterByTaxonomy(items, Query{}); len(got) != 2 {
		t.Fatalf("no constraint must keep every item, got %d", len(got))
	}
}

func TestFilterByTaxonomyTable(t *testing.T) {
	items := []Candidate{
		{AssetID: "yt-native", Source: "youtube", AssetKind: "", SemanticRole: ""},
		{AssetID: "yt-stock", Source: "youtube", AssetKind: "stock_video", SemanticRole: "stock"},
		{AssetID: "ar-clip", Source: "artlist", AssetKind: "broll", SemanticRole: "broll"},
		{AssetID: "unknown", Source: "youtube", AssetKind: "", SemanticRole: ""},
	}
	cases := []struct {
		name string
		q    Query
		want []string
	}{
		{
			name: "asset_kind selects the family",
			q:    Query{Filters: Filters{AssetKind: "stock_video"}},
			want: []string{"yt-stock"},
		},
		{
			name: "semantic_role selects the intent",
			q:    Query{Filters: Filters{SemanticRole: "broll"}},
			want: []string{"ar-clip"},
		},
		{
			name: "asset_kind and semantic_role compose with AND",
			q:    Query{Filters: Filters{AssetKind: "stock_video", SemanticRole: "broll"}},
			want: []string{},
		},
		{
			name: "case and surrounding whitespace are normalised",
			q:    Query{Filters: Filters{AssetKind: "  STOCK_VIDEO "}},
			want: []string{"yt-stock"},
		},
		{
			name: "unknown taxonomy is rejected (fail closed)",
			q:    Query{Filters: Filters{AssetKind: "never_declared"}},
			want: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FilterByTaxonomy(items, tc.q)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d items %v, want %d %v", len(got), ids(got), len(tc.want), tc.want)
			}
			for i, id := range tc.want {
				if got[i].AssetID != id {
					t.Fatalf("item %d = %q, want %q", i, got[i].AssetID, id)
				}
			}
		})
	}
}

func TestFilterByTaxonomyDoesNotMutateInput(t *testing.T) {
	items := []Candidate{{AssetID: "a", AssetKind: "stock_video"}, {AssetID: "b", AssetKind: "broll"}}
	_ = FilterByTaxonomy(items, Query{Filters: Filters{AssetKind: "stock_video"}})
	if len(items) != 2 {
		t.Fatalf("FilterByTaxonomy mutated its input: len=%d", len(items))
	}
}

// TestAggregatorEnforcesTaxonomyServerSide is the end-to-end pin: a caller can
// select YouTube-acquired stock without parsing the asset id, and the server —
// not the caller — guarantees the result set.
func TestAggregatorEnforcesTaxonomyServerSide(t *testing.T) {
	registry := NewBackendRegistry()
	backend := &stubBackend{
		name: "semantic", // always-eligible cross-source meta-backend
		caps: []Capability{CapVideo},
		items: []Candidate{
			{AssetID: "yt-native", Source: "youtube", SourceRef: "yt-native", Score: 0.9},
			{AssetID: "planner-1", Source: "youtube", AssetKind: "stock_video", SemanticRole: "stock", SourceRef: "planner-1", Score: 0.8},
		},
	}
	if err := registry.Register(backend); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	agg := NewAggregator(registry, nil)

	// Stock family requested: only the stock clip survives.
	res, err := agg.Search(context.Background(), Query{
		Sources: []string{"youtube"},
		Filters: Filters{AssetKind: "stock_video"},
		Limit:   50,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].AssetID != "planner-1" {
		t.Fatalf("taxonomy filter failed: got %v", ids(res.Items))
	}

	// Provenance only: both survive (the taxonomy axis was not constrained).
	res, err = agg.Search(context.Background(), Query{Sources: []string{"youtube"}, Limit: 50})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("provenance-only query must keep both, got %v", ids(res.Items))
	}
}
