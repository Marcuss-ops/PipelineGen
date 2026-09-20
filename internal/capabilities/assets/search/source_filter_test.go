// Package search — source_filter_test.go pins the server-side source
// enforcement: a sources:["youtube"] / filters.source filter must hold for
// EVERY candidate, including ones produced by the always-eligible semantic
// meta-backend. Before this, callers had to re-filter with jq.
package search

import (
	"context"
	"testing"
)

func TestFilterBySourceNoConstraintPassesThrough(t *testing.T) {
	items := []Candidate{
		{AssetID: "a", Source: "youtube"},
		{AssetID: "b", Source: "artlist"},
	}
	got := FilterBySource(items, Query{})
	if len(got) != 2 {
		t.Fatalf("no constraint must keep every item, got %d", len(got))
	}
}

func TestFilterBySourceTable(t *testing.T) {
	items := []Candidate{
		{AssetID: "yt-1", Source: "youtube", SourceRef: "yt-1"},
		{AssetID: "ar-1", Source: "artlist", SourceRef: "ar-1"},
		{AssetID: "loc-1", Source: "local", SourceRef: "loc-1"},
		{AssetID: "sem-1", Source: "semantic", SourceRef: "sem-1"},
		{AssetID: "none-1", Source: "", SourceRef: "none-1"},
		{AssetID: "planner", Source: "planner", SourceRef: "planner"},
	}
	cases := []struct {
		name string
		q    Query
		want []string
	}{
		{
			name: "sources youtube keeps only youtube provenance",
			q:    Query{Sources: []string{"youtube"}},
			want: []string{"yt-1"},
		},
		{
			name: "yt alias canonicalises to youtube",
			q:    Query{Sources: []string{"yt"}},
			want: []string{"yt-1"},
		},
		{
			name: "filters.source alone filters provenance",
			q:    Query{Filters: Filters{Source: "artlist"}},
			want: []string{"ar-1"},
		},
		{
			name: "sources and filters.source compose with AND",
			q:    Query{Sources: []string{"youtube", "artlist"}, Filters: Filters{Source: "youtube"}},
			want: []string{"yt-1"},
		},
		{
			name: "empty candidate source is dropped when a filter is set",
			q:    Query{Sources: []string{"local"}},
			want: []string{"loc-1"},
		},
		{
			name: "unknown provenance never sneaks past a youtube filter",
			q:    Query{Sources: []string{"youtube"}},
			want: []string{"yt-1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FilterBySource(items, tc.q)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d items, want %d (%v)", len(got), len(tc.want), ids(got))
			}
			for i, id := range tc.want {
				if got[i].AssetID != id {
					t.Fatalf("item %d = %q, want %q (all=%v)", i, got[i].AssetID, id, ids(got))
				}
			}
		})
	}
}

func TestFilterBySourceDoesNotMutateInput(t *testing.T) {
	items := []Candidate{{AssetID: "a", Source: "youtube"}, {AssetID: "b", Source: "artlist"}}
	_ = FilterBySource(items, Query{Sources: []string{"youtube"}})
	if len(items) != 2 {
		t.Fatalf("FilterBySource mutated its input: len=%d", len(items))
	}
}

// TestAggregatorEnforcesSourceFilterServerSide is the end-to-end pin: a
// sources:["youtube"] query through the real Aggregator must not return
// non-youtube provenance, even though the semantic backend is always eligible.
func TestAggregatorEnforcesSourceFilterServerSide(t *testing.T) {
	registry := NewBackendRegistry()
	// Named "semantic" so BackendRegistry.Eligible keeps it for any source
	// list (it is the cross-source meta-backend).
	semantic := &stubBackend{
		name: "semantic",
		caps: []Capability{CapVideo},
		items: []Candidate{
			{AssetID: "yt-1", Source: "youtube", SourceRef: "yt-1", Score: 0.9},
			{AssetID: "planner-1", Source: "planner", SourceRef: "planner-1", Score: 0.8},
			{AssetID: "ar-1", Source: "artlist", SourceRef: "ar-1", Score: 0.7},
		},
	}
	if err := registry.Register(semantic); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()

	agg := NewAggregator(registry, nil)
	res, err := agg.Search(context.Background(), Query{Sources: []string{"youtube"}, Limit: 50})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].AssetID != "yt-1" {
		t.Fatalf("server-side source filter failed: got %v", ids(res.Items))
	}
}

func ids(items []Candidate) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.AssetID)
	}
	return out
}
