package overlays

import (
	"testing"
)

// testRenderPlan is a minimal plan canvas for renderer compile tests.
func testRenderPlan() OverlayPlan {
	return OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "renderer-plan",
		VideoID:       "renderer-video",
		Width:         1920,
		Height:        1080,
		FPSNum:        30, FPSDen: 1,
	}
}

// TestRenderers_CompileEntityCards pins the three entity-card renderers:
// each requires text and compiles to a text layer with the entity_card
// preset and the correct template-backed geometry.
func TestRenderers_CompileEntityCards(t *testing.T) {
	plan := testRenderPlan()
	cases := []struct {
		renderer   Renderer
		name       string
		template   string
		wantPreset string
	}{
		{PersonCardRenderer{}, "PersonCardRenderer", "person_default", "lower_third_safe"},
		{OrganizationCardRenderer{}, "OrganizationCardRenderer", "org_default", "organization_card"},
		{LocationCardRenderer{}, "LocationCardRenderer", "gpe_default", "location_card"},
	}
	for _, tc := range cases {
		item := OverlayItem{ID: "item", Kind: "entity_card", TemplateID: tc.template, Text: "Ada", StartMs: 1000, EndMs: 3000}
		layer, err := tc.renderer.Compile(item, plan)
		if err != nil {
			t.Fatalf("%s.Compile: %v", tc.name, err)
		}
		if tc.renderer.Name() != tc.name {
			t.Fatalf("%s.Name() = %q, want %q", tc.name, tc.renderer.Name(), tc.name)
		}
		if layer.Type != "" {
			t.Errorf("%s: layer.Type = %q, want empty (Chronon derives it)", tc.name, layer.Type)
		}
		if layer.Preset != modernPresetFor(item, plan.PlanID) {
			t.Errorf("%s: layer.Preset = %q, want modern preset", tc.name, layer.Preset)
		}
		if layer.Text != "Ada" {
			t.Errorf("%s: layer.Text = %q, want Ada", tc.name, layer.Text)
		}
		// 1s→3s @ 30fps = start 30, duration 60.
		if layer.StartFrame != 30 || layer.DurationFrames != 60 {
			t.Errorf("%s: frame range = [%d +%d], want [30 +60]", tc.name, layer.StartFrame, layer.DurationFrames)
		}
	}
}

