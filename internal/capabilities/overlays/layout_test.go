// Package overlays — layout_test.go locks the deterministic canvas layout
// engine and the text auto-fit contract: semantic slot resolution, collision
// avoidance by priority, explicit-position precedence and the character
// budget. The golden byte-equal tests (golden_v1_test / golden_v2_test) keep
// the cross-repo invariant: explicit numeric positions and short phrases are
// untouched by this engine.
package overlays

import (
	"testing"
)

func item(id, template string, start, end int64, params map[string]any, assets ...OverlayAssetRef) OverlayItem {
	it := OverlayItem{
		ID: id, TemplateID: template,
		StartMs: start, EndMs: end,
		Params: params,
	}
	it.AssetRefs = assets
	return it
}

func imageAsset(id string) OverlayAssetRef {
	return OverlayAssetRef{AssetID: id, URL: "assets/" + id + ".png", SHA256: "hash-" + id, MediaType: "image/png"}
}

// TestLayoutSemanticSlotResolvesToCanvasSlot: a string position on an image
// layer resolves to the concrete canvas slot (right band, vertically
// centered, safe margin).
func TestLayoutSemanticSlotResolvesToCanvasSlot(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "layout-slot", VideoID: "v", ProjectID: "p",
		Width: 1280, Height: 720, FPS: 30,
		Items: []OverlayItem{
			item("img", "IMAGE_OVERLAY", 0, 3000, map[string]any{
				"position": "right", "priority": 0.9,
				"box_width": 260, "box_height": 260,
			}, imageAsset("globe")),
		},
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	layer := got.Plan.Layers[0]
	if layer.Position == nil {
		t.Fatalf("expected resolved position, got none")
	}
	// right band: x = 1280 - 48 - 260 = 972, y centered = (720-260)/2 = 230
	if layer.Position[0] != 972 || layer.Position[1] != 230 {
		t.Fatalf("unexpected slot position: %v", layer.Position)
	}
}

// TestLayoutCollisionMovesLowerPriorityToFallbackSlot: two image layers with
// the same semantic slot and overlapping frame ranges must not stack — the
// higher priority keeps the slot, the other moves to the next free slot.
func TestLayoutCollisionMovesLowerPriorityToFallbackSlot(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "layout-collision", VideoID: "v", ProjectID: "p",
		Width: 1280, Height: 720, FPS: 30,
		Items: []OverlayItem{
			item("img_a", "IMAGE_OVERLAY", 0, 4000, map[string]any{
				"position": "right", "priority": 0.5,
				"box_width": 260, "box_height": 260,
			}, imageAsset("a")),
			item("img_b", "IMAGE_OVERLAY", 1000, 5000, map[string]any{
				"position": "right", "priority": 0.9,
				"box_width": 260, "box_height": 260,
			}, imageAsset("b")),
		},
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	a, b := got.Plan.Layers[0], got.Plan.Layers[1]
	// b has higher priority → keeps the right slot; a falls back.
	if b.Position[0] != 972 || b.Position[1] != 230 {
		t.Fatalf("priority layer should keep right slot, got %v", b.Position)
	}
	if a.Position[0] == b.Position[0] && a.Position[1] == b.Position[1] {
		t.Fatalf("collision not resolved: both layers at %v", a.Position)
	}
	// The fallback must be a distinct canvas slot (never identical rect).
	if a.Position == nil || len(a.Position) != 2 {
		t.Fatalf("fallback layer must carry a resolved position, got %v", a.Position)
	}
}

// TestLayoutNonOverlappingSharesSlot: two images in the same semantic slot
// but disjoint time windows may both use it.
func TestLayoutNonOverlappingSharesSlot(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "layout-share", VideoID: "v", ProjectID: "p",
		Width: 1280, Height: 720, FPS: 30,
		Items: []OverlayItem{
			item("img_a", "IMAGE_OVERLAY", 0, 2000, map[string]any{
				"position": "corner", "box_width": 160, "box_height": 160,
			}, imageAsset("a")),
			item("img_b", "IMAGE_OVERLAY", 3000, 5000, map[string]any{
				"position": "corner", "box_width": 160, "box_height": 160,
			}, imageAsset("b")),
		},
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	a, b := got.Plan.Layers[0], got.Plan.Layers[1]
	if a.Position[0] != b.Position[0] || a.Position[1] != b.Position[1] {
		t.Fatalf("disjoint windows should share the slot: a=%v b=%v", a.Position, b.Position)
	}
}

// TestLayoutExplicitNumericPositionWins: an explicit numeric position is
// user intent and is never touched by the layout pass.
func TestLayoutExplicitNumericPositionWins(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "layout-explicit", VideoID: "v", ProjectID: "p",
		Width: 1280, Height: 720, FPS: 30,
		Items: []OverlayItem{
			item("img", "IMAGE_OVERLAY", 0, 3000, map[string]any{
				"position": []any{380, 0}, "box_width": 260, "box_height": 260,
			}, imageAsset("globe")),
		},
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	layer := got.Plan.Layers[0]
	if layer.Position[0] != 380 || layer.Position[1] != 0 {
		t.Fatalf("explicit position must win, got %v", layer.Position)
	}
}

