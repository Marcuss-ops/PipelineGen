package scriptgeneration

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	caprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/render"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// rustFontID is the only family spelling the Rust overlay compiler accepts on
// an editorial bullet pin: lowercase letters, digits and underscores, at most
// 64 bytes (overlay-compiler/src/editorial_plan_v2/request.rs).
var rustFontID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// rustFontConstant matches the runtime family constants the Rust planner emits
// when it has to choose a default (for example PLAYFAIR_RUNTIME_FAMILY and
// ENTITY_CAPTION_FONT).
var rustFontConstant = regexp.MustCompile(`(?m)^\s*const\s+([A-Z0-9_]*(?:FONT|FAMILY)[A-Z0-9_]*)\s*:\s*&str\s*=\s*"([^"]+)"`)

// TestCanonicalFontAssetsParity pins the Go half of the font contract: the
// payload-selectable families and the queue asset registry are the same set,
// with one staged path and one content hash per family. The Rust/rendering
// half is checked by TestRustFontVocabularyParity below and by RenderingGen's
// runtime font tests.
func TestCanonicalFontAssetsParity(t *testing.T) {
	registry := make(map[string]caprender.CanonicalFontAsset, len(caprender.CanonicalFontAssetsByFamily))
	for family, asset := range caprender.CanonicalFontAssetsByFamily {
		registry[family] = asset
	}
	supported := append([]string(nil), scriptpkg.SupportedFontFamilies...)
	sort.Strings(supported)
	if len(supported) != len(registry) {
		t.Fatalf("font payload contract has %d families, asset registry has %d", len(supported), len(registry))
	}
	for index, family := range supported {
		if index > 0 && family == supported[index-1] {
			t.Fatalf("font payload contract lists %q twice", family)
		}
		if !rustFontID.MatchString(family) {
			t.Errorf("family %q is not a valid Rust runtime font id", family)
		}
		asset, ok := registry[family]
		if !ok {
			t.Errorf("family %q is selectable in the payload but has no staged asset", family)
			continue
		}
		if asset.Path == "" {
			t.Errorf("family %q has no staged path", family)
		}
		if len(asset.SHA256) != 64 || strings.ToLower(asset.SHA256) != asset.SHA256 {
			t.Errorf("family %q has invalid staged hash %q", family, asset.SHA256)
		}
	}
	for family := range registry {
		if !scriptpkg.IsSupportedFontFamily(family) {
			t.Errorf("asset registry exposes %q, which the payload contract rejects", family)
		}
	}
}

// TestRustFontVocabularyParity reads the fonts the Rust overlay compiler
// actually emits: every checked-in contract plan, every request pin, and the
// runtime-default constants in the planner source. Rust deliberately does not
// restate the family catalogue (request.rs: identity is owned by the producer
// and by the RenderingGen registry, this layer validates shape only), so the
// fixtures and constants are the Rust-side vocabulary this producer must be
// able to stage. Adding a family on the Rust side therefore fails here until
// SupportedFontFamilies and CanonicalFontAssetsByFamily learn it.
func TestRustFontVocabularyParity(t *testing.T) {
	root := overlayPlanFixtureRoot
	if _, err := os.Stat(root); err != nil {
		t.Skipf("cross-repo BrainRust overlay fixtures unavailable: %v", err)
	}
	emitted, sources := rustEmittedFontFamilies(t, root)
	if len(emitted) == 0 {
		t.Fatal("Rust overlay fixtures declare no font family: parity would be vacuous")
	}
	constants := rustFontConstants(t, root)
	if len(constants) == 0 {
		t.Fatal("Rust planner exposes no runtime font constant: parity would be vacuous")
	}
	for family, source := range constants {
		if _, ok := emitted[family]; !ok {
			emitted[family] = source
			sources[family] = source
		}
	}
	if _, ok := emitted["playfair_display_italic"]; !ok {
		t.Errorf("Rust fixtures/constants never mention the canonical runtime family playfair_display_italic")
	}
	families := make([]string, 0, len(emitted))
	for family := range emitted {
		families = append(families, family)
	}
	sort.Strings(families)
	for _, family := range families {
		if !rustFontID.MatchString(family) {
			t.Errorf("%s: Rust emits %q, which is not a valid runtime font id", sources[family], family)
		}
		if !scriptpkg.IsSupportedFontFamily(family) {
			t.Errorf("%s: Rust emits %q, which the Go payload contract does not accept", sources[family], family)
		}
		asset, ok := caprender.CanonicalFontAssetsByFamily[family]
		if !ok {
			t.Errorf("%s: Rust emits %q, which has no canonical staged asset", sources[family], family)
			continue
		}
		if asset.Path == "" || len(asset.SHA256) != 64 {
			t.Errorf("%s: family %q has an incomplete canonical asset: %+v", sources[family], family, asset)
		}
	}
}

