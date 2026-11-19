package scriptgeneration

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestPayloadTypographyReachesDateAndMetricCards pins the data-card half of
// the run-level overlay_style contract. Dates and metrics are rendered by
// RenderingGen through the same number lane as phrases, so the payload family
// and size must land on both Params and Style without touching the seeded
// motion or the item window. The planner's own presentation pass intentionally
// keeps its default only for fields the payload did not choose.
func TestPayloadTypographyReachesDateAndMetricCards(t *testing.T) {
	fontSize := 92.0
	style := &scriptpkg.OverlayStyleSpec{
		FontFamily: "oswald",
		Size:       &scriptpkg.OverlaySizeSpec{FontSize: &fontSize},
	}
	items := []capmodel.OverlayItem{
		{
			ID: "date", SceneID: "scene-1", Kind: "number", TemplateID: "TIMELINE_DATE_CARD",
			Text: "3 giugno 2007", MotionID: "date_year_count",
			Params: map[string]any{"font_family": "playfair_display_italic", "font_size_px": 140.0},
			Style:  map[string]any{"font_family": "playfair_display_italic", "font_size": 140.0},
		},
		{
			ID: "metric", SceneID: "scene-1", Kind: "number", TemplateID: "METRIC_STAT_CARD",
			Text: "42 per cento", MotionID: "metric_digits_stagger",
			Params: map[string]any{"font_family": "playfair_display_italic", "font_size_px": 140.0},
			Style:  map[string]any{"font_family": "playfair_display_italic", "font_size": 140.0},
		},
	}

	applyPayloadOverlayStyleToItems(items, OverlayCanvasSpec{Style: style})

	for _, item := range items {
		if got := item.Params["font_family"]; got != "oswald" {
			t.Errorf("%s params font = %v, want the payload oswald", item.ID, got)
		}
		if got := item.Style["font_family"]; got != "oswald" {
			t.Errorf("%s style font = %v, want the payload oswald", item.ID, got)
		}
		if got := item.Params["font_size_px"]; got != fontSize {
			t.Errorf("%s params size = %v, want %v", item.ID, got, fontSize)
		}
		if got := item.Style["font_size"]; got != fontSize {
			t.Errorf("%s style size = %v, want %v", item.ID, got, fontSize)
		}
	}
	if items[0].MotionID != "date_year_count" || items[1].MotionID != "metric_digits_stagger" {
		t.Fatalf("payload typography changed the selected motions: %q / %q", items[0].MotionID, items[1].MotionID)
	}
}
