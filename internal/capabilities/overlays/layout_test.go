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
// param keeps its template default (legacy fixture compatibility).
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
	if layer.Position[0] != 380 || layer.Position[1] != 0 {
		t.Fatalf("template default must be preserved, got %v", layer.Position)
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
	if layer.Type != "text" || layer.Preset != "title_centered" {
		t.Fatalf("unexpected layer: %s/%s", layer.Type, layer.Preset)
	}
}

// TestAutoFitFontSize: display text within the budget carries no font_size;
// longer text scales down deterministically by character buckets.
func TestAutoFitFontSize(t *testing.T) {
	for _, text := range []string{"QUESTO CAMBIA TUTTO", "IL FUTURO È ADESSO"} {
		size, ok := fitFontSize(displayRunes(text))
		if ok {
			t.Fatalf("%q: expected no override, got %v", text, size)
		}
	}
	// 42 runes → bucket (33,44] → 48.
	got, ok := fitFontSize(displayRunes("UNA FRASE MOLTO LUNGA CHE SUPERA IL BUDGET"))
	if got != 48 || !ok {
		t.Fatalf("42 runes: want 48,true got %v,%v", got, ok)
	}
	for n, want := range map[int]float64{23: 56, 33: 48, 45: 40, 57: 32} {
		size, ok := fitFontSize(n)
		if !ok || size != want {
			t.Fatalf("fitFontSize(%d): want %v,true got %v,%v", n, want, size, ok)
		}
	}
}

// TestCompileEmitsFontSizeForLongText: a long IMPORTANT_PHRASE compiles to a
// text layer carrying the auto-fit font_size; a short one carries none.
func TestCompileEmitsFontSizeForLongText(t *testing.T) {
	longPlan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "fit-long", VideoID: "v", ProjectID: "p",
		Width: 1280, Height: 720, FPS: 30,
		Items: []OverlayItem{
			item("phrase", "IMPORTANT_PHRASE", 0, 3000, nil),
		},
	}
	longPlan.Items[0].Text = "UNA FRASE MOLTO LUNGA CHE SUPERA AMPIAMENTE IL BUDGET DI VISUALIZZAZIONE"
	got, err := CompileChrononPlan(longPlan)
	if err != nil {
		t.Fatalf("compile long: %v", err)
	}
	if got.Plan.Layers[0].FontSize == 0 {
		t.Fatalf("long text must carry font_size override")
	}

	shortPlan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "fit-short", VideoID: "v", ProjectID: "p",
		Width: 1280, Height: 720, FPS: 30,
		Items: []OverlayItem{
			item("phrase", "IMPORTANT_PHRASE", 0, 3000, nil),
		},
	}
	shortPlan.Items[0].Text = "CORTO"
	got, err = CompileChrononPlan(shortPlan)
	if err != nil {
		t.Fatalf("compile short: %v", err)
	}
	if got.Plan.Layers[0].FontSize != 0 {
		t.Fatalf("short text must not carry font_size override")
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
