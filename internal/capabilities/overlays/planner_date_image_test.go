package overlays

import capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

import (
	"math"
	"testing"
)

func TestMoveEntityImageAndTimelineDateStayCenteredWhenOverlapping(t *testing.T) {
	items := []capmodel.OverlayItem{
		{
			ID: "alberto", Kind: "entity_image", StartMs: 46_582, EndMs: 48_320,
			Params: map[string]any{"position_x": float64(0), "position_y": float64(0)},
		},
		{
			ID: "date", Kind: "number", TemplateID: "TIMELINE_DATE_CARD",
			StartMs: 44_033, EndMs: 50_033,
			Params: map[string]any{"font_size_px": float64(140), "style": map[string]any{"font_size": float64(140)}},
		},
	}

	items[0].Params["width"] = float64(1305)
	MoveImagesAsideForOverlappingDateCards(items, 1920)
	if got := items[0].Params["position_x"].(float64); math.Abs(got) > 0.001 {
		t.Fatalf("entity image x position = %v, want centered at 0px", got)
	}
	if got := items[0].Params["position_y"].(float64); math.Abs(got-(-108)) > 0.001 {
		t.Fatalf("entity image y position = %v, want upper centered lane at -108px", got)
	}
	if got := items[1].Params["font_size_px"]; got != float64(96) {
		t.Fatalf("overlapping date font size = %v, want 96px", got)
	}
	if got := items[1].Params["position_x"]; got != float64(960) {
		t.Fatalf("overlapping date x position = %v, want canvas center at 960px", got)
	}
	if got := items[1].Params["position_y"]; got != float64(928.8) {
		t.Fatalf("overlapping date y position = %v, want lower centered lane at 928.8px", got)
	}
	if got := items[1].Params["style"].(map[string]any)["font_size"]; got != float64(96) {
		t.Fatalf("overlapping date style font size = %v, want 96px", got)
	}
}

func TestMoveEntityImageStaysInsideCanvasWhenImageExceedsCanvas(t *testing.T) {
	items := []capmodel.OverlayItem{
		{ID: "photo", Kind: "image", StartMs: 0, EndMs: 2000,
			Params: map[string]any{"width": float64(2400), "height": float64(900)}},
		{ID: "date", Kind: "number", TemplateID: "TIMELINE_DATE_CARD", StartMs: 1000, EndMs: 3000},
	}
	MoveImagesAsideForOverlappingDateCards(items, 1920)
	width := items[0].Params["width"].(float64)
	x := items[0].Params["position_x"].(float64)
	if left, right := x-width/2, x+width/2; left < -883.2 || right > 883.2 {
		t.Fatalf("clamped image bounds [%v,%v] violate the canvas safe area", left, right)
	}
}
