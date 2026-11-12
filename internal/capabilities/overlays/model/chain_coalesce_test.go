package model

import (
	"encoding/json"
	"fmt"
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

// Without chain keys the legacy direct-call path is intact: items pass through untouched.
// Production enables chaining via cloneAnimationCounts (default 5000ms gap).
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
	if chain.StartUSValue() != 100_000 || chain.EndUSValue() != 1_500_000 {
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

// A chart comparison is atomic through coalescing: the label and its bars
// stay standalone even when nearby phrases fuse, so the group survives (or
// is pruned) as a unit instead of entombing the label and orphaning bars.
func TestCoalesceKeepsChartGroupAtomic(t *testing.T) {
	label := chainFixture("text_phrase", "s1", "42%\n18%", 5_412_000, 7_563_000, "motion_chart")
	label.TemplateID = "IMPORTANT_PHRASE"
	label.Params = map[string]any{"chart_card": true, "chart_group_id": "g1"}
	bar := chainFixture("shape", "s1", "", 5_412_000, 7_563_000, "")
	bar.TemplateID = "SHAPE"
	bar.Params = map[string]any{"shape": "rect", "chart_bar": true, "chart_group_id": "g1"}
	items := []OverlayItem{
		chainFixture("text_phrase", "s1", "alpha", 1_987_000, 11_301_000, "motion_a"),
		label,
		chainFixture("text_phrase", "s1", "beta", 6_000_000, 5_000_000, "motion_b"),
		bar,
	}
	got := CoalesceChainedOverlays(items, map[string]int{"chain_gap_ms": 5000})
	var labelOut, barOut *OverlayItem
	for i := range got {
		if got[i].Params["chart_card"] == true {
			labelOut = &got[i]
		}
		if got[i].Params["chart_bar"] == true {
			barOut = &got[i]
		}
		if got[i].Kind == KindPhraseChain {
			segs, ok := got[i].Params["chain_segments"].([]ChainSegment)
			if !ok {
				t.Fatalf("chain segments undecodable: %+v", got[i].Params["chain_segments"])
			}
			for _, seg := range segs {
				if seg.Params["chart_card"] == true || seg.Params["chart_bar"] == true {
					t.Fatalf("chart item entombed in chain: %+v", seg)
				}
			}
		}
	}
	if labelOut == nil || labelOut.Kind != "text_phrase" {
		t.Fatalf("chart label must stay standalone: %+v", got)
	}
	if barOut == nil || barOut.Kind != "shape" {
		t.Fatalf("chart bar must stay standalone: %+v", got)
	}
}

// Image and text remain separate; text chains never cross scenes.
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
	if len(got) != 3 {
		t.Fatalf("image and two scene-scoped phrases must remain separate: %+v", got)
	}
	images, phrases := 0, 0
	for _, item := range got {
		if chainFamily(item.Kind) == "image" {
			images++
		}
		if item.Kind == "text_phrase" {
			phrases++
		}
		if item.SceneID == "s2" && strings.Contains(item.Kind, "chain") {
			t.Fatalf("must not chain across scenes: %+v", got)
		}
	}
	if images != 1 || phrases != 2 {
		t.Fatalf("want one image and two separate phrases, got %+v", got)
	}
}

// Overlapping image and phrase windows still remain separate layers.
func TestCoalesceFusesOverlappingImagePhrase(t *testing.T) {
	img := chainFixture("text_phrase", "s1", "pic", 10_000_000, 5_000_000, "img_motion")
	img.Kind, img.TemplateID = "image", "IMAGE_OVERLAY"
	img.Text = ""
	img.AssetRefs = []OverlayAssetRef{{AssetID: "img-1", SHA256: "hash"}}
	phrase := chainFixture("text_phrase", "s1", "la frase", 12_000_000, 3_000_000, "phrase_motion")
	got := CoalesceChainedOverlays([]OverlayItem{img, phrase}, map[string]int{"chain_enabled": 1})
	if len(got) != 2 || got[0].Kind != "image" || got[1].Kind != "text_phrase" {
		t.Fatalf("overlapping image+phrase must remain distinct: %+v", got)
	}
	if len(got[0].AssetRefs) != 1 || got[1].Text != "la frase" {
		t.Fatalf("separate image and phrase lost their content: %+v", got)
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

// Map + phrase are standalone by contract: a map owns the full georeferenced
// canvas and never fuses into a text chain.
func TestCoalesceKeepsMapPhraseStandalone(t *testing.T) {
	m := chainFixture("map", "s1", "", 10_000_000, 5_000_000, "map_motion")
	m.TemplateID = "MAP"
	m.Text = ""
	m.Map = &MapOverlay{Provider: "local"}
	m.AssetRefs = []OverlayAssetRef{{AssetID: "basemap", SHA256: "hash"}}
	phrase := chainFixture("text_phrase", "s1", "la frase", 12_000_000, 3_000_000, "phrase_motion")
	got := CoalesceChainedOverlays([]OverlayItem{m, phrase}, map[string]int{"chain_enabled": 1})
	if len(got) != 2 {
		t.Fatalf("map+phrase must stay two standalone items: %+v", got)
	}
	if got[0].Kind != "map" || got[1].Kind != "text_phrase" {
		t.Fatalf("map+phrase kinds = %q/%q, want map/text_phrase", got[0].Kind, got[1].Kind)
	}
	if IsChainItemKind(got[0].Kind) || IsChainItemKind(got[1].Kind) {
		t.Fatalf("map+phrase must not create a chain: %+v", got)
	}
}

func TestCoalesceKeepsShortTextBeforeMapReadable(t *testing.T) {
	phrase := chainFixture("text_phrase", "s1", "Procura di Pavia", 10_000_000, 762_500, "phrase_motion")
	mapItem := chainFixture("map", "s1", "", 10_762_500, 8_000_000, "")
	mapItem.TemplateID = "MAP"
	mapItem.Map = &MapOverlay{Provider: "local"}
	mapItem.AssetRefs = []OverlayAssetRef{{AssetID: "basemap", SHA256: "hash"}}
	got := CoalesceChainedOverlays([]OverlayItem{phrase, mapItem}, map[string]int{"chain_enabled": 1})
	if len(got) != 2 {
		t.Fatalf("expected standalone phrase + map, got %+v", got)
	}
	if got[0].Kind != "text_phrase" || got[0].StartUSValue() != 10_000_000 || got[0].EndUSValue() != 10_762_500 {
		t.Fatalf("short phrase must keep its certified window: %+v", got[0])
	}
	if got[1].Kind != "map" || got[1].StartUSValue() != 10_762_500 {
		t.Fatalf("map must stay standalone after the phrase: %+v", got[1])
	}
}

// Two nearby maps must remain two standalone map items; automatic map chains
// are not part of the canonical contract.
func TestCoalesceKeepsMapsStandalone(t *testing.T) {
	mk := func(start int64) OverlayItem {
		m := chainFixture("map", "s1", "", start, 3_000_000, "map_motion")
		m.TemplateID = "MAP"
		m.Text = ""
		m.Map = &MapOverlay{Provider: "local"}
		m.AssetRefs = []OverlayAssetRef{{AssetID: "basemap", SHA256: "hash"}}
		return m
	}
	got := CoalesceChainedOverlays([]OverlayItem{mk(10_000_000), mk(12_000_000)}, map[string]int{"chain_enabled": 1})
	if len(got) != 2 {
		t.Fatalf("map+map must stay two standalone maps: %+v", got)
	}
	for i, item := range got {
		if item.Kind != "map" || IsChainItemKind(item.Kind) {
			t.Fatalf("map item %d = %q, want standalone map", i, item.Kind)
		}
	}
}

// A hand-built mixed chain carrying a map fails closed: maps cannot enter an
// automatic text/image/mixed chain.
func TestChainValidationRejectsMapInMixedChain(t *testing.T) {
	m := chainFixture("map", "s1", "", 10_000_000, 5_000_000, "map_motion")
	m.TemplateID = "MAP"
	m.Text = ""
	m.Map = &MapOverlay{Provider: "local"}
	m.AssetRefs = []OverlayAssetRef{{AssetID: "basemap", SHA256: "hash"}}
	phrase := chainFixture("text_phrase", "s1", "la frase", 15_000_000, 3_000_000, "phrase_motion")
	chain := OverlayItem{
		ID: "s1:chain-mixed:0", SceneID: "s1", Kind: KindMixedChain, TemplateID: TemplateMixedChain,
		StartMs: 10_000, EndMs: 18_000, StartUS: 10_000_000, DurationUS: 8_000_000,
		Params: map[string]any{"chain_segments": []ChainSegment{
			{Kind: "map", TemplateID: "MAP", StartUS: 10_000_000, DurationUS: 5_000_000, Map: m.Map, Assets: m.AssetRefs},
			{Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: phrase.Text, MotionID: phrase.MotionID, StartUS: 15_000_000, DurationUS: 3_000_000},
		}},
	}
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan, PlanID: "p", VideoID: "v",
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Items: []OverlayItem{chain},
	}
	if err := plan.Validate(); err == nil {
		t.Fatal("mixed chain carrying a map must fail closed")
	}
}

// Five nearby images must become one image_chain with five independently
// preserved identities and motions, never a single composite image.
func TestCoalesceFiveImagesPreserveFiveIdentitiesAndMotions(t *testing.T) {
	motions := []string{
		"image_focus_reveal",
		"image_roll_depth_in",
		"image_photo_drop",
		"image_spotlight_focus",
		"image_parallax_frame",
	}
	items := make([]OverlayItem, 0, len(motions))
	for i, motion := range motions {
		item := chainFixture("image", "s1", "", int64(i)*3_000_000, 2_800_000, motion)
		item.TemplateID = "IMAGE_OVERLAY"
		item.PresetID = "image_scale_in"
		item.Text = ""
		item.AssetRefs = []OverlayAssetRef{{
			AssetID: fmt.Sprintf("img-%d", i),
			SHA256:  strings.Repeat(fmt.Sprintf("%x", i+1), 64),
		}}
		items = append(items, item)
	}
	got := CoalesceChainedOverlays(items, map[string]int{"chain_enabled": 1})
	if len(got) != 1 {
		t.Fatalf("five nearby images must produce one chain, got %d: %+v", len(got), got)
	}
	chain := got[0]
	if chain.Kind != KindImageChain || chain.TemplateID != TemplateImageChain {
		t.Fatalf("chain kind/template = %q/%q, want image_chain/IMAGE_CHAIN", chain.Kind, chain.TemplateID)
	}
	segments, ok := DecodeChainSegments(chain.Params)
	if !ok || len(segments) != 5 {
		t.Fatalf("chain segments = %+v, want five", chain.Params["chain_segments"])
	}
	seenAssets, seenMotions := map[string]bool{}, map[string]bool{}
	for i, seg := range segments {
		if seg.AssetID != fmt.Sprintf("img-%d", i) || len(seg.Assets) != 1 || seg.Assets[0].AssetID != seg.AssetID {
			t.Fatalf("segment %d lost its own asset identity: %+v", i, seg)
		}
		if seg.MotionID != motions[i] {
			t.Fatalf("segment %d motion = %q, want %q", i, seg.MotionID, motions[i])
		}
		if seenAssets[seg.AssetID] || seenMotions[seg.MotionID] {
			t.Fatalf("segment %d repeated an asset or motion: assets=%v motions=%v", i, seenAssets, seenMotions)
		}
		seenAssets[seg.AssetID], seenMotions[seg.MotionID] = true, true
	}
	if len(chain.AssetRefs) != 5 {
		t.Fatalf("chain-level asset refs = %d, want five distinct source images", len(chain.AssetRefs))
	}
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan, PlanID: "five-images", VideoID: "v",
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Items: []OverlayItem{chain},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("five-image chain must validate: %v", err)
	}
}

// chain_max_segments bounds the chain; chain_disabled forces legacy output.
// A five-image chain is intentionally not split by an implicit ceiling.
func TestCoalesceFiveImagesIsNotSplitByImplicitCeiling(t *testing.T) {
	items := make([]OverlayItem, 0, 5)
	for i := 0; i < 5; i++ {
		item := chainFixture("image", "s1", "", int64(i)*2_700_000, 2_600_000, fmt.Sprintf("image_motion_%d", i))
		item.TemplateID = "IMAGE_OVERLAY"
		item.Text = ""
		item.AssetRefs = []OverlayAssetRef{{AssetID: fmt.Sprintf("asset-%d", i), SHA256: "hash"}}
		items = append(items, item)
	}
	got := CoalesceChainedOverlays(items, map[string]int{"chain_enabled": 1})
	if len(got) != 1 {
		t.Fatalf("five images must remain one chain without an explicit ceiling, got %d", len(got))
	}
	segments, ok := DecodeChainSegments(got[0].Params)
	if !ok || len(segments) != 5 {
		t.Fatalf("chain segments = %+v, want five", got[0].Params["chain_segments"])
	}
}

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

// The production default fuses at a five-second empty interval, excludes the
// first item beyond the boundary, and still respects per-scene isolation.
func TestCoalesceFiveSecondDefaultBoundary(t *testing.T) {
	items := []OverlayItem{
		chainFixture("text_phrase", "s1", "first", 0, 1_000_000, "motion_a"),
		chainFixture("text_phrase", "s1", "at-boundary", 6_000_000, 1_000_000, "motion_b"),
		chainFixture("text_phrase", "s1", "beyond-boundary", 12_000_001, 1_000_000, "motion_c"),
		chainFixture("text_phrase", "s2", "other-scene", 13_000_000, 1_000_000, "motion_d"),
	}
	got := CoalesceChainedOverlays(items, map[string]int{"chain_enabled": 1})
	if len(got) != 3 {
		t.Fatalf("got %d items, want two-segment chain + two standalone items: %+v", len(got), got)
	}
	segments, ok := DecodeChainSegments(got[0].Params)
	if !ok || len(segments) != 2 || segments[1].Text != "at-boundary" {
		t.Fatalf("exact 5000ms empty interval must fuse: segments=%+v ok=%t", segments, ok)
	}
	if got[1].Text != "beyond-boundary" || got[1].SceneID != "s1" || got[2].SceneID != "s2" {
		t.Fatalf("items past boundary or across scenes were merged: %+v", got)
	}
}

func TestCoalesceClampsConfiguredGapToFiveSeconds(t *testing.T) {
	items := []OverlayItem{
		chainFixture("text_phrase", "s1", "first", 0, 1_000_000, "motion_a"),
		chainFixture("text_phrase", "s1", "second", 6_000_001, 1_000_000, "motion_b"),
	}
	got := CoalesceChainedOverlays(items, map[string]int{"chain_gap_ms": 10_000})
	if len(got) != 2 || IsChainItemKind(got[0].Kind) {
		t.Fatalf("configured gap above the 5000ms ceiling must not fuse: %+v", got)
	}
	got = CoalesceChainedOverlays(items, map[string]int{"chain_gap_ms": int(^uint(0) >> 1)})
	if len(got) != 2 {
		t.Fatalf("oversized gap must clamp without overflow, got %+v", got)
	}
}

func TestCoalescePreservesAnimationPolicySelectorsAndOverrides(t *testing.T) {
	first := chainFixture("text_phrase", "s1", "first", 0, 1_000_000, "motion_a")
	first.GroupID, first.SubgroupID = "group-a", "child-a"
	first.MotionParams = map[string]any{"enter_frames": 17, "vendor": "payload-owned"}
	second := chainFixture("text_phrase", "s1", "second", 2_000_000, 1_000_000, "motion_b")
	second.GroupID, second.SubgroupID = "group-a", "child-b"
	second.MotionParams = map[string]any{"exit_frames": 9}
	got := CoalesceChainedOverlays([]OverlayItem{first, second}, map[string]int{"chain_enabled": 1})
	segments, ok := DecodeChainSegments(got[0].Params)
	if len(got) != 1 || !ok || len(segments) != 2 {
		t.Fatalf("expected the two selected animations in one chain: %+v", got)
	}
	if segments[0].GroupID != "group-a" || segments[0].SubgroupID != "child-a" || segments[0].MotionParams["vendor"] != "payload-owned" || segments[0].MotionParams["enter_frames"] != 17 {
		t.Fatalf("first segment lost policy selector/override: %+v", segments[0])
	}
	if segments[1].GroupID != "group-a" || segments[1].SubgroupID != "child-b" || segments[1].MotionParams["exit_frames"] != 9 {
		t.Fatalf("second segment lost policy selector/override: %+v", segments[1])
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

// Fused chains must preserve the run-wide no-repeat invariant verbatim:
// segment motions survive fusion distinct and in order, so the distinctness
// established by EnsureDistinctPhraseMotions before coalescing still holds
// inside every chain after it.
func TestCoalescePreservesDistinctMotionsPerChain(t *testing.T) {
	items := []OverlayItem{
		// Non-short motions on purpose: a short-phrase animation never chains
		// (one animation per clip), the invariant under test is the chain's
		// motion distinctness.
		chainFixture("text_phrase", "s1", "alpha", 100_000, 2_500_000, "phrase_apple_clean_01_blur_soft_reveal"),
		chainFixture("text_phrase", "s1", "beta", 2_700_000, 2_500_000, "phrase_apple_clean_25_opacity_soft_reveal"),
		chainFixture("text_phrase", "s1", "gamma", 5_300_000, 2_500_000, "typewriter_clean"),
	}
	got := CoalesceChainedOverlays(items, map[string]int{"chain_enabled": 1})
	if len(got) != 1 || !IsChainItemKind(got[0].Kind) {
		t.Fatalf("nearby phrases must fuse into one chain: %+v", got)
	}
	segs, ok := got[0].Params["chain_segments"].([]ChainSegment)
	if !ok || len(segs) != 3 {
		t.Fatalf("chain segments = %+v", got[0].Params["chain_segments"])
	}
	seen := map[string]bool{}
	for i, seg := range segs {
		if seg.MotionID != items[i].MotionID {
			t.Errorf("segment %d motion = %q, want %q (fusion must not reselect)", i, seg.MotionID, items[i].MotionID)
		}
		if seen[seg.MotionID] {
			t.Errorf("segment %d repeats motion %q inside one chain", i, seg.MotionID)
		}
		seen[seg.MotionID] = true
	}
}
