package overlays

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func shortTailMapItem(id string, startUS, durationUS int64, pin capmodel.MapOverlayPin) capmodel.OverlayItem {
	item := capmodel.OverlayItem{
		ID: id, SceneID: "scene-b1", Kind: "map", TemplateID: "MAP",
		StartUS: startUS, DurationUS: durationUS,
		StartMs: startUS / 1000, EndMs: (startUS + durationUS + 999) / 1000,
		Map: &capmodel.MapOverlay{
			Provider: "local", SourceID: "plate-siena",
			Center: capmodel.MapCenter{Latitude: 43.3185536, Longitude: 11.3316533},
			Zoom:   6, Width: 5760, Height: 3240,
			Pins: []capmodel.MapOverlayPin{pin},
		},
		Params: map[string]any{
			"map_group":      map[string]any{"index": 1, "count": 2, "size": 1},
			"map_pin_timing": map[string]map[string]any{pin.ID: {"start_us": int64(0), "duration_us": durationUS}},
		},
	}
	return item
}

// TestMergeShortMapTailMovesPinOntoPreviousMap pins the Campo regression: the
// last map of a scene was clamped to 0.94 s by the next scene. The certified
// Siena plate covers Campo, so the pin is merged and the short item disappears.
func TestMergeShortMapTailMovesPinOntoPreviousMap(t *testing.T) {
	siena := capmodel.MapOverlayPin{ID: "ent_siena", Label: "Siena", Latitude: 43.3185, Longitude: 11.3316}
	campo := capmodel.MapOverlayPin{ID: "ent_campo", Label: "Campo", Latitude: 43.7109, Longitude: 10.4787}
	items := []capmodel.OverlayItem{
		shortTailMapItem("siena", 9_132_000, 3_295_100, siena),
		shortTailMapItem("campo", 12_427_100, 942_115, campo),
	}

	merged := MergeShortMapTail(items, 3, MapMinimumReadableUS, "seed")
	if len(merged) != 1 {
		t.Fatalf("merged items = %d, want 1", len(merged))
	}
	item := merged[0]
	if got := len(item.Map.Pins); got != 2 {
		t.Fatalf("pins = %d, want 2", got)
	}
	if item.DurationUS != 4_237_215 {
		t.Fatalf("duration = %d, want 4_237_215", item.DurationUS)
	}
	group, _ := item.Params["map_group"].(map[string]any)
	if group["size"] != 2 || group["count"] != 1 {
		t.Fatalf("map_group = %#v, want size 2 and count 1", group)
	}
	timing, _ := item.Params["map_pin_timing"].(map[string]map[string]any)
	if timing["ent_campo"]["start_us"] != int64(3_295_100) {
		t.Fatalf("campo pin start = %v, want 3295100", timing["ent_campo"]["start_us"])
	}
}

func TestMergeShortMapTailKeepsUncoveredPlaceStandalone(t *testing.T) {
	siena := capmodel.MapOverlayPin{ID: "ent_siena", Label: "Siena", Latitude: 43.3185, Longitude: 11.3316}
	// A place outside the six-zoom plate must never be pinned on an unrelated
	// basemap; it stays its own map even when its window is short.
	farAway := capmodel.MapOverlayPin{ID: "ent_far", Label: "Far", Latitude: -33.86, Longitude: 151.21}
	items := []capmodel.OverlayItem{
		shortTailMapItem("siena", 9_132_000, 3_295_100, siena),
		shortTailMapItem("far", 12_427_100, 942_115, farAway),
	}

	merged := MergeShortMapTail(items, 3, MapMinimumReadableUS, "seed")
	if len(merged) != 2 {
		t.Fatalf("merged items = %d, want 2 (uncovered place must stay standalone)", len(merged))
	}
}
