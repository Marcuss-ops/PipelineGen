package overlays

import (
	"testing"
)

func TestResolveMapOverlapsShiftsPhraseAfterMap(t *testing.T) {
	items := []OverlayItem{
		{ID: "map1", Kind: "map", SceneID: "s1", StartUS: 81_000_000, DurationUS: 8_000_000, Map: &MapOverlay{}},
		{ID: "loc1", Kind: "location", SceneID: "s1", StartUS: 81_000_000, DurationUS: 1_000_000, Text: "Pavia"},
	}
	scenes := map[string][2]int64{"s1": {81_000_000, 95_000_000}}
	got := ResolveMapOverlaps(items, scenes, 24, 1)
	if len(got) != 2 {
		t.Fatalf("shifted phrase must survive, got %d items", len(got))
	}
	if got[1].StartUS != 89_000_000 {
		t.Fatalf("phrase must start at map end, got %d", got[1].StartUS)
	}
	if got[1].DurationUS != 1_000_000 {
		t.Fatalf("duration preserved when room remains, got %d", got[1].DurationUS)
	}
	if err := AdmitOverlayTimeline(got, 24, 1); err != nil {
		t.Fatalf("resolved plan must pass admission, got %v", err)
	}
}

func TestResolveMapOverlapsDropsWhenNoRoom(t *testing.T) {
	items := []OverlayItem{
		{ID: "map1", Kind: "map", SceneID: "s1", StartUS: 81_000_000, DurationUS: 8_000_000, Map: &MapOverlay{}},
		{ID: "loc1", Kind: "location", SceneID: "s1", StartUS: 81_000_000, DurationUS: 1_000_000, Text: "Pavia"},
	}
	// Scene ends with the map: nothing fits after it.
	scenes := map[string][2]int64{"s1": {81_000_000, 89_000_000}}
	got := ResolveMapOverlaps(items, scenes, 24, 1)
	if len(got) != 1 || got[0].ID != "map1" {
		t.Fatalf("phrase without room must drop, map stays: %+v", got)
	}
}

func TestResolveMapOverlapsDropsAnchoredInsteadOfShifting(t *testing.T) {
	items := []OverlayItem{
		{ID: "map1", Kind: "map", SceneID: "s1", StartUS: 0, DurationUS: 8_000_000, Map: &MapOverlay{}},
		{ID: "b1", Kind: "text_phrase", SceneID: "s1", StartUS: 0, DurationUS: 4_000_000,
			Text: "Certified bullet", Params: map[string]any{"editorial_role": "scene_bullet"}},
	}
	scenes := map[string][2]int64{"s1": {0, 20_000_000}}
	got := ResolveMapOverlaps(items, scenes, 24, 1)
	if len(got) != 1 || got[0].ID != "map1" {
		t.Fatalf("certified bullet must drop rather than lose TTS anchoring: %+v", got)
	}
}

func TestResolveMapOverlapsLeavesCleanPlanUntouched(t *testing.T) {
	items := []OverlayItem{
		{ID: "map1", Kind: "map", SceneID: "s1", StartUS: 0, DurationUS: 8_000_000, Map: &MapOverlay{}},
		{ID: "p1", Kind: "text_phrase", SceneID: "s1", StartUS: 9_000_000, DurationUS: 2_000_000, Text: "Later"},
		{ID: "p2", Kind: "text_phrase", SceneID: "s1", StartUS: 2_000_000, DurationUS: 2_000_000, Text: "Phrase"},
	}
	scenes := map[string][2]int64{"s1": {0, 20_000_000}}
	got := ResolveMapOverlaps(items, scenes, 24, 1)
	// p1 is clear; p2 overlaps the map and shifts to 8s.
	if len(got) != 3 {
		t.Fatalf("expected shift, not drop: %+v", got)
	}
	byID := map[string]OverlayItem{}
	for _, it := range got {
		byID[it.ID] = it
	}
	if byID["p2"].StartUS != 8_000_000 {
		t.Fatalf("p2 must shift to map end, got %d", byID["p2"].StartUS)
	}
	if byID["p1"].StartUS != 9_000_000 {
		t.Fatalf("clear item untouched, got %d", byID["p1"].StartUS)
	}
}
