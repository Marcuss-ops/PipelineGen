package overlays

import (
	"testing"
)

// The exact-cardinality word groups are the contract payload policies and
// seeded group draws rely on: one to five words, no gaps, no overlaps.
func TestShortPhraseWordGroupsCoverOneToFive(t *testing.T) {
	for wordCount := 1; wordCount <= 5; wordCount++ {
		if len(ShortPhraseMotionsForWordCount(wordCount)) == 0 {
			t.Errorf("word group %d is empty", wordCount)
		}
	}
	if len(ShortPhraseMotionsForWordCount(0)) != 0 || len(ShortPhraseMotionsForWordCount(6)) != 0 {
		t.Error("word groups outside 1..5 must be empty")
	}
	seenRecipes := make(map[string]int)
	for wordCount := 1; wordCount <= 5; wordCount++ {
		for _, id := range ShortPhraseMotionsForWordCount(wordCount) {
			group, ok := ShortPhraseMotionWordCount(id)
			if !ok || group != wordCount {
				t.Errorf("motion %q sits in word group %d but reports group %d (known=%v)", id, wordCount, group, ok)
			}
			if IsShortPhraseRecipe(id) {
				if prev, dup := seenRecipes[id]; dup {
					t.Errorf("exact recipe %q shared by word groups %d and %d", id, prev, wordCount)
				}
				seenRecipes[id] = wordCount
			}
		}
	}
	if _, ok := ShortPhraseMotionWordCount("definitely_not_a_motion"); ok {
		t.Error("unknown motion must not report a word group")
	}
	for _, id := range retiredShortPhraseRecipes {
		if IsShortPhraseRecipe(id) {
			t.Errorf("retired ID %q is still classified as a short-phrase recipe", id)
		}
		if _, ok := ShortPhraseMotionWordCount(id); ok {
			t.Errorf("retired ID %q still has a routed word count", id)
		}
	}
	// The surviving C++ short-phrase burst recipe is pinned to its declared
	// three-word phrase like every other short recipe; short_phrase_typewriter
	// was retired with the typewriter family and is rejected above with the
	// other retired recipes.
	if group, ok := ShortPhraseMotionWordCount("short_phrase_burst"); !ok || group != 3 {
		t.Errorf("short_phrase_burst word group = %d (known=%v), want 3", group, ok)
	}
	if group, ok := ShortPhraseMotionWordCount("typewriter_modern_01_monospace_block_cursor"); ok {
		t.Errorf("typewriter_modern_01 must not be pinned to a word group, got group %d", group)
	}
	if IsShortPhraseRecipe("typewriter_modern_01_monospace_block_cursor") {
		t.Error("typewriter ids are general-catalog members, not exact recipes")
	}
	if !IsShortPhraseRecipe("short_phrase_character_tracking_reveal") {
		t.Error("a surviving short_phrase_ ID must mark an exact-cardinality recipe")
	}
	// Exact recipes must never leak into the general catalog the long and
	// regular lanes rotate over.
	for _, id := range CertifiedPhraseMotions() {
		if IsShortPhraseRecipe(id) {
			t.Errorf("exact recipe %q leaked into the general phrase catalog", id)
		}
	}
}