// TestLayoutTemplateDefaultUntouched: an image layer without any position
// param carries NO position — preset-driven images leave placement to
// Chronon's anchor resolver (ADR-029), so there is no Go template default.
func TestLayoutTemplateDefaultUntouched(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "layout-default", VideoID: "v", ProjectID: "p",
		Width: 1280, Height: 720, FPS: 30,
		Items: []OverlayItem{
			item("img", "IMAGE_OVERLAY", 0, 3000, nil, imageAsset("globe")),
		},
	}
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	layer := got.Plan.Layers[0]
	if layer.Position != nil {
		t.Fatalf("preset-driven image must not carry a template-default position, got %v", layer.Position)
	}
	if layer.Type != "image" {
		t.Fatalf("preset-driven image must project canonical primitive type=image, got %q (production validator requires type on every layer)", layer.Type)
	}
	if layer.Fit != "" {
		t.Fatalf("preset-driven image must not carry fit (Chronon derives it), got %q", layer.Fit)
	}
}

// TestLayoutTextNeverAutoLaidOut: text layers keep preset-driven geometry —
// a string position on a text layer is ignored (no Position emitted).
func TestLayoutTextNeverAutoLaidOut(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "layout-text", VideoID: "v", ProjectID: "p",
		Width: 1280, Height: 720, FPS: 30,
		Items: []OverlayItem{
			item("phrase", "IMPORTANT_PHRASE", 0, 3000, map[string]any{"position": "center", "priority": 0.9}),
		},
	}
	// Text items require Text to be meaningful; set it here.
	plan.Items[0].Text = "UN TITOLO"
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	layer := got.Plan.Layers[0]
	if layer.Position != nil {
		t.Fatalf("text layers must not carry a layout position, got %v", layer.Position)
	}
	if layer.Type != "text" || !contains(phrasePresetCandidates, layer.Preset) {
		t.Fatalf("unexpected layer: %s/%s", layer.Type, layer.Preset)
	}
}

// TestCompileEmitsNoFontOrFontSizeForText certifies the preset-ownership
// contract: text layers carry neither font nor font_size — Chronon's
// VisualPresetRegistry + StyleResolver own both.
func TestCompileEmitsNoFontOrFontSizeForText(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "fit-long", VideoID: "v", ProjectID: "p",
		Width: 1280, Height: 720, FPS: 30,
		Items: []OverlayItem{
			item("phrase", "IMPORTANT_PHRASE", 0, 3000, nil),
		},
	}
	plan.Items[0].Text = "UNA FRASE MOLTO LUNGA CHE SUPERA AMPIAMENTE IL BUDGET DI VISUALIZZAZIONE"
	got, err := CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	layer := got.Plan.Layers[0]
	if layer.Font != "" || layer.FontSize != 0 {
		t.Fatalf("text layer must not carry font/font_size: %+v", layer)
	}
	// The queue still carries the canonical font bytes for materialization.
	found := false
	for _, a := range got.Assets {
		if a.Hash == GoldenFontHash && a.LogicalPath == CanonicalTextFontPath {
			found = true
		}
	}
	if !found {
		t.Fatalf("queue assets must carry the canonical font: %+v", got.Assets)
	}
}

// TestLayoutDeterministic: compiling the same plan twice yields identical
// positions (the engine is a pure function of its inputs).
func TestLayoutDeterministic(t *testing.T) {
	build := func() OverlayPlan {
		return OverlayPlan{
			SchemaVersion: SchemaVersionPlan,
			PlanID:        "layout-det", VideoID: "v", ProjectID: "p",
			Width: 1280, Height: 720, FPS: 30,
			Items: []OverlayItem{
				item("a", "IMAGE_OVERLAY", 0, 4000, map[string]any{"position": "right", "priority": 0.4, "box_width": 260, "box_height": 260}, imageAsset("a")),
				item("b", "IMAGE_OVERLAY", 500, 4500, map[string]any{"position": "right", "priority": 0.8, "box_width": 260, "box_height": 260}, imageAsset("b")),
				item("c", "IMAGE_OVERLAY", 1000, 5000, map[string]any{"position": "right", "priority": 0.6, "box_width": 260, "box_height": 260}, imageAsset("c")),
			},
		}
	}
	first, err := CompileChrononPlan(build())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	second, err := CompileChrononPlan(build())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for i := range first.Plan.Layers {
		a, b := first.Plan.Layers[i].Position, second.Plan.Layers[i].Position
		if len(a) != len(b) || (len(a) == 2 && (a[0] != b[0] || a[1] != b[1])) {
			t.Fatalf("non-deterministic layout at layer %d: %v vs %v", i, a, b)
		}
	}
}
