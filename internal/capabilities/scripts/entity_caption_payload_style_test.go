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
	style := &scriptpkg.OverlayStyleSpec{
		FontFamily: "oswald",
		Color:      []float64{1.0, 0.78, 0.25, 1.0},
		GlowSize:   &glow,
		GlowColor:  "#4DE1FF",
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

	segments, ok := capmodel.ChainSegmentMaps(items[1].Params)
	if !ok || len(segments) != 1 {
		t.Fatalf("chain segments = %#v, want one segment", items[1].Params["chain_segments"])
	}
	segmentParams, _ := segments[0]["params"].(map[string]any)
	if got := segmentParams["font_family"]; got != "oswald" {
		t.Errorf("chain caption font = %#v, want oswald", got)
	}
	if got := segmentParams["glow_size"]; got != 18.0 {
		t.Errorf("chain caption glow = %#v, want 18", got)
	}
	if got := segmentParams["fill"]; got != "#FFC740" {
		t.Errorf("chain caption fill = %#v, want #FFC740", got)
	}
}
