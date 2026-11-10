package overlays

import capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

import "testing"

func layoutPhraseItem(id string, startMs, endMs int64, motion string) capmodel.OverlayItem {
	return capmodel.OverlayItem{
		ID: id, SceneID: "s1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
		Text: "A long phrase that stays on screen", MotionID: motion,
		StartMs: startMs, EndMs: endMs,
	}
}

func layoutChainInput() PlanInput {
	return PlanInput{
		PlanID: "layout", VideoID: "layout-video",
		FPSNum: 24, FPSDen: 1,
		AnimationCounts: map[string]int{"chain_gap_ms": 1_000},
	}
}

// Duplicate phrase motions rotate away and a second pass changes nothing.
func TestApplyAnimationConsistencyRotatesDuplicatesIdempotently(t *testing.T) {
	items := []capmodel.OverlayItem{
		layoutPhraseItem("a", 0, 3_000, "text_fade_up"),
		layoutPhraseItem("b", 4_000, 7_000, "text_fade_up"),
	}
	input := PlanInput{
		PlanID: "layout", VideoID: "layout-video",
		PhraseMotions: []string{"text_fade_up", "text_word_rise"},
	}
	ApplyAnimationConsistency(items, input)
	if items[0].MotionID == items[1].MotionID {
		t.Fatalf("duplicate phrase motions survived: %q", items[1].MotionID)
	}
	settled := items[1].MotionID
	ApplyAnimationConsistency(items, input)
	if items[1].MotionID != settled {
		t.Fatalf("second consistency pass changed %q to %q", settled, items[1].MotionID)
	}
}

// A pinned explicit policy choice is registered as used, so the unpinned
// sibling rotates away instead of the pinned item being overwritten.
func TestApplyAnimationConsistencyPinnedKeepsExplicitChoice(t *testing.T) {
	items := []capmodel.OverlayItem{
		layoutPhraseItem("a", 0, 3_000, "text_fade_up"),
		layoutPhraseItem("b", 4_000, 7_000, "text_fade_up"),
	}
	input := PlanInput{
		PlanID: "layout", VideoID: "layout-video",
		PhraseMotions: []string{"text_fade_up", "text_word_rise"},
	}
	ApplyAnimationConsistencyPinned(items, input, map[int]bool{0: true})
	if items[0].MotionID != "text_fade_up" {
		t.Fatalf("pinned item motion = %q, want text_fade_up", items[0].MotionID)
	}
	if items[1].MotionID == "text_fade_up" {
		t.Fatalf("unpinned duplicate kept the pinned motion %q", items[1].MotionID)
	}
}

// The planner finalizer fuses nearby phrases into one chain and lays the
// second segment at the previous segment's end plus the 400 ms handoff.
func TestFinalizePlannerAnimationLayoutFusesNearbyPhrases(t *testing.T) {
	items := []capmodel.OverlayItem{
		layoutPhraseItem("a", 0, 1_000, "text_fade_up"),
		layoutPhraseItem("b", 1_600, 2_600, "text_word_rise"),
	}
	got := FinalizePlannerAnimationLayout(items, layoutChainInput(), 24, 1)
	if len(got) != 1 || !capmodel.IsChainItemKind(got[0].Kind) {
		t.Fatalf("nearby phrases did not fuse: %+v", got)
	}
	segments, ok := capmodel.DecodeChainSegments(got[0].Params)
	if !ok || len(segments) != 2 {
		t.Fatalf("chain segments = %+v (decoded %v)", got[0].Params["chain_segments"], ok)
	}
	if segments[1].StartUS != 1_400_000 {
		t.Fatalf("second segment start = %dus, want 1400000us (1000ms + 400ms handoff)", segments[1].StartUS)
	}
}

// The compile pre-policy stage shapes explicit entrances before chain fusion;
// the segment keeps the shaped value, so the second rank settles one step
// (12 frames at 24 fps) after the first.
func TestPrepareCompiledAnimationLayoutCascadesBeforeFusion(t *testing.T) {
	items := []capmodel.OverlayItem{
		{
			ID: "a", SceneID: "s1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
			Text: "One", MotionID: "text_fade_up", StartMs: 0, EndMs: 3_000,
			MotionParams: map[string]any{"enter_frames": 8},
		},
		{
			ID: "b", SceneID: "s1", Kind: "number", TemplateID: "METRIC_STAT_CARD",
			Text: "42", MotionID: "metric_pop", StartMs: 0, EndMs: 3_000,
			MotionParams: map[string]any{"enter_frames": 8},
		},
	}
	got := PrepareCompiledAnimationLayout(items, layoutChainInput(), 24, 1)
	if got[0].MotionParams["enter_frames"] != 8 {
		t.Fatalf("rank zero entrance = %v, want 8", got[0].MotionParams["enter_frames"])
	}
	if got[1].MotionParams["enter_frames"] != 20 {
		t.Fatalf("rank one entrance = %v, want 20 (8 + 12-frame step)", got[1].MotionParams["enter_frames"])
	}
}

// Phrase entrances retain the one-third share for short windows and cap long
// windows so their text settles within 700ms.
func TestPhraseEntranceFramesCapsLongClipEntrances(t *testing.T) {
	if got := PhraseEntranceFrames(1_200_000, 24, 1); got != 10 {
		t.Fatalf("1.2s at 24fps entrance = %d, want 10", got)
	}
	if got := PhraseEntranceFrames(6_000_000, 24, 1); got != 17 {
		t.Fatalf("6s at 24fps entrance = %d, want 17 (700ms cap)", got)
	}
	if got := PhraseEntranceFrames(0, 24, 1); got != 0 {
		t.Fatalf("empty clip entrance = %d, want 0", got)
	}
	if got := PhraseEntranceFrames(-1, 24, 1); got != 0 {
		t.Fatalf("negative clip entrance = %d, want 0", got)
	}
}

// The selection boundary stays validation-only: chain containers and
// composite parents must not carry a motion of their own.
func TestResolveAnimationSelectionsRejectsContainerMotions(t *testing.T) {
	badChain := []capmodel.OverlayItem{{ID: "c1", Kind: "phrase_chain", MotionID: "text_fade_up"}}
	if err := ResolveAnimationSelections(badChain); err == nil {
		t.Fatal("chain container with MotionID must fail")
	}
	goodChain := []capmodel.OverlayItem{{ID: "c1", Kind: "phrase_chain"}}
	if err := ResolveAnimationSelections(goodChain); err != nil {
		t.Fatalf("chain container without MotionID: %v", err)
	}
	badParent := []capmodel.OverlayItem{{
		ID: "img", Kind: string(capmodel.KindEntityImage), MotionID: "image_fade_reveal",
		ImageLayers: []capmodel.OverlayImageLayer{{ID: "a", MotionID: "image_fade_reveal"}},
	}}
	if err := ResolveAnimationSelections(badParent); err == nil {
		t.Fatal("composite parent with MotionID must fail")
	}
	missingChild := []capmodel.OverlayItem{{
		ID: "img", Kind: string(capmodel.KindEntityImage),
		ImageLayers: []capmodel.OverlayImageLayer{{ID: "a"}},
	}}
	if err := ResolveAnimationSelections(missingChild); err == nil {
		t.Fatal("composite child without MotionID must fail")
	}
}
