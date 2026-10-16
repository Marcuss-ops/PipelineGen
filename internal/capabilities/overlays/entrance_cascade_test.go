package overlays

import (
	"testing"
)

func cascadeItem(kind, id string, startMs, endMs int64, enter any) OverlayItem {
	item := OverlayItem{
		ID: id, SceneID: "scene-1", Kind: kind,
		StartMs: startMs, EndMs: endMs,
	}
	if enter != nil {
		item.MotionParams = map[string]any{"enter_frames": enter}
	}
	return item
}

func cascadeEnter(t *testing.T, item OverlayItem) (int, bool) {
	t.Helper()
	if item.MotionParams == nil {
		return 0, false
	}
	return cascadeEnterFrames(item.MotionParams)
}

// The canonical crowded beat: a phrase, a portrait and a plain image share
// one anchor. The phrase keeps its snap, the portrait settles one step
// later, and items without an explicit entrance plus all certified windows
// stay untouched.
func TestCascadeClusteredEntrancesStaggersCoPresentSettles(t *testing.T) {
	items := []OverlayItem{
		cascadeItem("text_phrase", "phrase", 1000, 5000, 32),
		cascadeItem("entity_image", "portrait", 1000, 5000, 8),
		cascadeItem("image", "photo", 1000, 5000, nil),
		cascadeItem("number", "date", 1000, 5000, nil),
	}
	CascadeClusteredEntrances(items, 24, 1)
	if got, _ := cascadeEnter(t, items[0]); got != 32 {
		t.Fatalf("phrase enter = %d, want untouched 32", got)
	}
	if got, _ := cascadeEnter(t, items[1]); got != 32+12 {
		t.Fatalf("portrait enter = %d, want cascaded 44", got)
	}
	if _, present := cascadeEnter(t, items[2]); present {
		t.Fatalf("plain image gained an explicit enter it never authored")
	}
	if _, present := cascadeEnter(t, items[3]); present {
		t.Fatalf("number card gained an explicit enter it never authored")
	}
	for _, item := range items {
		if item.StartMs != 1000 || item.EndMs != 5000 {
			t.Fatalf("certified window moved for %q: %d-%d", item.ID, item.StartMs, item.EndMs)
		}
	}
}

// Priority decides the order inside one beat, not plan order: the phrase
// keeps rank zero even when listed after the portrait.
func TestCascadeClusteredEntrancesOrdersByPriority(t *testing.T) {
	items := []OverlayItem{
		cascadeItem("entity_image", "portrait", 1000, 5000, 8),
		cascadeItem("text_phrase", "phrase", 1000, 5000, 32),
	}
	CascadeClusteredEntrances(items, 24, 1)
	if got, _ := cascadeEnter(t, items[1]); got != 32 {
		t.Fatalf("phrase enter = %d, want untouched rank-zero 32", got)
	}
	if got, _ := cascadeEnter(t, items[0]); got != 44 {
		t.Fatalf("portrait enter = %d, want cascaded 44", got)
	}
}

// Beats farther apart than the epsilon read as intentional sequencing and
// keep their authored entrances.
func TestCascadeClusteredEntrancesSeparatesBeats(t *testing.T) {
	items := []OverlayItem{
		cascadeItem("text_phrase", "early", 1000, 3000, 24),
		cascadeItem("entity_image", "late", 3000, 6000, 8),
	}
	CascadeClusteredEntrances(items, 24, 1)
	if got, _ := cascadeEnter(t, items[0]); got != 24 {
		t.Fatalf("early phrase enter = %d, want 24", got)
	}
	if got, _ := cascadeEnter(t, items[1]); got != 8 {
		t.Fatalf("late portrait enter = %d, want 8", got)
	}
}

// The cascade never shortens, never exceeds the renderer ceiling or the
// item's own window, and converges instead of drifting on repeat passes.
func TestCascadeClusteredEntrancesNeverShortensAndConverges(t *testing.T) {
	items := []OverlayItem{
		cascadeItem("text_phrase", "phrase", 1000, 9000, 32),
		// Already settles later than the cascade target: keep it.
		cascadeItem("entity_image", "slow", 1000, 9000, 200),
		// Short window: the 44-frame target must clamp to the duration.
		cascadeItem("entity_image", "brief", 1000, 1500, 8),
	}
	CascadeClusteredEntrances(items, 24, 1)
	if got, _ := cascadeEnter(t, items[1]); got != 200 {
		t.Fatalf("slow portrait enter = %d, want kept 200", got)
	}
	// 500ms at 24fps is 12 frames: the brief portrait clamps there.
	if got, _ := cascadeEnter(t, items[2]); got != 12 {
		t.Fatalf("brief portrait enter = %d, want duration-clamped 12", got)
	}
	again := []OverlayItem{
		cascadeItem("text_phrase", "phrase", 1000, 9000, 32),
		cascadeItem("entity_image", "ranked", 1000, 9000, 8),
	}
	CascadeClusteredEntrances(again, 24, 1)
	first := again[1].MotionParams["enter_frames"]
	CascadeClusteredEntrances(again, 24, 1)
	if again[1].MotionParams["enter_frames"] != first {
		t.Fatalf("repeat pass drifted the entrance: %v vs %v", first, again[1].MotionParams["enter_frames"])
	}
}

// Composites, chains and maps own timing the cascade cannot see: layered
// portraits share one camera rig, chains carry fused segments, maps carry a
// camera_move timeline. All three stay untouched.
func TestCascadeClusteredEntrancesSkipsCompositeChainAndMap(t *testing.T) {
	composite := cascadeItem("entity_image", "group", 1000, 5000, nil)
	composite.ImageLayers = []OverlayImageLayer{{ID: "a"}, {ID: "b"}}
	items := []OverlayItem{
		cascadeItem("text_phrase", "phrase", 1000, 5000, 32),
		composite,
		cascadeItem(string(KindPhraseChain), "chain", 1000, 5000, 16),
		{ID: "map", SceneID: "scene-1", Kind: "map", StartMs: 1000, EndMs: 5000,
			MotionParams: map[string]any{"enter_frames": 8}},
	}
	CascadeClusteredEntrances(items, 24, 1)
	if len(items[1].ImageLayers) != 2 || items[1].MotionParams != nil {
		t.Fatalf("composite was shaped: %+v", items[1])
	}
	if got, _ := cascadeEnter(t, items[2]); got != 16 {
		t.Fatalf("chain enter = %d, want untouched 16", got)
	}
	if got, _ := cascadeEnter(t, items[3]); got != 8 {
		t.Fatalf("map enter = %d, want untouched 8", got)
	}
}

// JSON checkpointing decodes numbers as float64: the cascade must still read
// the authored entrance instead of treating it as absent.
func TestCascadeClusteredEntrancesReadsFloatEnters(t *testing.T) {
	items := []OverlayItem{
		cascadeItem("text_phrase", "phrase", 1000, 5000, float64(32)),
		cascadeItem("entity_image", "portrait", 1000, 5000, float64(8)),
	}
	CascadeClusteredEntrances(items, 24, 1)
	if got, _ := cascadeEnter(t, items[1]); got != 44 {
		t.Fatalf("portrait enter = %d, want cascaded 44", got)
	}
}
