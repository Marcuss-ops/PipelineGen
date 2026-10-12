package overlays

import (
	"encoding/json"
	"strings"
	"testing"
)

func chainFixture(kind, sceneID, text string, startUS, durationUS int64, motion string) OverlayItem {
	return OverlayItem{
		ID: sceneID + "-phrase-" + text, SceneID: sceneID, Kind: kind,
		TemplateID: "IMPORTANT_PHRASE", PresetID: "phrase_default", MotionID: motion,
		Text:    text,
		StartMs: startUS / 1000, EndMs: (startUS + durationUS + 999) / 1000,
		StartUS: startUS, DurationUS: durationUS,
	}
}

// Without chain keys the legacy path is intact: items pass through untouched.
// Production enables chaining via cloneAnimationCounts (default 2500ms gap).
func TestCoalesceDisabledByDefault(t *testing.T) {
	items := []OverlayItem{
		chainFixture("text_phrase", "s1", "alpha", 100_000, 500_000, "motion_a"),
		chainFixture("text_phrase", "s1", "beta", 700_000, 500_000, "motion_b"),
	}
	got := CoalesceChainedOverlays(items, nil)
	if len(got) != 2 || got[0].Kind != "text_phrase" {
		t.Fatalf("legacy path changed without chain keys: %+v", got)
	}
	off := CoalesceChainedOverlays(items, map[string]int{"chain_enabled": 1, "chain_disabled": 1})
	if len(off) != 2 || off[0].Kind != "text_phrase" {
		t.Fatalf("chain_disabled must keep legacy items: %+v", off)
	}
}

// With chain_gap_ms, nearby phrases fuse into one phrase_chain carrying both
// motions, while a far phrase stays standalone.
func TestCoalesceFusesNearbyPhrases(t *testing.T) {
	items := []OverlayItem{
		chainFixture("text_phrase", "s1", "alpha", 100_000, 500_000, "motion_a"),
		chainFixture("text_phrase", "s1", "beta", 700_000, 500_000, "motion_b"),
		chainFixture("text_phrase", "s1", "gamma", 5_000_000, 500_000, "motion_c"),
	}
	items[0].Params = map[string]any{"font_family": "Inter", "font_size_px": 72}
	got := CoalesceChainedOverlays(items, map[string]int{"chain_gap_ms": 600})
	if len(got) != 2 {
		t.Fatalf("got %d items, want chain + standalone: %+v", len(got), got)
	}
	chain := got[0]
	if chain.Kind != KindPhraseChain || chain.TemplateID != TemplatePhraseChain {
		t.Fatalf("chain kind/template = %q/%q", chain.Kind, chain.TemplateID)
	}
	if chain.PresetID != "" || chain.MotionID != "" {
		t.Fatalf("chain container must not copy a child preset or motion: preset=%q motion=%q", chain.PresetID, chain.MotionID)
	}
	if chain.StartUSValue() != 100_000 || chain.EndUSValue() != 1_200_000 {
		t.Fatalf("chain window = [%d,%d)", chain.StartUSValue(), chain.EndUSValue())
	}
	segs, ok := chain.Params["chain_segments"].([]ChainSegment)
	if !ok || len(segs) != 2 {
		t.Fatalf("chain segments = %+v", chain.Params["chain_segments"])
	}
	if _, leaked := chain.Params["font_family"]; leaked {
		t.Fatal("chain container must not inherit a child's font family")
	}
	if segs[0].Params["font_family"] != "Inter" {
		t.Fatalf("segment runtime style lost: %+v", segs[0].Params)
	}
	if segs[0].MotionID != "motion_a" || segs[1].MotionID != "motion_b" {
		t.Fatalf("segment motions lost: %+v", segs)
	}
	if chain.Params["chain_camera"] != ChainCameraFlyUnderFade {
		t.Fatalf("chain camera = %v", chain.Params["chain_camera"])
	}
	if got[1].Text != "gamma" || got[1].Kind != "text_phrase" {
		t.Fatalf("far phrase must stay standalone: %+v", got[1])
	}
}

