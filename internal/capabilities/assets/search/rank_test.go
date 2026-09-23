package search

import (
	"testing"
	"time"
)

func TestRankForQuerySupportsDiscoverySorts(t *testing.T) {
	oldDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	items := []Candidate{
		{AssetID: "relevance", Score: 1, PublishedAt: &oldDate, DurationMs: 10_000, ViewCount: 1},
		{AssetID: "popularity", Score: 0.1, PublishedAt: &newDate, DurationMs: 20_000, ViewCount: 100},
		{AssetID: "unknown", Score: 0.9, DurationMs: 0, ViewCount: 1000},
	}
	cases := []struct{ mode, first string }{
		{"relevance", "relevance"},
		{"newest", "popularity"},
		{"oldest", "relevance"},
		{"longest", "popularity"},
		{"shortest", "relevance"},
		{"views", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			got := RankForQuery(items, Query{Filters: Filters{Sort: tc.mode}})
			if len(got) != len(items) || got[0].AssetID != tc.first {
				t.Fatalf("ranked=%+v, first want %q", got, tc.first)
			}
			if items[0].AssetID != "relevance" {
				t.Fatalf("ranking mutated input: %+v", items)
			}
		})
	}
}