// TestRenderers_CompileNonEntity pins the lower-third / popup / quote
// renderers and their distinct geometry.
func TestRenderers_CompileNonEntity(t *testing.T) {
	plan := testRenderPlan()

	lt, err := (LowerThirdRenderer{}).Compile(OverlayItem{ID: "lt", Kind: "lower_third", TemplateID: "lower_third", Text: "Nome Cognome", StartMs: 0, EndMs: 2000}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if lt.Type != "" || lt.Preset != modernPresetFor(OverlayItem{ID: "lt", SceneID: "", TemplateID: "lower_third"}, plan.PlanID) {
		t.Fatalf("lower_third layer = %+v, want modern preset with no type", lt)
	}

	popup, err := (ImagePopupRenderer{}).Compile(OverlayItem{
		ID: "popup", Kind: "image_popup", TemplateID: "image_popup", StartMs: 0, EndMs: 2000,
		AssetRefs: []OverlayAssetRef{{AssetID: "img", URL: "assets/apple.png", SHA256: "abc"}},
	}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if popup.Preset != modernPresetFor(OverlayItem{ID: "popup", TemplateID: "image_popup"}, plan.PlanID) || popup.Type != "" || popup.Fit != "" || popup.BoxWidth != 0 || popup.BoxHeight != 0 {
		t.Fatalf("image_popup layer = %+v, want modern preset with no type/geometry", popup)
	}

	quote, err := (QuoteRenderer{}).Compile(OverlayItem{ID: "q", Kind: "quote", TemplateID: "QUOTE", Text: "\"Cambia tutto\"", StartMs: 0, EndMs: 2000}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if quote.Type != "" || quote.Preset != modernPresetFor(OverlayItem{ID: "q", TemplateID: "QUOTE"}, plan.PlanID) {
		t.Fatalf("quote layer = %+v, want modern preset with no type", quote)
	}

	number, err := (NumberRenderer{}).Compile(OverlayItem{ID: "n", Kind: "number", TemplateID: "NUMBER", Text: "47%", StartMs: 0, EndMs: 2000}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if number.Type != "" || number.Preset != modernPresetFor(OverlayItem{ID: "n", TemplateID: "NUMBER"}, plan.PlanID) {
		t.Fatalf("number layer = %+v, want modern preset with no type", number)
	}

	product, err := (ProductRenderer{}).Compile(OverlayItem{
		ID: "p", Kind: "product", TemplateID: "PRODUCT", StartMs: 0, EndMs: 2000,
		AssetRefs: []OverlayAssetRef{{AssetID: "prod", URL: "assets/prod.png", SHA256: "pp"}},
	}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if product.Type != "image" || product.Fit != "contain" || product.BoxWidth != 420 || product.BoxHeight != 420 {
		t.Fatalf("product layer = %+v, want image/contain/420x420", product)
	}

	logo, err := (LogoRenderer{}).Compile(OverlayItem{
		ID: "l", Kind: "logo", TemplateID: "LOGO", StartMs: 0, EndMs: 2000,
		AssetRefs: []OverlayAssetRef{{AssetID: "log", URL: "assets/logo.png", SHA256: "ll"}},
	}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if logo.Type != "image" || logo.Fit != "contain" || logo.BoxWidth != 180 || logo.BoxHeight != 180 {
		t.Fatalf("logo layer = %+v, want image/contain/180x180", logo)
	}
}

// TestRenderers_MissingRequiredInputFailsClosed pins the fail-closed
// contract: a text kind without text, and an image popup without an asset,
// must return a typed error rather than silently render an empty layer.
func TestRenderers_MissingRequiredInputFailsClosed(t *testing.T) {
	plan := testRenderPlan()

	if _, err := (PersonCardRenderer{}).Compile(OverlayItem{ID: "x", Kind: "entity_card", TemplateID: "person_default", StartMs: 0, EndMs: 1000}, plan); err == nil {
		t.Fatal("PersonCardRenderer accepted empty text")
	}
	if _, err := (LowerThirdRenderer{}).Compile(OverlayItem{ID: "x", Kind: "lower_third", TemplateID: "lower_third", StartMs: 0, EndMs: 1000}, plan); err == nil {
		t.Fatal("LowerThirdRenderer accepted empty text")
	}
	if _, err := (QuoteRenderer{}).Compile(OverlayItem{ID: "x", Kind: "quote", TemplateID: "quote", StartMs: 0, EndMs: 1000}, plan); err == nil {
		t.Fatal("QuoteRenderer accepted empty text")
	}
	if _, err := (ImagePopupRenderer{}).Compile(OverlayItem{ID: "x", Kind: "image_popup", TemplateID: "image_popup", StartMs: 0, EndMs: 1000}, plan); err == nil {
		t.Fatal("ImagePopupRenderer accepted missing asset_refs")
	}
	if _, err := (ProductRenderer{}).Compile(OverlayItem{ID: "x", Kind: "product", TemplateID: "PRODUCT", StartMs: 0, EndMs: 1000}, plan); err == nil {
		t.Fatal("ProductRenderer accepted missing asset_refs")
	}
	if _, err := (LogoRenderer{}).Compile(OverlayItem{ID: "x", Kind: "logo", TemplateID: "LOGO", StartMs: 0, EndMs: 1000}, plan); err == nil {
		t.Fatal("LogoRenderer accepted missing asset_refs")
	}
}

// TestRenderers_RegistryResolvesToConcreteRenderer pins the registry
// integration: Resolve(kind) hands back the concrete renderer, and that
// renderer can compile a layer end-to-end.
func TestRenderers_RegistryResolvesToConcreteRenderer(t *testing.T) {
	reg := NewChrononOverlayRegistry()

	entry, err := reg.Resolve("entity_card")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := entry.Renderer.(PersonCardRenderer); !ok {
		t.Fatalf("entity_card renderer type = %T, want PersonCardRenderer", entry.Renderer)
	}

	layer, err := entry.Renderer.Compile(
		OverlayItem{ID: "o", Kind: "entity_card", TemplateID: entry.Template, Text: "Tom Hanks", StartMs: 0, EndMs: 1000},
		testRenderPlan(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Preset != modernPresetFor(OverlayItem{ID: "o", SceneID: "", TemplateID: entry.Template}, testRenderPlan().PlanID) || layer.Text != "Tom Hanks" {
		t.Fatalf("resolved renderer layer = %+v", layer)
	}
}
