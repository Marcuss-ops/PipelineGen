package overlayplan

import "testing"

func TestOverlayPlanCanvasDefaultsTheCertifiedImageMotionPools(t *testing.T) {
	canvas, err := OverlayPlanCanvas(OverlayCanvasSpec{})
	if err != nil {
		t.Fatalf("OverlayPlanCanvas: %v", err)
	}
	if len(canvas.ImageMotions) < 2 {
		t.Fatalf("image motions = %v, want the certified rotation pool", canvas.ImageMotions)
	}
	if len(canvas.EntityImageMotions) == 0 {
		t.Fatal("entity image motions must default to the certified pool")
	}
	if len(canvas.EntityCaptionMotions) == 0 {
		t.Fatal("entity caption motions must default to the certified pool")
	}
}

func TestOverlayPlanCanvasPreservesExplicitImageMotionPools(t *testing.T) {
	canvas, err := OverlayPlanCanvas(OverlayCanvasSpec{
		ImageMotions:         []string{"image_one", "image_two"},
		EntityImageMotions:   []string{"entity_one"},
		EntityCaptionMotions: []string{"caption_one"},
	})
	if err != nil {
		t.Fatalf("OverlayPlanCanvas: %v", err)
	}
	if len(canvas.ImageMotions) != 2 || canvas.ImageMotions[0] != "image_one" || canvas.ImageMotions[1] != "image_two" {
		t.Fatalf("explicit image motions were replaced: %v", canvas.ImageMotions)
	}
	if len(canvas.EntityImageMotions) != 1 || canvas.EntityImageMotions[0] != "entity_one" {
		t.Fatalf("explicit entity image motions were replaced: %v", canvas.EntityImageMotions)
	}
	if len(canvas.EntityCaptionMotions) != 1 || canvas.EntityCaptionMotions[0] != "caption_one" {
		t.Fatalf("explicit entity caption motions were replaced: %v", canvas.EntityCaptionMotions)
	}
}
