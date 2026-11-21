package script

import "testing"

func TestOverlayCardBackgroundAcceptsStrokeShadowAndMotions(t *testing.T) {
	opacity := 0.55
	blur := 18.0
	spec := &OverlayStyleSpec{CardBackground: &OverlayCardBackgroundSpec{
		Color:   "#12233A",
		Stroke:  &OverlayCardBackgroundStrokeSpec{Color: "#FFD159", Width: 2.5},
		Shadow:  &OverlayCardBackgroundShadowSpec{Color: "#000000", Opacity: &opacity, Blur: &blur, Offset: []float64{0, 6}},
		Motions: []string{"fade", "slide_left", "wipe_right"},
	}}
	if err := spec.Validate(); err != nil {
		t.Fatalf("valid card_background rejected: %v", err)
	}
}

func TestOverlayCardBackgroundRejectsOutOfContractFields(t *testing.T) {
	nine := []string{"fade", "fade", "fade", "fade", "fade", "fade", "fade", "fade", "fade"}
	badBlur := 65.0
	for name, background := range map[string]*OverlayCardBackgroundSpec{
		"stroke color":   {Color: "#12233A", Stroke: &OverlayCardBackgroundStrokeSpec{Color: "red", Width: 2}},
		"stroke width":   {Color: "#12233A", Stroke: &OverlayCardBackgroundStrokeSpec{Color: "#FFFFFF", Width: 65}},
		"shadow blur":    {Color: "#12233A", Shadow: &OverlayCardBackgroundShadowSpec{Blur: &badBlur}},
		"shadow offset":  {Color: "#12233A", Shadow: &OverlayCardBackgroundShadowSpec{Offset: []float64{4}}},
		"unknown motion": {Color: "#12233A", Motions: []string{"explode"}},
		"motion pool":    {Color: "#12233A", Motions: nine},
	} {
		spec := &OverlayStyleSpec{CardBackground: background}
		if err := spec.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}
