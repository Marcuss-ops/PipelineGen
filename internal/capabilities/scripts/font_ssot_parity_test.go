package scriptgeneration

import (
	"sort"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestCanonicalFontAssetsParity pins the single source of truth: the queue
// producer stages exactly the families the payload contract accepts, with a
// path and hash for each. Adding a family is one entry on each side.
func TestCanonicalFontAssetsParity(t *testing.T) {
	var registry []string
	for family := range capoverlay.CanonicalFontAssetsByFamily {
		registry = append(registry, family)
	}
	sort.Strings(registry)
	supported := append([]string(nil), scriptpkg.SupportedFontFamilies...)
	sort.Strings(supported)
	if len(registry) != len(supported) {
		t.Fatalf("font registry = %v, payload contract = %v", registry, supported)
	}
	for i := range registry {
		if registry[i] != supported[i] {
			t.Fatalf("font registry = %v, payload contract = %v", registry, supported)
		}
	}
	for family, asset := range capoverlay.CanonicalFontAssetsByFamily {
		if asset.Path == "" {
			t.Errorf("family %q has no staged path", family)
		}
		if len(asset.SHA256) != 64 {
			t.Errorf("family %q has invalid staged hash %q", family, asset.SHA256)
		}
	}
}

func TestRunTypographyOverridesChildAnimationDefaults(t *testing.T) {
	layers := []capoverlay.OverlayImageLayer{{
		Caption: "Ada Lovelace", CaptionFontFamily: "bricolage_grotesque",
		Params: map[string]any{
			"font_family": "inter_bold", "glow_size": 3.0,
			"style": map[string]any{"fill": "#FF0000", "shadow": map[string]any{"blur": 2.0}},
		},
	}}
	canonical := map[string]any{
		"font_family": "poppins", "glow_size": 0.0, "stroke_size": 2.0,
		"style": map[string]any{"fill": "#FFFFFF", "shadow": map[string]any{"blur": 8.0}},
	}
	scriptoverlay.MergeCaptionStyleIntoLayers(layers, canonical)
	if got := layers[0].Params["font_family"]; got != "poppins" {
		t.Fatalf("caption font = %v, want canonical run font poppins", got)
	}
	if got := layers[0].Params["glow_size"]; got != 0.0 {
		t.Fatalf("caption glow = %v, want explicit disabled glow", got)
	}
	if got := layers[0].Params["stroke_size"]; got != 2.0 {
		t.Fatalf("caption stroke = %v, want canonical run stroke", got)
	}
	style, _ := layers[0].Params["style"].(map[string]any)
	if style["fill"] != "#FFFFFF" || style["shadow"].(map[string]any)["blur"] != 8.0 {
		t.Fatalf("caption nested style = %v, want canonical run style", style)
	}
}

func TestExplicitRunShadowSettingsResolveOneCanonicalRuntimeStyle(t *testing.T) {
	disabled := scriptoverlay.StyleParams(&scriptpkg.OverlayStyleSpec{Shadow: &scriptpkg.OverlayShadowSpec{Enabled: false}})
	for key, want := range map[string]float64{
		"shadow_opacity": 0, "shadow_blur_px": 0,
		"shadow_offset_x_px": 0, "shadow_offset_y_px": 0,
	} {
		if got := disabled[key]; got != want {
			t.Errorf("disabled shadow %s = %v, want %v", key, got, want)
		}
	}
	enabled := scriptoverlay.StyleParams(&scriptpkg.OverlayStyleSpec{Shadow: &scriptpkg.OverlayShadowSpec{Enabled: true}})
	for key, want := range map[string]float64{
		"shadow_opacity": 0.7, "shadow_blur_px": 8,
		"shadow_offset_x_px": 0, "shadow_offset_y_px": 4,
	} {
		if got := enabled[key]; got != want {
			t.Errorf("enabled default shadow %s = %v, want %v", key, got, want)
		}
	}
}

func TestRunStyleWinsOverAnimationSpecificTextParams(t *testing.T) {
	zero, stroke := 0.0, 3.0
	items := []capoverlay.OverlayItem{{
		ID: "motion-text", Kind: "text_phrase",
		Params: map[string]any{
			"font_family": "inter_bold", "glow_size": 12.0, "stroke_size": 1.0,
			"shadow_blur_px": 20.0,
			"style":          map[string]any{"fill": "#FF0000", "shadow": map[string]any{"blur": 20.0}},
		},
	}}
	canvas := OverlayCanvasSpec{Style: &scriptpkg.OverlayStyleSpec{
		Color: []float64{1, 1, 1, 1}, FontFamily: "poppins",
		GlowSize: &zero, StrokeSize: &stroke,
		Shadow: &scriptpkg.OverlayShadowSpec{Enabled: false},
	}}
	applyOverlayStyleToItems(items, canvas)
	params := items[0].Params
	for key, want := range map[string]any{
		"font_family": "poppins", "glow_size": float64(0), "stroke_size": float64(3),
		"shadow_blur_px": float64(0), "shadow_opacity": float64(0),
	} {
		if got := params[key]; got != want {
			t.Errorf("run style %s = %v, want %v", key, got, want)
		}
	}
	style, _ := params["style"].(map[string]any)
	if style["fill"] != "#FFFFFF" {
		t.Errorf("run fill = %v, want #FFFFFF", style["fill"])
	}
	shadow, _ := style["shadow"].(map[string]any)
	if shadow["blur"] != 0.0 || shadow["opacity"] != 0.0 {
		t.Errorf("run nested shadow = %v, want disabled zero shadow", shadow)
	}

	entityParams := scriptoverlay.MergeRuntimeTextStyleParams(map[string]any{
		"font_family": "inter_bold", "position_x": 10.0, "glow_size": 8.0,
	}, overlayStyleParams(canvas.Style))
	if entityParams["font_family"] != "poppins" || entityParams["glow_size"] != 0.0 || entityParams["stroke_size"] != 3.0 {
		t.Errorf("entity caption style = %v, want canonical run style", entityParams)
	}
	if entityParams["position_x"] != 10.0 {
		t.Errorf("entity image geometry changed by text style merge: %v", entityParams["position_x"])
	}
}

func TestRuntimeFontAssetsStagesEverySelectedFamily(t *testing.T) {
	plan := capoverlay.OverlayPlan{Items: []capoverlay.OverlayItem{
		{ID: "a", Params: map[string]any{"font_family": "bricolage_grotesque"}},
		{ID: "b", Params: map[string]any{"font_family": "bricolage_grotesque"}},
		{ID: "c", CaptionFontFamily: "inter_bold"},
		{ID: "d", ImageLayers: []capoverlay.OverlayImageLayer{{Params: map[string]any{"font_family": "inter"}}}},
		{ID: "e", ImageLayers: []capoverlay.OverlayImageLayer{{Caption: "child", CaptionFontFamily: "playfair_display_italic"}}},
	}}
	assets, err := runtimeFontAssets(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 4 {
		t.Fatalf("staged %d font assets, want 4 (bricolage + inter_bold + image-layer inter + child caption playfair, deduped)", len(assets))
	}
	byRef := map[string]string{}
	for _, a := range assets {
		byRef[a.Ref().AssetID] = a.Ref().SHA256
	}
	if byRef["assets/fonts/Bricolage-Grotesque.ttf"] != "413e7357809ddd12fd80a96a8a396de0e401638d4acd3cb3e37532f0472ac682" {
		t.Errorf("bricolage asset = %v", byRef)
	}
	if byRef["assets/fonts/Inter-Bold.ttf"] != "deb990e241954e1af87c6033d40a76cf0f48e0d112541794526051ac4a3ee2c8" {
		t.Errorf("inter_bold asset = %v", byRef)
	}
	if _, ok := byRef["assets/fonts/Inter.ttf"]; !ok {
		t.Errorf("standalone image-layer Inter font missing: %v", byRef)
	}
	if _, ok := byRef["assets/fonts/PlayfairDisplay-Italic.ttf"]; !ok {
		t.Errorf("child caption Playfair font missing: %v", byRef)
	}
	if got, err := runtimeFontAssets(capoverlay.OverlayPlan{}); err != nil || len(got) != 0 {
		t.Errorf("empty plan staged %d assets, want 0", len(got))
	}
}

func TestRuntimeFontAssetsStagesFontsSelectedInsideChains(t *testing.T) {
	segments := []capoverlay.ChainSegment{{
		CaptionFontFamily: "inter_bold",
		Params:            map[string]any{"font_family": "bricolage_grotesque"},
		Style:             map[string]any{"font_family": "inter"},
		ImageLayers:       []capoverlay.OverlayImageLayer{{Caption: "child", CaptionFontFamily: "dejavu_sans", Params: map[string]any{"font_family": "inter_bold"}}},
	}}
	plan := capoverlay.OverlayPlan{Items: []capoverlay.OverlayItem{{
		ID: "chain", Kind: capoverlay.KindMixedChain,
		Params: map[string]any{"chain_segments": segments},
	}}}
	assets, err := runtimeFontAssets(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 4 {
		t.Fatalf("staged %d fonts, want four deduplicated chain and child-caption selections", len(assets))
	}
}

func TestRuntimeFontAssetsRejectsUnknownSelectedFamily(t *testing.T) {
	plan := capoverlay.OverlayPlan{Items: []capoverlay.OverlayItem{{
		ID: "bad-font", Params: map[string]any{"font_family": "not-a-family"},
	}}}
	if _, err := runtimeFontAssets(plan); err == nil {
		t.Fatal("unknown selected font family must fail closed")
	}
}
