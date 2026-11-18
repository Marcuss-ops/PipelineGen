package scriptgeneration

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestPayloadBulletFontStylesBulletsOnly pins the second-font contract: the
// bullet block reaches bullet-point copy, while every other text item keeps the
// run-level family the payload chose.
func TestPayloadBulletFontStylesBulletsOnly(t *testing.T) {
	bulletSize := 96.0
	items := []capmodel.OverlayItem{
		{
			ID: "bullet", SceneID: "s1", Kind: "text_phrase",
			Params: map[string]any{"editorial_role": "scene_bullet", "font_family": "playfair_display_italic"},
			Style:  map[string]any{"font_family": "playfair_display_italic"},
		},
		{
			ID: "phrase", SceneID: "s1", Kind: "text_phrase",
			Params: map[string]any{}, Style: map[string]any{"font_family": "playfair_display_italic"},
		},
	}
	canvas := OverlayCanvasSpec{Style: &scriptpkg.OverlayStyleSpec{
		FontFamily: "inter",
		Bullet:     &scriptpkg.OverlayBulletStyleSpec{FontFamily: "oswald", FontSizePX: &bulletSize},
	}}

	applyPayloadOverlayStyleToItems(items, canvas)

	bullet := items[0]
	if got := bullet.Params["font_family"]; got != "oswald" {
		t.Errorf("bullet params font = %v, want oswald", got)
	}
	if got := bullet.Style["font_family"]; got != "oswald" {
		t.Errorf("bullet style font = %v, want oswald (Style overrides Params)", got)
	}
	if got := bullet.Params["font_size_px"]; got != bulletSize {
		t.Errorf("bullet font size = %v, want %v", got, bulletSize)
	}
	if got := bullet.Style["font_size"]; got != bulletSize {
		t.Errorf("bullet style font size = %v, want %v", got, bulletSize)
	}

	phrase := items[1]
	if got := phrase.Params["font_family"]; got != "inter" {
		t.Errorf("phrase params font = %v, want the run-level inter", got)
	}
	if got := phrase.Style["font_family"]; got != "inter" {
		t.Errorf("phrase style font = %v, want the run-level inter", got)
	}
	if _, ok := phrase.Params["font_size_px"]; ok {
		t.Errorf("phrase size must keep the planner default, got %v", phrase.Params["font_size_px"])
	}
}

// TestBulletFontFallbackAndPinPrecedence pins the fallback chain: no bullet
// block means the run-level font, and a per-scene pin still wins on its bullet.
func TestBulletFontFallbackAndPinPrecedence(t *testing.T) {
	pinSize := 64.0
	items := []capmodel.OverlayItem{
		{ID: "fallback", SceneID: "s1", Kind: "text_phrase", Params: map[string]any{"editorial_role": "scene_bullet"}},
		{ID: "pinned", SceneID: "s2", Kind: "text_phrase", Params: map[string]any{"editorial_role": "chapter_bullet"}},
	}
	canvas := OverlayCanvasSpec{
		Style: &scriptpkg.OverlayStyleSpec{FontFamily: "inter"},
		EditorialBulletStyles: []scriptpkg.EditorialBulletStyle{{
			SceneID: "s2", FontFamily: "abril_fatface", FontSizePX: pinSize,
		}},
	}

	applyPayloadOverlayStyleToItems(items, canvas)

	if got := items[0].Params["font_family"]; got != "inter" {
		t.Errorf("bullet without a block = %v, want the run-level inter", got)
	}
	if got := items[1].Params["font_family"]; got != "abril_fatface" {
		t.Errorf("pinned bullet font = %v, want the pin abril_fatface", got)
	}
	if got := items[1].Params["font_size_px"]; got != pinSize {
		t.Errorf("pinned bullet size = %v, want %v", got, pinSize)
	}

	// A pin is more specific than the run-level bullet block, so it survives
	// even when both name a family.
	bulletSize := 96.0
	canvas.Style.Bullet = &scriptpkg.OverlayBulletStyleSpec{FontFamily: "oswald", FontSizePX: &bulletSize}
	applyPayloadOverlayStyleToItems(items, canvas)
	if got := items[0].Params["font_family"]; got != "oswald" {
		t.Errorf("bullet with a block = %v, want oswald", got)
	}
	if got := items[1].Params["font_family"]; got != "abril_fatface" {
		t.Errorf("pin lost to the run-level bullet block: %v", got)
	}
}

// TestBulletFontAppliesInsideFusedChains pins the same contract for a bullet
// that chain coalescing fused with other copy: the segment keeps its bullet
// font and its siblings keep the run-level font.
func TestBulletFontAppliesInsideFusedChains(t *testing.T) {
	item := capmodel.OverlayItem{
		ID: "chain", SceneID: "s1", Kind: "mixed_chain",
		Params: map[string]any{"chain_segments": []any{
			map[string]any{
				"kind":   "text_phrase",
				"params": map[string]any{"editorial_role": "scene_bullet", "font_family": "playfair_display_italic"},
				"style":  map[string]any{"font_family": "playfair_display_italic"},
			},
			map[string]any{
				"kind":   "number",
				"params": map[string]any{"font_family": "playfair_display_italic"},
				"style":  map[string]any{"font_family": "playfair_display_italic"},
			},
		}},
	}
	canvas := OverlayCanvasSpec{Style: &scriptpkg.OverlayStyleSpec{
		FontFamily: "inter",
		Bullet:     &scriptpkg.OverlayBulletStyleSpec{FontFamily: "oswald"},
	}}

	applyPayloadOverlayStyleToItems([]capmodel.OverlayItem{item}, canvas)

	segments, ok := capmodel.DecodeChainSegments(item.Params)
	if !ok || len(segments) != 2 {
		t.Fatalf("chain segments = %+v (decoded=%v)", item.Params["chain_segments"], ok)
	}
	if got := segments[0].Params["font_family"]; got != "oswald" {
		t.Errorf("bullet segment font = %v, want oswald", got)
	}
	if got := segments[0].Style["font_family"]; got != "oswald" {
		t.Errorf("bullet segment style font = %v, want oswald", got)
	}
	if got := segments[1].Params["font_family"]; got != "inter" {
		t.Errorf("sibling segment font = %v, want the run-level inter", got)
	}
}

func TestOverlayBulletStyleValidation(t *testing.T) {
	size := 96.0
	if err := (&scriptpkg.OverlayStyleSpec{Bullet: &scriptpkg.OverlayBulletStyleSpec{
		FontFamily: "oswald", FontSizePX: &size,
	}}).Validate(); err != nil {
		t.Fatalf("valid bullet block rejected: %v", err)
	}

	small, large, zero := 8.0, 400.0, 0.0
	stroke := 65.0
	cases := map[string]*scriptpkg.OverlayStyleSpec{
		"unknown font":   {Bullet: &scriptpkg.OverlayBulletStyleSpec{FontFamily: "comic_sans"}},
		"size too small": {Bullet: &scriptpkg.OverlayBulletStyleSpec{FontSizePX: &small}},
		"size too large": {Bullet: &scriptpkg.OverlayBulletStyleSpec{FontSizePX: &large}},
		"size zero":      {Bullet: &scriptpkg.OverlayBulletStyleSpec{FontSizePX: &zero}},
		"glow color":     {Bullet: &scriptpkg.OverlayBulletStyleSpec{GlowColor: "white"}},
		"stroke":         {Bullet: &scriptpkg.OverlayBulletStyleSpec{StrokeSize: &stroke}},
	}
	for name, style := range cases {
		if err := style.Validate(); err == nil {
			t.Errorf("%s: invalid bullet block accepted", name)
		}
	}
}
