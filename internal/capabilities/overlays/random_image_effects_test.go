package overlays

import (
	"reflect"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func randomImageEffectItems() []capmodel.OverlayItem {
	return []capmodel.OverlayItem{
		{ID: "photo-a", Kind: "image", TemplateID: "IMAGE_OVERLAY", StartMs: 0, EndMs: 4000, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "a"}}},
		{ID: "photo-b", Kind: "entity_image", TemplateID: "ENTITY_IMAGE", StartMs: 0, EndMs: 4000, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "b"}}},
		{ID: "phrase-c", Kind: "phrase", TemplateID: "IMPORTANT_PHRASE", StartMs: 0, EndMs: 4000, Text: "HELLO"},
		{ID: "photo-d", Kind: "image", TemplateID: "IMAGE_OVERLAY", StartMs: 0, EndMs: 4000, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "d"}},
			Params: map[string]any{"effects": []any{map[string]any{"type": "glow", "params": map[string]any{"radius": 10.0, "intensity": 0.5}}}}},
	}
}

// TestAttachRandomImageEffectsDecoratesOnlyImageSurfaces proves the policy
// decorates image overlays only, never text, and never overrides an explicitly
// authored effect stack.
func TestAttachRandomImageEffectsDecoratesOnlyImageSurfaces(t *testing.T) {
	const runs = 40
	decoratedAcrossRuns := map[string]bool{}
	for run := 0; run < runs; run++ {
		items := randomImageEffectItems()
		AttachRandomImageEffects("plan-"+string(rune('A'+run%10))+string(rune('A'+run/10)), items)
		for _, item := range items {
			effects, hasEffects := item.Params["effects"]
			switch item.ID {
			case "phrase-c":
				if hasEffects {
					t.Fatalf("phrase item %q received runtime effects: %+v", item.ID, effects)
				}
			case "photo-d":
				stack, ok := effects.([]any)
				if !ok || len(stack) != 1 {
					t.Fatalf("explicit effect stack on %q was rewritten: %+v", item.ID, effects)
				}
				if entry := stack[0].(map[string]any); entry["type"] != "glow" {
					t.Fatalf("explicit effect on %q was rewritten: %+v", item.ID, entry)
				}
			default:
				if !hasEffects {
					continue
				}
				decoratedAcrossRuns[item.ID] = true
				stack := effects.([]any)
				if len(stack) != 1 {
					t.Fatalf("item %q must receive exactly one light effect: %+v", item.ID, stack)
				}
				entry := stack[0].(map[string]any)
				wireType, _ := entry["type"].(string)
				params, ok := entry["params"].(map[string]any)
				if wireType == "" || !ok || len(params) == 0 {
					t.Fatalf("item %q received a malformed wire effect: %+v", item.ID, entry)
				}
			}
		}
	}
	if !decoratedAcrossRuns["photo-a"] || !decoratedAcrossRuns["photo-b"] {
		t.Fatalf("across %d runs both image surfaces must be decorated at least once: %v", runs, decoratedAcrossRuns)
	}
}

// TestAttachRandomImageEffectsIsDeterministic proves a plan re-derivation
// replays the exact same decorations (same draw, same effect, same seed).
func TestAttachRandomImageEffectsIsDeterministic(t *testing.T) {
	for run := 0; run < 10; run++ {
		first := randomImageEffectItems()
		second := randomImageEffectItems()
		planID := "replay-" + string(rune('a'+run))
		AttachRandomImageEffects(planID, first)
		AttachRandomImageEffects(planID, second)
		for index := range first {
			a, aOK := first[index].Params["effects"]
			b, bOK := second[index].Params["effects"]
			if aOK != bOK {
				t.Fatalf("item %q draw differs between replays", first[index].ID)
			}
			if aOK && !reflect.DeepEqual(a, b) {
				t.Fatalf("item %q effect stack differs between replays: %+v vs %+v", first[index].ID, a, b)
			}
		}
	}
}

// TestAttachRandomImageEffectsDecoratesCompositeLayersIndependently pins that a
// composite gets one decoration per child layer, never on the shared parent.
func TestAttachRandomImageEffectsDecoratesCompositeLayersIndependently(t *testing.T) {
	items := []capmodel.OverlayItem{{
		ID: "entity-pair", Kind: "entity_image", SceneID: "scene-1", StartMs: 0, EndMs: 5000,
		AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "ada"}, {AssetID: "grace"}},
		ImageLayers: []capmodel.OverlayImageLayer{
			{ID: "ada-layer", AssetID: "ada", StartMS: 0, EndMS: 5000, Params: map[string]any{"position_x": float64(-480)}},
			{ID: "grace-layer", AssetID: "grace", StartMS: 0, EndMS: 5000, Params: map[string]any{"position_x": float64(480)}},
		},
	}}
	const planID = "composite-random-effects"
	AttachRandomImageEffects(planID, items)
	for _, layer := range items[0].ImageLayers {
		effects, ok := layer.Params["effects"].([]any)
		if !ok {
			// The per-image draw may say no; when it says yes the decoration
			// must exist (selection is deterministic for this identity).
			if SelectImageEffectDecorationEnabled(planID, layer.ID) {
				t.Fatalf("layer %q was selected for decoration but has no effects", layer.ID)
			}
			continue
		}
		if len(effects) != 1 {
			t.Fatalf("layer %q effects = %+v, want one independent decoration", layer.ID, effects)
		}
		entry, ok := effects[0].(map[string]any)
		if !ok || entry["type"] == "" {
			t.Fatalf("layer %q has malformed effect: %+v", layer.ID, effects[0])
		}
	}
	if _, leaked := items[0].Params["effects"]; leaked {
		t.Fatal("composite effects must be attached to their child layers, not the shared parent")
	}
}

// TestAttachRandomImageEffectsTreatsRunLevelStackAsExplicit pins the
// interaction with overlay_style.image.effects: the run-level projection
// already wrote params.effects, so the random draw must not replace it.
func TestAttachRandomImageEffectsTreatsRunLevelStackAsExplicit(t *testing.T) {
	item := capmodel.OverlayItem{ID: "photo-style", Kind: "image", TemplateID: "IMAGE_OVERLAY",
		StartMs: 0, EndMs: 4000, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "s"}},
		Params: map[string]any{"effects": []map[string]any{{"type": "vignette", "amount": 0.3}}}}
	items := []capmodel.OverlayItem{item}
	AttachRandomImageEffects("plan-run-level", items)
	stack, ok := items[0].Params["effects"].([]map[string]any)
	if !ok || len(stack) != 1 || stack[0]["type"] != "vignette" {
		t.Fatalf("run-level effect stack was rewritten by the random policy: %+v", items[0].Params["effects"])
	}
}
