package scriptgeneration

import "testing"

func TestOverlayPlanCanvasCarriesAndValidatesEntityBadgeColor(t *testing.T) {
	canvas := GoldenOverlayCanvas
	canvas.EntityBadgeColor = "#336699"
	wire, err := overlayPlanCanvas(canvas)
	if err != nil {
		t.Fatalf("overlayPlanCanvas: %v", err)
	}
	if wire.EntityBadgeColor != "#336699" {
		t.Fatalf("entity_badge_color = %q, want #336699", wire.EntityBadgeColor)
	}

	canvas.EntityBadgeColor = "blue"
	if _, err := overlayPlanCanvas(canvas); err == nil {
		t.Fatal("invalid entity_badge_color must fail closed")
	}
}