// rustEmittedFontFamilies walks every JSON fixture under the Rust overlay
// contract root and collects font_family / caption_font_family values, with
// the file that declared each one.
func rustEmittedFontFamilies(t *testing.T, root string) (map[string]string, map[string]string) {
	t.Helper()
	families := map[string]string{}
	sources := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var document any
		if err := json.Unmarshal(raw, &document); err != nil {
			return err
		}
		walkFontFamilies(document, func(family string) {
			if _, seen := families[family]; !seen {
				families[family] = path
				sources[family] = path
			}
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk Rust overlay fixtures: %v", err)
	}
	return families, sources
}

func walkFontFamilies(node any, visit func(string)) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "font_family" || key == "caption_font_family" {
				if family, ok := child.(string); ok {
					if family = strings.TrimSpace(family); family != "" {
						visit(family)
					}
				}
			}
			walkFontFamilies(child, visit)
		}
	case []any:
		for _, child := range value {
			walkFontFamilies(child, visit)
		}
	}
}

// rustFontConstants reads the runtime-default family constants from the Rust
// planner source next to the fixtures. It fails closed when the source tree is
// present but the constants disappeared: a rename must be a deliberate update
// of this gate, not a silent loss of coverage.
func rustFontConstants(t *testing.T, fixtureRoot string) map[string]string {
	t.Helper()
	plannerDir := filepath.Join(fixtureRoot, "..", "..", "..", "src", "editorial_plan_v2", "planner")
	entries, err := os.ReadDir(plannerDir)
	if err != nil {
		t.Fatalf("read Rust planner source %s: %v", plannerDir, err)
	}
	found := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".rs") {
			continue
		}
		path := filepath.Join(plannerDir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, match := range rustFontConstant.FindAllStringSubmatch(string(raw), -1) {
			family := strings.TrimSpace(match[2])
			if family == "" {
				continue
			}
			// Two constants may legitimately name the same family (the entity
			// caption default and the presentation default are both Playfair);
			// the gate only needs the family set, not the constant names.
			if _, exists := found[family]; !exists {
				found[family] = entry.Name()
			}
		}
	}
	return found
}

func TestExplicitRunShadowSettingsResolveOneCanonicalRuntimeStyle(t *testing.T) {
	disabled := capoverlay.StyleParams(&scriptpkg.OverlayStyleSpec{Shadow: &scriptpkg.OverlayShadowSpec{Enabled: false}})
	for key, want := range map[string]float64{
		"shadow_opacity": 0, "shadow_blur_px": 0,
		"shadow_offset_x_px": 0, "shadow_offset_y_px": 0,
	} {
		if got := disabled[key]; got != want {
			t.Errorf("disabled shadow %s = %v, want %v", key, got, want)
		}
	}
	enabled := capoverlay.StyleParams(&scriptpkg.OverlayStyleSpec{Shadow: &scriptpkg.OverlayShadowSpec{Enabled: true}})
	for key, want := range map[string]float64{
		"shadow_opacity": 0.7, "shadow_blur_px": 8,
		"shadow_offset_x_px": 0, "shadow_offset_y_px": 4,
	} {
		if got := enabled[key]; got != want {
			t.Errorf("enabled default shadow %s = %v, want %v", key, got, want)
		}
	}
}

