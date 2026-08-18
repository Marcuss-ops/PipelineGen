package overlays

import (
	"testing"
)

// entityPlan returns a minimal compilable plan carrying exactly the given
// items (canvas/identity reused from the golden plan so frame math is stable).
func entityPlan(items ...OverlayItem) OverlayPlan {
	plan := GoldenOverlayPlanV1()
	plan.PlanID = "entity-primitives"
	plan.Items = items
	return plan
}

// TestSemanticEntitiesTerminateInCanonicalPrimitives pins the semantic
// entity vocabulary: preset-driven entities (IMPORTANT_PHRASE, PERSON, …)
// resolve to a Chronon preset and emit NO type (Chronon derives it from
// supported_layer); preset-less primitives (PRODUCT / LOGO) still emit their
// image type. Asset-driven entities carry an asset ref, text-driven entities
// carry text.
func TestSemanticEntitiesTerminateInCanonicalPrimitives(t *testing.T) {
	cases := []struct {
		entity     string
		wantType   string
		wantPreset string
		withAsset  bool
	}{
		{"IMPORTANT_PHRASE", "", "caption_card", false},
		{"IMPORTANT_WORD", "", "active_word_pop", false},
		{"IMAGE_OVERLAY", "", "image_focus_in", true},
		{"PERSON", "", "lower_third_safe", false},
		{"NUMBER", "", "active_word_pop", false},
		{"QUOTE", "", "caption_card", false},
		{"LOCATION", "", "location_card", false},
		{"PRODUCT", "image", "", true},
		{"LOGO", "image", "", true},
	}
	for _, tc := range cases {
		item := OverlayItem{
			ID:         "item-" + tc.entity,
			TemplateID: tc.entity,
			Text:       "Test",
			StartMs:    0,
			EndMs:      1000,
		}
		if tc.withAsset {
			item.AssetRefs = []OverlayAssetRef{{AssetID: "asset", URL: "assets/asset.png", SHA256: "aa11"}}
		}
		got, err := CompileChrononPlan(entityPlan(item))
		if err != nil {
			t.Fatalf("%s: compile: %v", tc.entity, err)
		}
		if len(got.Plan.Layers) != 1 {
			t.Fatalf("%s: got %d layers, want 1", tc.entity, len(got.Plan.Layers))
		}
		layer := got.Plan.Layers[0]
		if layer.Type != tc.wantType {
			t.Errorf("%s: layer.Type = %q, want %q (canonical primitive)", tc.entity, layer.Type, tc.wantType)
		}
		if tc.wantPreset != "" && layer.Preset != tc.wantPreset {
			t.Errorf("%s: layer.Preset = %q, want %q", tc.entity, layer.Preset, tc.wantPreset)
		}
	}
}

// TestSemanticEntityAliasesPinConcordance pins that the semantic spellings
// (PERSON/LOCATION/QUOTE) share the concrete template specs the kind registry
// uses (person_default/gpe_default/quote), so the two spellings can never
// drift apart.
func TestSemanticEntityAliasesPinConcordance(t *testing.T) {
	aliases := map[string]string{
		"PERSON":   "person_default",
		"LOCATION": "gpe_default",
		"QUOTE":    "quote",
	}
	for semantic, concrete := range aliases {
		s, okS := templateRegistry[semantic]
		c, okC := templateRegistry[concrete]
		if !okS || !okC {
			t.Fatalf("alias pair %s/%s not fully registered", semantic, concrete)
		}
		// The preset is resolved through the single SemanticOverlayResolver;
		// alias spellings must agree on the same Chronon preset.
		sp, _ := DefaultSemanticOverlayResolver.PresetFor(semantic)
		cp, _ := DefaultSemanticOverlayResolver.PresetFor(concrete)
		if s.LayerType != c.LayerType || sp != cp || s.Fit != c.Fit ||
			s.BoxWidth != c.BoxWidth || s.BoxHeight != c.BoxHeight ||
			s.Primitive != c.Primitive {
			t.Errorf("alias %s diverges from %s:\n got  %+v\n want %+v", semantic, concrete, s, c)
		}
	}
}

// TestSemanticResolverCoversSemanticItemVocabulary pins the editorial mapping
// for every SemanticItem type (the semantic index vocabulary): each resolves
// to a canonical Chronon preset through the single SemanticOverlayResolver —
// no scattered switches, and every role terminates in an EXISTING preset
// (ADR-029: no new Chronon preset is invented in Go).
func TestSemanticResolverCoversSemanticItemVocabulary(t *testing.T) {
	want := map[string]string{
		"PERSON":           "lower_third_safe",
		"ORGANIZATION":     "organization_card",
		"LOCATION":         "location_card",
		"DATE":             "lower_third_safe",
		"MONEY":            "active_word_pop",
		"NUMBER":           "active_word_pop",
		"PERCENTAGE":       "active_word_pop",
		"IMPORTANT_PHRASE": "caption_card",
		"QUOTE":            "caption_card",
		"CLAIM":            "caption_card",
		"STATISTIC":        "active_word_pop",
		"RANKING":          "active_word_pop",
		"TITLE":            "lower_third_safe",
		"EVENT":            "lower_third_safe",
		"IMAGE_ENTITY":     "image_focus_in",
	}
	for role, preset := range want {
		got, ok := DefaultSemanticOverlayResolver.PresetFor(role)
		if !ok {
			t.Errorf("semantic role %q has no preset mapping", role)
			continue
		}
		if got != preset {
			t.Errorf("semantic role %q → %q, want %q", role, got, preset)
		}
	}
}

