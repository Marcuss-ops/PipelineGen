package overlays

import (
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
func TestCoalesceDisabledByDefault(t *testing.T) {
	items := []OverlayItem{
		chainFixture("text_phrase", "s1", "alpha", 100_000, 500_000, "motion_a"),
		chainFixture("text_phrase", "s1", "beta", 700_000, 500_000, "motion_b"),
	}
	got := CoalesceChainedOverlays(items, nil)
	if len(got) != 2 || got[0].Kind != "text_phrase" {
		t.Fatalf("legacy path changed without chain keys: %+v", got)
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
	got := CoalesceChainedOverlays(items, map[string]int{"chain_gap_ms": 600})
	if len(got) != 2 {
		t.Fatalf("got %d items, want chain + standalone: %+v", len(got), got)
	}
	chain := got[0]
	if chain.Kind != KindPhraseChain || chain.TemplateID != TemplatePhraseChain {
		t.Fatalf("chain kind/template = %q/%q", chain.Kind, chain.TemplateID)
	}
	if chain.StartUSValue() != 100_000 || chain.EndUSValue() != 1_200_000 {
		t.Fatalf("chain window = [%d,%d)", chain.StartUSValue(), chain.EndUSValue())
	}
	segs, ok := chain.Params["chain_segments"].([]ChainSegment)
	if !ok || len(segs) != 2 {
		t.Fatalf("chain segments = %+v", chain.Params["chain_segments"])
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

// Chains never cross scenes and never mix text with images.
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
	for _, item := range got {
		if strings.Contains(item.Kind, "chain") {
			t.Fatalf("must not chain across scenes/families: %+v", got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %d items, want 3 standalone", len(got))
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
