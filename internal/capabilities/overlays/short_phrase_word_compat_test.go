package overlays

import (
	"testing"
)

// The exact-cardinality word groups are the contract payload policies and
// seeded group draws rely on: one to five words, no overlaps. A group may be
// empty only when the registry has no active renderer-backed recipe for that
// word count, as is currently the case for two-word recipes.
func TestShortPhraseWordGroupsCoverOneToFive(t *testing.T) {
	available := make(map[int]bool, 5)
	for _, entry := range canonicalAnimationRegistry.Entries() {
		if wordCount, ok := AnimationWordCount(entry.ID); ok {
			available[wordCount] = true
		}
	}
	for wordCount := 1; wordCount <= 5; wordCount++ {
		ids := ShortPhraseMotionsForWordCount(wordCount)
		if available[wordCount] && len(ids) == 0 {
			t.Errorf("word group %d is empty but the registry has an active recipe", wordCount)
		}
		if !available[wordCount] && len(ids) != 0 {
			t.Errorf("word group %d routes recipes but the registry has no active recipe", wordCount)
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
	if !IsShortPhraseRecipe("short_phrase_shape_phrase_wipe") {
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