// TestEveryTemplateDeclaresCanonicalPrimitive pins the fail-closed contract:
// every registered template terminates in exactly one of the four canonical
// primitives — a new entity can never be added without deciding its primitive.
func TestEveryTemplateDeclaresCanonicalPrimitive(t *testing.T) {
	for id, spec := range templateRegistry {
		switch spec.Primitive {
		case PrimitiveText, PrimitiveImage, PrimitiveVideo, PrimitiveShape:
		default:
			t.Errorf("template %q has no canonical primitive (got %q)", id, spec.Primitive)
		}
	}
}

// TestVideoBackgroundCompilesToVideoLayer pins the Video primitive: the
// VIDEO_BACKGROUND template compiles to a Chronon "video" layer whose `source`
// (never `asset`) resolves the content-addressed video, spanning the full
// canvas for the whole clip, and the asset lands in the queue job's assets.
func TestVideoBackgroundCompilesToVideoLayer(t *testing.T) {
	got, err := CompileChrononPlan(entityPlan(OverlayItem{
		ID:         "video_bg",
		TemplateID: "VIDEO_BACKGROUND",
		StartMs:    0,
		EndMs:      5000,
		AssetRefs:  []OverlayAssetRef{{AssetID: "bg", URL: "assets/bg.mp4", SHA256: "bb22"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	layer := got.Plan.Layers[0]
	if layer.Type != "video" {
		t.Fatalf("layer.Type = %q, want video", layer.Type)
	}
	if layer.Source != "assets/bg.mp4" {
		t.Errorf("layer.Source = %q, want assets/bg.mp4", layer.Source)
	}
	if layer.Asset != "" {
		t.Errorf("video layer must reference `source`, not `asset` (got asset=%q)", layer.Asset)
	}
	// Full canvas for the whole clip (5s @ 30fps = 150 frames).
	if layer.StartFrame != 0 || layer.DurationFrames != 150 {
		t.Errorf("video background frame range = [%d +%d], want [0 +150]", layer.StartFrame, layer.DurationFrames)
	}
	if layer.BoxWidth != 1280 || layer.BoxHeight != 720 {
		t.Errorf("video background box = %dx%d, want 1280x720", layer.BoxWidth, layer.BoxHeight)
	}
	found := false
	for _, a := range got.Assets {
		if a.Hash == "bb22" && a.LogicalPath == "assets/bg.mp4" {
			found = true
		}
	}
	if !found {
		t.Fatalf("video asset not projected into queue assets: %+v", got.Assets)
	}
}

// TestShapeCompilesToColorLayer pins the Shape primitive: the SHAPE template
// compiles to a Chronon "color" layer (full-canvas rect) with the template's
// default RGBA, overridable via Params["color"].
func TestShapeCompilesToColorLayer(t *testing.T) {
	got, err := CompileChrononPlan(entityPlan(OverlayItem{
		ID: "accent", TemplateID: "SHAPE", StartMs: 0, EndMs: 2000,
	}))
	if err != nil {
		t.Fatal(err)
	}
	layer := got.Plan.Layers[0]
	if layer.Type != "color" {
		t.Fatalf("layer.Type = %q, want color", layer.Type)
	}
	if len(layer.Color) != 4 || layer.Color[0] != 0 || layer.Color[3] != 0.35 {
		t.Fatalf("layer.Color = %v, want default [0 0 0 0.35]", layer.Color)
	}

	got, err = CompileChrononPlan(entityPlan(OverlayItem{
		ID: "accent", TemplateID: "SHAPE", StartMs: 0, EndMs: 2000,
		Params: map[string]any{"color": []any{1.0, 0.2, 0.2, 0.8}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	layer = got.Plan.Layers[0]
	if len(layer.Color) != 4 || layer.Color[0] != 1.0 || layer.Color[1] != 0.2 || layer.Color[3] != 0.8 {
		t.Fatalf("layer.Color = %v, want Params override [1 0.2 0.2 0.8]", layer.Color)
	}
}

// TestCompileChrononPlanRejectsTemplateWithoutPrimitive pins the fail-closed
// contract at compile time: a template whose spec carries no canonical
// primitive must be rejected (a vocabulary bug, never a silent render).
func TestCompileChrononPlanRejectsTemplateWithoutPrimitive(t *testing.T) {
	plan := entityPlan(OverlayItem{
		ID: "broken", TemplateID: "IMPORTANT_PHRASE", Text: "x", StartMs: 0, EndMs: 1000,
	})
	// Drop the primitive from the registry entry (restore afterwards).
	original := templateRegistry["IMPORTANT_PHRASE"]
	templateRegistry["IMPORTANT_PHRASE"] = TemplateSpec{LayerType: "text"}
	defer func() { templateRegistry["IMPORTANT_PHRASE"] = original }()

	if _, err := CompileChrononPlan(plan); err == nil {
		t.Fatal("expected error for template without canonical primitive")
	}
}
