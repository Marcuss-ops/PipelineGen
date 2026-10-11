package scriptgeneration

import (
	"sort"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
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

func TestRuntimeFontAssetsStagesEverySelectedFamily(t *testing.T) {
	plan := capoverlay.OverlayPlan{Items: []capoverlay.OverlayItem{
		{ID: "a", Params: map[string]any{"font_family": "bricolage_grotesque"}},
		{ID: "b", Params: map[string]any{"font_family": "bricolage_grotesque"}},
		{ID: "c", CaptionFontFamily: "inter_bold"},
		{ID: "d", ImageLayers: []capoverlay.OverlayImageLayer{{Params: map[string]any{"font_family": "inter"}}}},
	}}
	assets, err := runtimeFontAssets(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 3 {
		t.Fatalf("staged %d font assets, want 3 (bricolage + inter_bold + image-layer inter, deduped)", len(assets))
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
	if got, err := runtimeFontAssets(capoverlay.OverlayPlan{}); err != nil || len(got) != 0 {
		t.Errorf("empty plan staged %d assets, want 0", len(got))
	}
}

func TestRuntimeFontAssetsStagesFontsSelectedInsideChains(t *testing.T) {
	segments := []capoverlay.ChainSegment{{
		CaptionFontFamily: "inter_bold",
		Params:            map[string]any{"font_family": "bricolage_grotesque"},
		Style:             map[string]any{"font_family": "inter"},
		ImageLayers:       []capoverlay.OverlayImageLayer{{Params: map[string]any{"font_family": "inter_bold"}}},
	}}
	plan := capoverlay.OverlayPlan{Items: []capoverlay.OverlayItem{{
		ID: "chain", Kind: capoverlay.KindMixedChain,
		Params: map[string]any{"chain_segments": segments},
	}}}
	assets, err := runtimeFontAssets(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 3 {
		t.Fatalf("staged %d fonts, want three deduplicated chain selections", len(assets))
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