// TestRunStyleWinsOverAnimationSpecificTextParams covers the live Go boundary
// that projects a run style onto overlay items. The entity-caption merge that
// used to follow is Rust-owned now (presentation.rs normalizes canonical fonts
// before the plan leaves the compiler), so it is asserted against Rust
// fixtures instead of a second Go merge.
func TestRunStyleWinsOverAnimationSpecificTextParams(t *testing.T) {
	zero, stroke := 0.0, 3.0
	items := []capmodel.OverlayItem{{
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
	applyPayloadOverlayStyleToItems(items, canvas)
	params := items[0].Params
	for key, want := range map[string]any{
		"font_family": "poppins", "stroke_size": float64(3),
		"shadow_blur_px": float64(0), "shadow_opacity": float64(0),
	} {
		if got := params[key]; got != want {
			t.Errorf("run style %s = %v, want %v", key, got, want)
		}
	}
	// A disabled run glow travels as an explicit zero; the renderer clears the
	// preset/role glow on it, so the animation's glow must not survive either.
	if got, ok := params["glow_size"]; !ok || got != 0.0 {
		t.Errorf("run style disabled glow = %v (present=%v), want explicit 0", got, ok)
	}
	style, _ := params["style"].(map[string]any)
	if style["fill"] != "#FFFFFF" {
		t.Errorf("run fill = %v, want #FFFFFF", style["fill"])
	}
	shadow, _ := style["shadow"].(map[string]any)
	if shadow["blur"] != 0.0 || shadow["opacity"] != 0.0 {
		t.Errorf("run nested shadow = %v, want disabled zero shadow", shadow)
	}
}

func TestRuntimeFontAssetsStagesEverySelectedFamily(t *testing.T) {
	plan := capmodel.OverlayPlan{Items: []capmodel.OverlayItem{
		{ID: "a", Params: map[string]any{"font_family": "bricolage_grotesque"}},
		{ID: "b", Params: map[string]any{"font_family": "bricolage_grotesque"}},
		{ID: "c", CaptionFontFamily: "inter_bold"},
		{ID: "d", ImageLayers: []capmodel.OverlayImageLayer{{Params: map[string]any{"font_family": "inter"}}}},
		{ID: "e", ImageLayers: []capmodel.OverlayImageLayer{{Caption: "child", CaptionFontFamily: "playfair_display_italic"}}},
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
	if got, err := runtimeFontAssets(capmodel.OverlayPlan{}); err != nil || len(got) != 0 {
		t.Errorf("empty plan staged %d assets, want 0", len(got))
	}
}

func TestRuntimeFontAssetsStagesFontsSelectedInsideChains(t *testing.T) {
	segments := []capmodel.ChainSegment{{
		CaptionFontFamily: "inter_bold",
		Params:            map[string]any{"font_family": "bricolage_grotesque"},
		Style:             map[string]any{"font_family": "inter"},
		ImageLayers:       []capmodel.OverlayImageLayer{{Caption: "child", CaptionFontFamily: "dejavu_sans", Params: map[string]any{"font_family": "inter_bold"}}},
	}}
	plan := capmodel.OverlayPlan{Items: []capmodel.OverlayItem{{
		ID: "chain", Kind: capmodel.KindMixedChain,
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
	plan := capmodel.OverlayPlan{Items: []capmodel.OverlayItem{{
		ID: "bad-font", Params: map[string]any{"font_family": "not-a-family"},
	}}}
	if _, err := runtimeFontAssets(plan); err == nil {
		t.Fatal("unknown selected font family must fail closed")
	}
}
