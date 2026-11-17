package script

import (
	"strings"
	"testing"
)

// TestValidateImageStyleSharesRunLevelBounds pins that a per-pin style is
// checked by the same owner as overlay_style.image, not a second copy.
func TestValidateImageStyleSharesRunLevelBounds(t *testing.T) {
	spacing := 2.0
	if err := ValidateImageStyle(&OverlayImageStyleSpec{Grid: &OverlayImageGridSpec{SpacingPX: &spacing}}); err == nil {
		t.Fatal("grid.spacing_px 2 must be rejected")
	}
	effects := make([]ImageEffectSpec, 0, MaxImageEffects+1)
	for i := 0; i <= MaxImageEffects; i++ {
		effects = append(effects, ImageEffectSpec{Type: "glow", Radius: 10})
	}
	if err := ValidateImageStyle(&OverlayImageStyleSpec{Effects: effects}); err == nil {
		t.Fatalf("more than %d effects must be rejected", MaxImageEffects)
	}
	if err := ValidateImageStyle(&OverlayImageStyleSpec{Effects: []ImageEffectSpec{{Type: "vignette", Amount: 0.3}}}); err != nil {
		t.Fatalf("valid pin style rejected: %v", err)
	}
}

// TestValidateOverlayImageStylePinRequiresScene proves the pin style is bound
// to a scene (the style is applied to the image that scene produces) and that
// an invalid style is reported with its payload path.
func TestValidateOverlayImageStylePinRequiresScene(t *testing.T) {
	details := validateOverlayImageSFX([]OverlayImageAsset{
		{Style: &OverlayImageStyleSpec{}},
	}, "item")
	if len(details) != 1 || !strings.Contains(details[0], "overlay_images[0].style requires scene_id") {
		t.Fatalf("missing scene_id details = %v", details)
	}

	spacing := 900.0
	details = validateOverlayImageSFX([]OverlayImageAsset{
		{SceneID: "s1", Style: &OverlayImageStyleSpec{Grid: &OverlayImageGridSpec{SpacingPX: &spacing}}},
	}, "item")
	if len(details) != 1 || !strings.Contains(details[0], "overlay_images[0].style:") {
		t.Fatalf("invalid pin style details = %v", details)
	}
}
