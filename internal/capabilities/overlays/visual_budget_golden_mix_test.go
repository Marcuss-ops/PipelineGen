package overlays

import (
	"fmt"
	"strings"
	"testing"
)

// TestGoldenThroughputMixBudgetAdmitsOneHundredItems pins the golden
// throughput admission (TICKET-OVERLAY-THROUGHPUT-100-MIN): with the
// payload's requested ceilings — 50 grounded phrases split 25 short + 25
// long, 30 images, 10 value cards and 10 grounded maps — the run-level
// editorial budget must admit all 100 materialized items. Before the fix the
// hard phrase limit (20), the map run ceiling (3) and the number ceiling (5)
// clamped the same candidate set to 36–41 items.
func TestGoldenThroughputMixBudgetAdmitsOneHundredItems(t *testing.T) {
	items := make([]OverlayItem, 0, 100)
	slot := 0
	next := func(item OverlayItem) OverlayItem {
		// Distinct non-overlapping word-aligned windows: phrase lane
		// admission drops same-lane overlaps.
		item.StartUS = int64(slot) * 10_000
		item.StartMs = item.StartUS / 1000
		item.DurationUS = 8_000
		item.EndMs = (item.StartUS + item.DurationUS + 999) / 1000
		slot++
		if item.Params == nil {
			item.Params = map[string]any{}
		}
		item.Params["priority"] = float64(1000 - slot)
		return item
	}
	for i := 0; i < 25; i++ {
		items = append(items, next(OverlayItem{
			ID: fmt.Sprintf("short-%02d", i), SceneID: fmt.Sprintf("scene-%02d", i),
			Kind: "text_phrase", Text: fmt.Sprintf("short phrase %02d alpha", i),
		}))
	}
	for i := 0; i < 25; i++ {
		items = append(items, next(OverlayItem{
			ID: fmt.Sprintf("long-%02d", i), SceneID: fmt.Sprintf("scene-%02d", i),
			Kind: "text_phrase", Text: fmt.Sprintf("long phrase %02d alpha bravo charlie delta echo", i),
		}))
	}
	for i := 0; i < 30; i++ {
		items = append(items, next(OverlayItem{
			ID: fmt.Sprintf("image-%02d", i), SceneID: fmt.Sprintf("image-scene-%02d", i),
			Kind:      "entity_image",
			AssetRefs: []OverlayAssetRef{{AssetID: fmt.Sprintf("asset-%02d", i), SHA256: fmt.Sprintf("%02x", i) + strings.Repeat("a", 62)}},
		}))
	}
	for i := 0; i < 10; i++ {
		items = append(items, next(OverlayItem{
			ID: fmt.Sprintf("number-%02d", i), SceneID: fmt.Sprintf("number-scene-%02d", i),
			Kind: "number", Text: fmt.Sprintf("12 march 20%02d", i),
		}))
	}
	for i := 0; i < 10; i++ {
		items = append(items, next(OverlayItem{
			ID: fmt.Sprintf("map-%02d", i), SceneID: fmt.Sprintf("map-scene-%02d", i),
			Kind: "map", Map: &MapOverlay{Provider: "local", SourceID: fmt.Sprintf("plate-%02d", i)},
		}))
	}

	got, budget := ApplyEditorialOverlayBudgetWithBulletLane(items, 50, 30, 10, false, 25, 25, 0)

	if len(got) != 100 {
		t.Fatalf("materialized %d items, want the full 100-item golden mix", len(got))
	}
	counts := map[string]int{}
	short := 0
	for _, item := range got {
		counts[item.Kind]++
		if item.Kind == "text_phrase" && len(strings.Fields(item.Text)) <= 5 {
			short++
		}
	}
	if counts["text_phrase"] != 50 || short != 25 {
		t.Fatalf("phrases = %d (short %d), want 50 (25 short)", counts["text_phrase"], short)
	}
	if counts["entity_image"] != 30 {
		t.Fatalf("images = %d, want 30", counts["entity_image"])
	}
	if counts["number"] != 10 {
		t.Fatalf("numbers = %d, want 10", counts["number"])
	}
	if counts["map"] != 10 {
		t.Fatalf("maps = %d, want 10", counts["map"])
	}
	if budget != (PhraseOverlayBudget{Requested: 50, Materialized: 50}) {
		t.Fatalf("phrase budget = %+v, want requested/materialized 50", budget)
	}
}
