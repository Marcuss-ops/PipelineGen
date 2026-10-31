package overlays

import "testing"

func TestMoveEntityImageAsideFromOverlappingTimelineDate(t *testing.T) {
	items := []OverlayItem{
		{
			ID: "alberto", Kind: "entity_image", StartMs: 46_582, EndMs: 48_320,
			Params: map[string]any{"position_x": float64(0), "position_y": float64(0)},
		},
		{
			ID: "date", Kind: "number", TemplateID: "TIMELINE_DATE_CARD",
			StartMs: 44_033, EndMs: 50_033,
		},
	}

	moveImagesAsideForOverlappingDateCards(items)
	if got := items[0].Params["position_x"]; got != float64(-720) {
		t.Fatalf("entity image x position = %v, want -720px", got)
	}
	if got := items[0].Params["position_y"]; got != float64(0) {
		t.Fatalf("entity image y position = %v, want 0px", got)
	}
}
