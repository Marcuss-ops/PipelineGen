package overlays

import "testing"

// animation_counts.short_important_phrase / important_phrase are UI preview
// hints. Honoring them as runtime caps made a 25-phrase run use only 10 short
// and 20 long motions, repeating the same entrances for the remaining cards.
func TestPhraseLaneCountsArePreviewOnly(t *testing.T) {
	counts := map[string]int{"short_important_phrase": 10, "important_phrase": 20}
	if got := laneAnimationCount(counts, laneShortPhrase); got != 0 {
		t.Fatalf("short phrase lane count = %d, want 0 (preview-only)", got)
	}
	if got := laneAnimationCount(counts, laneImportantPhrase); got != 0 {
		t.Fatalf("important phrase lane count = %d, want 0 (preview-only)", got)
	}
	// Non-phrase lanes keep their documented window semantics.
	if got := laneAnimationCount(map[string]int{"dates": 4}, laneTimelineDate); got != 4 {
		t.Fatalf("date lane count = %d, want 4", got)
	}
}

func TestPhraseLaneRotationIgnoresPreviewCaps(t *testing.T) {
	counts := map[string]int{"short_important_phrase": 10, "important_phrase": 20}
	shortSeen := map[string]bool{}
	for ordinal := 0; ordinal < 25; ordinal++ {
		shortSeen[selectShortPhraseMotionLimited(
			"preview-cap", "short_phrase_group", ordinal, 3, nil,
			laneAnimationCount(counts, laneShortPhrase),
		)] = true
	}
	if len(shortSeen) <= 10 {
		t.Fatalf("short phrase rotation used only %d motions with preview cap 10: %v", len(shortSeen), shortSeen)
	}

	longSeen := map[string]bool{}
	for ordinal := 0; ordinal < 25; ordinal++ {
		longSeen[selectLongPhraseMotionLimited(
			"preview-cap", "run", ordinal, nil,
			laneAnimationCount(counts, laneImportantPhrase),
		)] = true
	}
	if len(longSeen) <= 20 {
		t.Fatalf("long phrase rotation used only %d motions with preview cap 20: %v", len(longSeen), longSeen)
	}
}
