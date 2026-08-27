package script

import (
	"strings"
	"testing"
)

func queryProfile() SegmentSemanticProfile {
	return SegmentSemanticProfile{
		SegmentID: "segment-001", TextHash: "hash-1",
		Topic:            "origine dei primi trattori",
		ImportantPhrases: []string{"John Froelich developed an early tractor"},
		Entities: []ExtractedEntity{
			{Value: "John Froelich", Type: "PERSON", Confidence: .99},
			{Value: "Iowa", Type: "PLACE", Confidence: .98},
			{Value: "1892", Type: "DATE", Confidence: .97},
		},
		Keywords:    []WeightedKeyword{{Value: "tractor", Confidence: 1}, {Value: "agriculture", Confidence: .8}},
		VisualTerms: []WeightedKeyword{{Value: "early gasoline tractor", Confidence: 1}, {Value: "vintage farm machinery", Confidence: .8}},
		Terms: []SemanticTerm{
			{Value: "John Froelich", Kind: TermKindSubject, Score: .99},
			{Value: "Iowa", Kind: TermKindContext, Score: .98},
			{Value: "1892", Kind: TermKindTemporal, Score: .97},
			{Value: "tractor", Kind: TermKindTechnology, Score: 1},
			{Value: "agriculture", Kind: TermKindContext, Score: .8},
			{Value: "early gasoline tractor", Kind: TermKindVisual, Score: 1},
		},
	}
}

func TestBuildYouTubeQueries_UsesEntitiesAndTemporalContext(t *testing.T) {
	queries := BuildYouTubeQueries(queryProfile(), 5)
	if len(queries) == 0 || !strings.Contains(strings.ToLower(queries[0]), "john froelich") {
		t.Fatalf("YouTube queries = %v, want entity-first query", queries)
	}
	joined := strings.ToLower(strings.Join(queries, " | "))
	if !strings.Contains(joined, "1892") || !strings.Contains(joined, "tractor") {
		t.Fatalf("YouTube queries = %v, want temporal and subject context", queries)
	}
}

func TestBuildArtlistQueries_IsVisualFirst(t *testing.T) {
	queries := BuildArtlistQueries(queryProfile(), 5)
	if len(queries) == 0 || queries[0] != "early gasoline tractor" {
		t.Fatalf("Artlist queries = %v, want visual term first", queries)
	}
	for _, query := range queries {
		if strings.Contains(strings.ToLower(query), "john froelich") {
			t.Fatalf("Artlist query %q unexpectedly prioritizes person entity", query)
		}
	}
}

func TestBuildImageQueries_IsEntityFirstAndIncludesDate(t *testing.T) {
	queries := BuildImageQueries(queryProfile(), 5)
	if len(queries) == 0 || !strings.Contains(strings.ToLower(queries[0]), "john froelich") {
		t.Fatalf("image queries = %v, want entity-first query", queries)
	}
	if !strings.Contains(strings.Join(queries, " | "), "1892") {
		t.Fatalf("image queries = %v, want temporal context", queries)
	}
}

func TestQueryBuilders_AreDeterministicAndDeduplicated(t *testing.T) {
	profile := queryProfile()
	profile.VisualTerms = append(profile.VisualTerms, WeightedKeyword{Value: " early   gasoline tractor ", Confidence: .5})
	for _, build := range []func(SegmentSemanticProfile, int) []string{
		BuildYouTubeQueries, BuildArtlistQueries, BuildImageQueries,
	} {
		first := build(profile, 5)
		second := build(profile, 5)
		if strings.Join(first, "|") != strings.Join(second, "|") {
			t.Fatalf("builder is not deterministic: %v != %v", first, second)
		}
		seen := map[string]bool{}
		for _, query := range first {
			key := strings.ToLower(query)
			if seen[key] {
				t.Fatalf("duplicate query %q in %v", query, first)
			}
			seen[key] = true
		}
	}
}

func TestQueryBuilders_RespectLimit(t *testing.T) {
	for _, build := range []func(SegmentSemanticProfile, int) []string{
		BuildYouTubeQueries, BuildArtlistQueries, BuildImageQueries,
	} {
		if got := build(queryProfile(), 1); len(got) > 1 {
			t.Fatalf("builder returned %d queries with limit 1: %v", len(got), got)
		}
	}
}
