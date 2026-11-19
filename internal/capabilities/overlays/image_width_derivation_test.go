package overlays

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestImageStyleHeightOnlyDropsThePlannerWidth certifies the height-only card
// contract: the planner's default still width must not survive a style that
// authored only a height, otherwise RenderingGen can never derive the width
// from the photo's aspect. Entity cards keep their authored square geometry.
func TestImageStyleHeightOnlyDropsThePlannerWidth(t *testing.T) {
	height := 700
	fit := "cover"
	photo := capmodel.OverlayItem{ID: "photo", Kind: "image", Params: map[string]any{
		"width": 1340.0, "height": 760.0, "fit": "contain", "priority": 1.0,
	}}
	ApplyImageStyle(&photo, &scriptpkg.OverlayImageStyleSpec{Height: &height, Fit: fit})
	if _, ok := photo.Params["width"]; ok {
		t.Fatalf("height-only style kept the planner width: %#v", photo.Params)
	}
	if photo.Params["height"] != 700 || photo.Params["fit"] != "cover" {
		t.Fatalf("height-only style geometry = %#v, want 700/cover", photo.Params)
	}

	width := 960
	authored := capmodel.OverlayItem{ID: "authored", Kind: "image", Params: map[string]any{"width": 1340.0}}
	ApplyImageStyle(&authored, &scriptpkg.OverlayImageStyleSpec{Width: &width, Height: &height})
	if authored.Params["width"] != 960 || authored.Params["height"] != 700 {
		t.Fatalf("authored geometry = %#v, want 960x700", authored.Params)
	}

	entity := capmodel.OverlayItem{ID: "portrait", Kind: string(capmodel.KindEntityImage), Params: map[string]any{"width": 820.0, "height": 820.0}}
	ApplyImageStyle(&entity, &scriptpkg.OverlayImageStyleSpec{Height: &height})
	if entity.Params["width"] != 820.0 && entity.Params["width"] != 820 {
		t.Fatalf("entity card lost its authored width: %#v", entity.Params)
	}
}
