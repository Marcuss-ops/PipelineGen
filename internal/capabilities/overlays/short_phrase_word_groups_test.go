package overlays

import (
	"sort"
	"testing"
)

// TestShortPhraseWordGroupsAreGeneratedFromCatalog re-derives the routing table
// from the checked-in ChrononTemplate catalog. The table is the only place a
// generated short phrase learns which recipes its word count may use, so a
// catalog change (or a new retirement) that is not mirrored here must fail the
// tests instead of silently rerouting production.
func TestShortPhraseWordGroupsAreGeneratedFromCatalog(t *testing.T) {
	universe := loadCanonicalMotionUniverse(t)
	want := map[int][]string{}
	for id, wordCount := range universe.shortWordCount {
		if IsRemovedGeneratedPhraseMotion(id) {
			continue
		}
		if wordCount < 1 || wordCount > 5 {
			continue
		}
		want[wordCount] = append(want[wordCount], id)
	}
	if len(shortPhraseRecipeWordGroups) != 5 {
		t.Fatalf("routing table has %d word groups, want exactly 1..5", len(shortPhraseRecipeWordGroups))
	}
	for wordCount := 1; wordCount <= 5; wordCount++ {
		got := append([]string(nil), shortPhraseRecipeWordGroups[wordCount]...)
		expected := append([]string(nil), want[wordCount]...)
		sort.Strings(got)
		sort.Strings(expected)
		if len(got) != len(expected) {
			t.Fatalf("word group %d routes %d recipes, the catalog derives %d\ngot:  %v\nwant: %v",
				wordCount, len(got), len(expected), got, expected)
		}
		for i := range got {
			if got[i] != expected[i] {
				t.Fatalf("word group %d drifted from the catalog\ngot:  %v\nwant: %v", wordCount, got, expected)
			}
		}
	}
	for wordCount, ids := range shortPhraseRecipeWordGroups {
		for _, id := range ids {
			if IsRemovedGeneratedPhraseMotion(id) {
				t.Errorf("retired recipe %q is still routed by word group %d", id, wordCount)
			}
			if !IsShortPhraseRecipe(id) {
				t.Errorf("word group %d carries non-recipe id %q", wordCount, id)
			}
		}
	}
}