// Chains never cross scenes; text+image in the same scene fuse as mixed_chain.
func TestCoalesceRespectsSceneAndFamily(t *testing.T) {
	img := chainFixture("text_phrase", "s1", "alpha", 100_000, 500_000, "motion_a")
	img.Kind, img.TemplateID = "image", "IMAGE_OVERLAY"
	img.AssetRefs = []OverlayAssetRef{{AssetID: "img-1", SHA256: "hash"}}
	items := []OverlayItem{
		chainFixture("text_phrase", "s1", "alpha", 100_000, 500_000, "motion_a"),
		chainFixture("text_phrase", "s2", "beta", 700_000, 500_000, "motion_b"),
		img,
	}
	got := CoalesceChainedOverlays(items, map[string]int{"chain_enabled": 1})
	if len(got) != 2 {
		t.Fatalf("same-scene image+phrase must fuse, cross-scene must not: %+v", got)
	}
	foundMixed := false
	for _, item := range got {
		if item.Kind == KindMixedChain {
			foundMixed = true
			if item.TemplateID != TemplateMixedChain {
				t.Fatalf("mixed chain template = %q", item.TemplateID)
			}
		}
		if item.SceneID == "s2" && strings.Contains(item.Kind, "chain") {
			t.Fatalf("must not chain across scenes: %+v", got)
		}
	}
	if !foundMixed {
		t.Fatalf("want one mixed_chain: %+v", got)
	}
}

// Image@10s + phrase@12s (overlapping windows) fuse into one mixed_chain.
func TestCoalesceFusesOverlappingImagePhrase(t *testing.T) {
	img := chainFixture("text_phrase", "s1", "pic", 10_000_000, 5_000_000, "img_motion")
	img.Kind, img.TemplateID = "image", "IMAGE_OVERLAY"
	img.Text = ""
	img.AssetRefs = []OverlayAssetRef{{AssetID: "img-1", SHA256: "hash"}}
	phrase := chainFixture("text_phrase", "s1", "la frase", 12_000_000, 3_000_000, "phrase_motion")
	got := CoalesceChainedOverlays([]OverlayItem{img, phrase}, map[string]int{"chain_enabled": 1})
	if len(got) != 1 || got[0].Kind != KindMixedChain {
		t.Fatalf("overlapping image+phrase must fuse: %+v", got)
	}
	if len(got[0].AssetRefs) != 1 || strings.TrimSpace(got[0].Text) == "" {
		t.Fatalf("mixed chain must carry image asset + phrase text: %+v", got[0])
	}
}

// Number + phrase share the text family and fuse as phrase_chain.
func TestCoalesceFusesNumberPhrase(t *testing.T) {
	num := chainFixture("number", "s1", "42", 10_000_000, 2_000_000, "metric_counter_rise")
	num.TemplateID = "NUMBER"
	phrase := chainFixture("text_phrase", "s1", "la frase", 11_000_000, 2_000_000, "phrase_motion")
	got := CoalesceChainedOverlays([]OverlayItem{num, phrase}, map[string]int{"chain_enabled": 1})
	if len(got) != 1 || got[0].Kind != KindPhraseChain {
		t.Fatalf("number+phrase must fuse as phrase_chain: %+v", got)
	}
}

// Entity card + phrase fuse as mixed_chain keeping caption and text.
func TestCoalesceFusesCardPhrase(t *testing.T) {
	card := chainFixture("entity_card", "s1", "", 10_000_000, 3_000_000, "card_motion")
	card.TemplateID = "person_default"
	card.Text = ""
	card.EntityCaption = "Ada"
	phrase := chainFixture("text_phrase", "s1", "la frase", 11_000_000, 2_000_000, "phrase_motion")
	got := CoalesceChainedOverlays([]OverlayItem{card, phrase}, map[string]int{"chain_enabled": 1})
	if len(got) != 1 || got[0].Kind != KindMixedChain {
		t.Fatalf("card+phrase must fuse as mixed_chain: %+v", got)
	}
	segs, ok := got[0].Params["chain_segments"].([]ChainSegment)
	if !ok || len(segs) != 2 || segs[0].EntityCaption != "Ada" || segs[1].Text != "la frase" {
		t.Fatalf("mixed segments must keep card caption + phrase text: %+v", got[0].Params["chain_segments"])
	}
}

// Map + phrase fuse as mixed_chain keeping the georeference per segment.
func TestCoalesceFusesMapPhrase(t *testing.T) {
	m := chainFixture("map", "s1", "", 10_000_000, 5_000_000, "map_motion")
	m.TemplateID = "MAP"
	m.Text = ""
	m.Map = &MapOverlay{Provider: "local"}
	m.AssetRefs = []OverlayAssetRef{{AssetID: "basemap", SHA256: "hash"}}
	phrase := chainFixture("text_phrase", "s1", "la frase", 12_000_000, 3_000_000, "phrase_motion")
	got := CoalesceChainedOverlays([]OverlayItem{m, phrase}, map[string]int{"chain_enabled": 1})
	if len(got) != 1 || got[0].Kind != KindMixedChain {
		t.Fatalf("map+phrase must fuse as mixed_chain: %+v", got)
	}
	segs, ok := got[0].Params["chain_segments"].([]ChainSegment)
	if !ok || len(segs) != 2 || segs[0].Map == nil {
		t.Fatalf("mixed segments must keep the map payload: %+v", got[0].Params["chain_segments"])
	}
}

