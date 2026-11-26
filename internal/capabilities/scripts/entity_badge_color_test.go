package scriptgeneration

import "testing"

func TestOverlayPlanCanvasFillsTheCertifiedImageMotionPool(t *testing.T) {
	canvas := GoldenOverlayCanvas
	canvas.ImageMotions = nil
	wire, err := overlayPlanCanvas(canvas)
	if err != nil {
		t.Fatalf("overlayPlanCanvas: %v", err)
	}
	if len(wire.ImageMotions) < 2 {
		t.Fatalf("image motion pool = %#v, want the certified catalog", wire.ImageMotions)
	}

	// An explicit pool still narrows the rotation; the fallback never
	// overwrites an authored choice.
	canvas.ImageMotions = []string{"image_focus_reveal"}
	wire, err = overlayPlanCanvas(canvas)
	if err != nil {
		t.Fatalf("overlayPlanCanvas with explicit pool: %v", err)
	}
	if len(wire.ImageMotions) != 1 || wire.ImageMotions[0] != "image_focus_reveal" {
		t.Fatalf("explicit image motion pool = %#v, want the caller's single value", wire.ImageMotions)
	}
}

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
