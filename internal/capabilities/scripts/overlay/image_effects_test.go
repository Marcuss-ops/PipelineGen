package overlay

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func imageEffectItems() []capabilityoverlay.OverlayItem {
	return []capabilityoverlay.OverlayItem{
		{ID: "photo-a", Kind: "image", TemplateID: "IMAGE_OVERLAY", StartMs: 0, EndMs: 4000, AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "a"}}},
		{ID: "photo-b", Kind: "entity_image", TemplateID: "ENTITY_IMAGE", StartMs: 0, EndMs: 4000, AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "b"}}},
		{ID: "phrase-c", Kind: "phrase", TemplateID: "IMPORTANT_PHRASE", StartMs: 0, EndMs: 4000, Text: "HELLO"},
		{ID: "photo-d", Kind: "image", TemplateID: "IMAGE_OVERLAY", StartMs: 0, EndMs: 4000, AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "d"}},
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
		items := imageEffectItems()
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
				entry := stack[0].(map[string]any)
				if entry["type"] != "glow" {
					t.Fatalf("explicit effect on %q was rewritten: %+v", item.ID, entry)
				}
			default:
				if hasEffects {
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
	}
	if !decoratedAcrossRuns["photo-a"] || !decoratedAcrossRuns["photo-b"] {
		t.Fatalf("across %d runs both image surfaces must be decorated at least once: %v", runs, decoratedAcrossRuns)
	}
}

// TestAttachRandomImageEffectsIsDeterministic proves a plan re-derivation
// replays the exact same decorations (same draw, same effect, same seed).
func TestAttachRandomImageEffectsDecoratesEachCompositeLayerIndependently(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{{
		ID: "entity-pair", Kind: "entity_image", SceneID: "scene-1", StartMs: 0, EndMs: 5000,
		AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "ada"}, {AssetID: "grace"}},
		ImageLayers: []capabilityoverlay.OverlayImageLayer{
			{ID: "ada-layer", AssetID: "ada", StartMS: 0, EndMS: 5000, Params: map[string]any{"position_x": float64(-480)}},
			{ID: "grace-layer", AssetID: "grace", StartMS: 0, EndMS: 5000, Params: map[string]any{"position_x": float64(480)}},
		},
	}}
	const planID = "composite-random-effects"
	AttachRandomImageEffects(planID, items)
	for _, layer := range items[0].ImageLayers {
		effects, ok := layer.Params["effects"].([]any)
		if !ok {
			// Randomized policy may intentionally leave a surface undecorated;
			// when enabled, selection is deterministic for this layer identity.
			if capabilityoverlay.SelectImageEffectDecorationEnabled(planID, layer.ID) {
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

func TestAttachRandomImageEffectsIsDeterministic(t *testing.T) {
	for run := 0; run < 10; run++ {
		first := imageEffectItems()
		second := imageEffectItems()
		planID := "replay-" + string(rune('a'+run))
		AttachRandomImageEffects(planID, first)
		AttachRandomImageEffects(planID, second)
		for index := range first {
			a, aOK := first[index].Params["effects"]
			b, bOK := second[index].Params["effects"]
			if aOK != bOK {
				t.Fatalf("item %q draw differs between replays", first[index].ID)
			}
			if aOK && !equalEffects(a, b) {
				t.Fatalf("item %q effect stack differs between replays: %+v vs %+v", first[index].ID, a, b)
			}
		}
	}
}

func equalEffects(a, b any) bool {
	x, xOK := a.([]any)
	y, yOK := b.([]any)
	if !xOK || !yOK || len(x) != len(y) {
		return false
	}
	for i := range x {
		xe, xOK := x[i].(map[string]any)
		ye, yOK := y[i].(map[string]any)
		if !xOK || !yOK || xe["type"] != ye["type"] {
			return false
		}
		xp, _ := xe["params"].(map[string]any)
		yp, _ := ye["params"].(map[string]any)
		if len(xp) != len(yp) {
			return false
		}
		for key, xv := range xp {
			yv, exists := yp[key]
			if !exists || !equalParam(xv, yv) {
				return false
			}
		}
	}
	return true
}

func equalParam(a, b any) bool {
	if af, ok := a.(float64); ok {
		bf, ok := b.(float64)
		return ok && af == bf
	}
	if as, ok := a.(uint32); ok {
		bs, ok := b.(uint32)
		return ok && as == bs
	}
	if ab, ok := a.(bool); ok {
		bb, ok := b.(bool)
		return ok && ab == bb
	}
	if aa, ok := a.([]float64); ok {
		ba, ok := b.([]float64)
		if !ok || len(aa) != len(ba) {
			return false
		}
		for i := range aa {
			if aa[i] != ba[i] {
				return false
			}
		}
		return true
	}
	return a == b
}
