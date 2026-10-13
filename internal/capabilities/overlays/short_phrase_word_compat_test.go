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
	// Typewriter glyph animators are deliberately not dealt into the automatic
	// word groups: on the installed Vulkan lane they rendered blank clips, so
	// only exact-cardinality recipes rotate for generated short phrases. They
	// stay general-catalog members an explicit plan can still name.
	if group, ok := ShortPhraseMotionWordCount("typewriter_modern_01_monospace_block_cursor"); ok {
		t.Errorf("typewriter_modern_01 must not be pinned to a word group, got group %d", group)
	}
	if IsShortPhraseRecipe("typewriter_modern_01_monospace_block_cursor") {
		t.Error("typewriter ids are general-catalog members, not exact recipes")
	}
	if !IsShortPhraseRecipe("short_phrase_scale_settle_word") {
		t.Error("short_phrase_ prefix must mark an exact-cardinality recipe")
	}
	// Exact recipes must never leak into the general catalog the long and
	// regular lanes rotate over.
	for _, id := range CertifiedPhraseMotions() {
		if IsShortPhraseRecipe(id) {
			t.Errorf("exact recipe %q leaked into the general phrase catalog", id)
		}
	}
}