// Two nearby maps fuse as map_chain.
func TestCoalesceFusesMapMap(t *testing.T) {
	mk := func(start int64) OverlayItem {
		m := chainFixture("map", "s1", "", start, 3_000_000, "map_motion")
		m.TemplateID = "MAP"
		m.Text = ""
		m.Map = &MapOverlay{Provider: "local"}
		m.AssetRefs = []OverlayAssetRef{{AssetID: "basemap", SHA256: "hash"}}
		return m
	}
	got := CoalesceChainedOverlays([]OverlayItem{mk(10_000_000), mk(12_000_000)}, map[string]int{"chain_enabled": 1})
	if len(got) != 1 || got[0].Kind != KindMapChain || got[0].TemplateID != TemplateMapChain {
		t.Fatalf("map+map must fuse as map_chain: %+v", got)
	}
}

// A fused mixed chain with map + phrase validates; maps without payload fail.
func TestChainValidationMixedMap(t *testing.T) {
	m := chainFixture("map", "s1", "", 10_000_000, 5_000_000, "map_motion")
	m.TemplateID = "MAP"
	m.Text = ""
	m.Map = &MapOverlay{Provider: "local"}
	m.AssetRefs = []OverlayAssetRef{{AssetID: "basemap", SHA256: "hash"}}
	phrase := chainFixture("text_phrase", "s1", "la frase", 12_000_000, 3_000_000, "phrase_motion")
	chain := CoalesceChainedOverlays([]OverlayItem{m, phrase}, map[string]int{"chain_enabled": 1})[0]
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan, PlanID: "p", VideoID: "v",
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Items: []OverlayItem{chain},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("fused mixed map chain must validate: %v", err)
	}
}

// chain_max_segments bounds the chain; chain_disabled forces legacy output.
func TestCoalesceHonorsLimits(t *testing.T) {
	items := make([]OverlayItem, 4)
	for i := range items {
		items[i] = chainFixture("text_phrase", "s1", string(rune('a'+i)), int64(i)*600_000, 500_000, "motion_a")
	}
	got := CoalesceChainedOverlays(items, map[string]int{"chain_gap_ms": 1000, "chain_max_segments": 2})
	if len(got) != 2 {
		t.Fatalf("got %d items, want two 2-segment chains", len(got))
	}
	for _, item := range got {
		if item.Kind != KindPhraseChain {
			t.Fatalf("expected chains: %+v", got)
		}
	}
	off := CoalesceChainedOverlays(items, map[string]int{"chain_gap_ms": 1000, "chain_disabled": 1})
	if len(off) != 4 {
		t.Fatalf("chain_disabled must keep legacy items")
	}
}

// A fused chain validates; a hand-built malformed chain fails closed.
func TestChainValidation(t *testing.T) {
	items := []OverlayItem{
		chainFixture("text_phrase", "s1", "alpha", 100_000, 500_000, "motion_a"),
		chainFixture("text_phrase", "s1", "beta", 700_000, 500_000, "motion_b"),
	}
	chain := CoalesceChainedOverlays(items, map[string]int{"chain_gap_ms": 600})[0]
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan, PlanID: "p", VideoID: "v",
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Items: []OverlayItem{chain},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("fused chain must validate: %v", err)
	}
	broken := chain
	broken.Params = map[string]any{}
	badPlan := plan
	badPlan.Items = []OverlayItem{broken}
	badPlan.Fingerprint = ""
	if err := badPlan.Validate(); err == nil {
		t.Fatal("chain without segments must fail closed")
	}
}

// A chain that crossed JSON (durable runs, child-plan splits) decodes its
// segments as []any: validation must still accept it. Regression test for
// the live "needs at least two segments" failure on healthy chains.
func TestValidateChainItemAfterJSONRoundTrip(t *testing.T) {
	items := []OverlayItem{
		chainFixture("text_phrase", "s1", "alpha", 60_000_000, 1_500_000, "motion_a"),
		chainFixture("text_phrase", "s1", "beta", 62_000_000, 1_500_000, "motion_b"),
	}
	chain := CoalesceChainedOverlays(items, map[string]int{"chain_gap_ms": 2500})[0]
	raw, err := json.Marshal(chain)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded OverlayItem
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Params["chain_segments"].([]ChainSegment); ok {
		t.Fatal("round-trip should decode segments as generic values, test is vacuous")
	}
	if err := ValidateChainItem(reloaded); err != nil {
		t.Fatalf("deserialized chain must validate: %v", err)
	}
	segs, ok := DecodeChainSegments(reloaded.Params)
	if !ok || len(segs) != 2 || segs[0].MotionID != "motion_a" {
		t.Fatalf("decoded segments = %+v, ok=%v", segs, ok)
	}
}
