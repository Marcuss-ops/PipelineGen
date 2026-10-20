package overlays

import (
	"testing"
)

// TestImageEffectDecorationVocabularyIsTheEffectsPack pins the decoration
// table to ChrononTemplate's EffectsPack: twenty unique snake_case names, each
// lowering to a wire layer-effect object with a type and parameters.
func TestImageEffectDecorationVocabularyIsTheEffectsPack(t *testing.T) {
	names := ImageEffectDecorationNames()
	if len(names) != 20 {
		t.Fatalf("decoration vocabulary has %d entries, want the 20 EffectsPack kinds: %v", len(names), names)
	}
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || seen[name] {
			t.Fatalf("decoration names must be non-empty and unique: %v", names)
		}
		seen[name] = true
		effect := ImageEffectDecorationEffect(name, 42)
		if effect == nil {
			t.Fatalf("decoration %q produced no wire effect", name)
		}
		wireType, _ := effect["type"].(string)
		if wireType == "" {
			t.Fatalf("decoration %q wire effect has no type: %+v", name, effect)
		}
		params, ok := effect["params"].(map[string]any)
		if !ok || len(params) == 0 {
			t.Fatalf("decoration %q wire effect has no params: %+v", name, effect)
		}
	}
	if ImageEffectDecorationEffect("unknown", 1) != nil {
		t.Fatal("unknown decoration must produce no wire effect")
	}
}

// TestImageEffectDecorationSeedsOnlyNoiseLikeLooks proves the per-item seed is
// injected exactly into the seeded decorations and never into static looks.
func TestImageEffectDecorationSeedsOnlyNoiseLikeLooks(t *testing.T) {
	for _, name := range ImageEffectDecorationNames() {
		effect := ImageEffectDecorationEffect(name, 99)
		params := effect["params"].(map[string]any)
		_, hasSeed := params["seed"]
		if hasSeed != (name == "film_grain" || name == "film_scratches" || name == "floating_dust" || name == "paper_grain") {
			t.Fatalf("decoration %q seed presence = %v, want seeded only for grain/dust looks: %+v", name, hasSeed, params)
		}
		if hasSeed {
			if seed, ok := params["seed"].(uint32); !ok || seed != 99 {
				t.Fatalf("decoration %q seed = %v, want the injected uint32 99", name, params["seed"])
			}
		}
	}
}

// TestSelectImageEffectDecorationIsDeterministicButVaried pins the selection
// contract: same plan+item replays the same draw and decoration, different
// identities get independent draws.
func TestSelectImageEffectDecorationIsDeterministicButVaried(t *testing.T) {
	for i := 0; i < 20; i++ {
		id := string(rune('a' + i))
		enabled := SelectImageEffectDecorationEnabled("plan-1", id)
		if enabled != SelectImageEffectDecorationEnabled("plan-1", id) {
			t.Fatalf("toggle for item %q is not deterministic", id)
		}
		name := SelectImageEffectDecoration("plan-1", id)
		if name != SelectImageEffectDecoration("plan-1", id) {
			t.Fatalf("decoration for item %q is not deterministic", id)
		}
		if enabled && name == "" {
			t.Fatalf("enabled item %q selected no decoration", id)
		}
		if !enabled && SelectImageEffectDecorationEnabled("plan-1", id) {
			t.Fatalf("toggle for item %q flipped between calls", id)
		}
	}
	// A different plan identity is a new draw, never a replay.
	sameAcrossPlans := true
	for i := 0; i < 20 && sameAcrossPlans; i++ {
		id := string(rune('a' + i))
		if SelectImageEffectDecorationEnabled("plan-1", id) != SelectImageEffectDecorationEnabled("plan-2", id) {
			sameAcrossPlans = false
		}
	}
	if sameAcrossPlans {
		t.Fatal("20 items drew identical toggles across two plan identities; the draw is not varying")
	}
}

// TestIsImageOverlayItemVocabulary proves the canonical predicate covers the
// image surfaces and nothing else, with or without explicit kind.
func TestIsImageOverlayItemVocabulary(t *testing.T) {
	image := OverlayItem{Kind: "image", AssetRefs: []OverlayAssetRef{{AssetID: "a"}}}
	if !IsImageOverlayItem(image) {
		t.Fatal("kind image must be an image surface")
	}
	if !IsImageOverlayItem(OverlayItem{Kind: "", TemplateID: "IMAGE_POPUP", ImageLayers: []OverlayImageLayer{{ID: "l"}}}) {
		t.Fatal("image templates with layers must be image surfaces")
	}
	if IsImageOverlayItem(OverlayItem{Kind: "phrase", AssetRefs: []OverlayAssetRef{{AssetID: "a"}}}) {
		t.Fatal("phrase items must not be image surfaces")
	}
	if IsImageOverlayItem(OverlayItem{Kind: "image"}) {
		t.Fatal("assetless image items must not be image surfaces")
	}
}
