package scriptgeneration

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// A captioned entity card is image-backed, but its visible text is the
// caption. The run-level payload typography must reach that caption both as a
// standalone card and when the card is fused into an image_chain; otherwise
// the payload font/color/glow silently stop at the portrait.
func TestPayloadTextStyleReachesCaptionedEntityImagesAndChainSegments(t *testing.T) {
	glow := 18.0
	opacity := 0.82
	radius := 10.0
	shadowOpacity := 0.55
	shadowBlur := 18.0
	style := &scriptpkg.OverlayStyleSpec{
		FontFamily: "oswald",
		Color:      []float64{1.0, 0.78, 0.25, 1.0},
		GlowSize:   &glow,
		GlowColor:  "#4DE1FF",
		// The context rectangle behind an entity name is the same payload
		// card_background every text lane uses; its colour is caller-owned.
		CardBackground: &scriptpkg.OverlayCardBackgroundSpec{
			Color: "#12233A", Opacity: &opacity, Radius: &radius, Padding: []float64{16, 10},
			Stroke: &scriptpkg.OverlayCardBackgroundStrokeSpec{Color: "#FFD159", Width: 2.5},
			Shadow: &scriptpkg.OverlayCardBackgroundShadowSpec{
				Color: "#000000", Opacity: &shadowOpacity, Blur: &shadowBlur, Offset: []float64{0, 6},
			},
			Motions: []string{"fade", "slide_up"},
		},
	}
	items := []capmodel.OverlayItem{
		{
			ID:            "entity",
			Kind:          "entity_image",
			EntityCaption: "Ada Lovelace",
			AssetRefs: []capmodel.OverlayAssetRef{{
				AssetID: "ada", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			}},
		},
		{
			ID:   "chain",
			Kind: "image_chain",
			Params: map[string]any{
				"chain_segments": []any{
					map[string]any{
						"kind":           "entity_image",
						"entity_caption": "Grace Hopper",
						"asset_id":       "grace",
						"params":         map[string]any{},
					},
				},
			},
		},
	}

	applyPayloadOverlayStyleToItems(items, OverlayCanvasSpec{Style: style})

	entityParams := items[0].Params
	if got := entityParams["font_family"]; got != "oswald" {
		t.Errorf("entity caption font = %#v, want oswald", got)
	}
	if got := entityParams["glow_size"]; got != 18.0 {
		t.Errorf("entity caption glow = %#v, want 18", got)
	}
	if got := entityParams["glow_color"]; got != "#4DE1FF" {
		t.Errorf("entity caption glow color = %#v, want #4DE1FF", got)
	}
	if got := entityParams["fill"]; got != "#FFC740" {
		t.Errorf("entity caption fill = %#v, want #FFC740", got)
	}
	assertCaptionBackground(t, entityParams, "entity caption")

	segments, ok := items[1].Segments()
	if !ok || len(segments) != 1 {
		t.Fatalf("chain segments = %#v, want one segment", items[1].Params["chain_segments"])
	}
	segmentParams := segments[0].Params
	if got := segmentParams["font_family"]; got != "oswald" {
		t.Errorf("chain caption font = %#v, want oswald", got)
	}
	if got := segmentParams["glow_size"]; got != 18.0 {
		t.Errorf("chain caption glow = %#v, want 18", got)
	}
	if got := segmentParams["fill"]; got != "#FFC740" {
		t.Errorf("chain caption fill = %#v, want #FFC740", got)
	}
	assertCaptionBackground(t, segmentParams, "chain caption")
}

// assertCaptionBackground pins the context rectangle and its colour on the
// params the renderer reads: the nested style.background travels with the
// caption, so the plate is opt-in and caller-coloured.
func assertCaptionBackground(t *testing.T, params map[string]any, label string) {
	t.Helper()
	style, ok := params["style"].(map[string]any)
	if !ok {
		t.Fatalf("%s style block = %#v, want one", label, params["style"])
	}
	plate, ok := style["background"].(map[string]any)
	if !ok {
		t.Fatalf("%s background = %#v, want a plate", label, style["background"])
	}
	if got := plate["color"]; got != "#12233A" {
		t.Errorf("%s plate color = %#v, want #12233A", label, got)
	}
	if got := plate["opacity"]; got != 0.82 {
		t.Errorf("%s plate opacity = %#v, want 0.82", label, got)
	}
	if got := plate["radius"]; got != 10.0 {
		t.Errorf("%s plate radius = %#v, want 10", label, got)
	}
	padding, ok := plate["padding"].([]float64)
	if !ok || len(padding) != 2 || padding[0] != 16 || padding[1] != 10 {
		t.Errorf("%s plate padding = %#v, want [16 10]", label, plate["padding"])
	}
	stroke, ok := plate["stroke"].(map[string]any)
	if !ok || stroke["color"] != "#FFD159" || stroke["width"] != 2.5 {
		t.Errorf("%s plate stroke = %#v, want #FFD159 at 2.5", label, plate["stroke"])
	}
	shadow, ok := plate["shadow"].(map[string]any)
	if !ok || shadow["color"] != "#000000" || shadow["blur"] != 18.0 || shadow["opacity"] != 0.55 {
		t.Errorf("%s plate shadow = %#v, want #000000 blur 18 opacity 0.55", label, plate["shadow"])
	}
	motions, ok := plate["motions"].([]string)
	if !ok || len(motions) != 2 || motions[0] != "fade" || motions[1] != "slide_up" {
		t.Errorf("%s plate motions = %#v, want [fade slide_up]", label, plate["motions"])
	}
}
