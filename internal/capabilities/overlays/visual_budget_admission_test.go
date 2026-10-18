package overlays

import (
	"strings"
	"testing"
)

func TestRankedNonOverlappingPhraseLanesFillsShortAndLongTargets(t *testing.T) {
	items := []OverlayItem{
		{ID: "short-a", SceneID: "scene-a", Kind: "text_phrase", Text: "two words", StartUS: 0, DurationUS: 2_000_000},
		{ID: "short-overlap", SceneID: "scene-a", Kind: "text_phrase", Text: "three short words", StartUS: 1_000_000, DurationUS: 2_000_000},
		{ID: "short-b", SceneID: "scene-a", Kind: "text_phrase", Text: "four small short words", StartUS: 2_000_000, DurationUS: 2_000_000},
		{ID: "long-a", SceneID: "scene-b", Kind: "text_phrase", Text: "this is a grounded long phrase", StartUS: 0, DurationUS: 2_000_000},
		{ID: "long-b", SceneID: "scene-b", Kind: "text_phrase", Text: "another grounded long phrase in context", StartUS: 2_000_000, DurationUS: 2_000_000},
	}
	got := rankedUniqueOverlayPhraseLanes(items, 2, 2)
	if len(got) != 4 {
		t.Fatalf("selected phrase count = %d, want 4 (%v)", len(got), got)
	}
	shortCount, longCount := 0, 0
	for _, index := range got {
		words := len(strings.Fields(items[index].Text))
		if words <= 5 {
			shortCount++
		} else {
			longCount++
		}
	}
	if shortCount != 2 || longCount != 2 {
		t.Fatalf("phrase lanes = short:%d long:%d, want 2 each", shortCount, longCount)
	}
}
