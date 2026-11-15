package overlays

import capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

import (
	"fmt"
	"testing"
)

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

// The planner stage must leave candidates separate so budgets and collision
// checks can inspect them. The compiled finalizer owns the only chain pass.
func TestFinalizePlannerAnimationLayoutDefersNearbyPhraseFusion(t *testing.T) {
	items := []capmodel.OverlayItem{
		layoutPhraseItem("a", 0, 1_000, "text_fade_up"),
		layoutPhraseItem("b", 1_600, 2_600, "text_word_rise"),
	}
	got := FinalizePlannerAnimationLayout(items, layoutChainInput(), 24, 1)
	if len(got) != 2 || capmodel.IsChainItemKind(got[0].Kind) || capmodel.IsChainItemKind(got[1].Kind) {
		t.Fatalf("planner fused candidates before final admission: %+v", got)
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
	goodChain := capmodel.CoalesceChainedOverlays([]capmodel.OverlayItem{
		{ID: "a", SceneID: "s1", Kind: "image", TemplateID: "IMAGE_OVERLAY", MotionID: "image_a", StartMs: 0, EndMs: 1_000, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "asset-a"}}},
		{ID: "b", SceneID: "s1", Kind: "image", TemplateID: "IMAGE_OVERLAY", MotionID: "image_b", StartMs: 1_000, EndMs: 2_000, AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "asset-b"}}},
	}, map[string]int{"chain_enabled": 1})
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

func TestResolveAnimationSelectionsAcceptsGroupOwnedComposite(t *testing.T) {
	good := []capmodel.OverlayItem{{
		ID: "img", Kind: string(capmodel.KindEntityImage), GroupMotionID: "duo_split_reveal",
		ImageLayers: []capmodel.OverlayImageLayer{{ID: "a"}, {ID: "b"}},
	}}
	if err := ResolveAnimationSelections(good); err != nil {
		t.Fatalf("group-owned composite must keep its children motion-free: %v", err)
	}
	bad := []capmodel.OverlayItem{{
		ID: "img", Kind: string(capmodel.KindEntityImage), GroupMotionID: "duo_split_reveal",
		ImageLayers: []capmodel.OverlayImageLayer{{ID: "a", MotionID: "image_fade_reveal"}, {ID: "b"}},
	}}
	if err := ResolveAnimationSelections(bad); err == nil {
		t.Fatal("group-owned composite with a competing child motion must fail")
	}
}

func TestFinalizeCompiledAnimationLayoutKeepsOverflowingImageRunStandalone(t *testing.T) {
	items := make([]capmodel.OverlayItem, 0, 4)
	for i := 0; i < 4; i++ {
		start := int64(i) * 5_000_000
		items = append(items, capmodel.OverlayItem{
			ID: fmt.Sprintf("image-%d", i), SceneID: "s1", Kind: "image", TemplateID: "IMAGE_OVERLAY",
			MotionID: fmt.Sprintf("image_motion_%d", i),
			StartUS:  start, DurationUS: 5_000_000,
			StartMs: start / 1000, EndMs: (start + 5_000_000) / 1000,
			AssetRefs: []capmodel.OverlayAssetRef{{AssetID: fmt.Sprintf("asset-%d", i), SHA256: "hash"}},
		})
	}
	input := layoutChainInput()
	input.Scenes = []SceneInput{{ID: "s1", EndUS: 20_000_000}}
	got := FinalizeCompiledAnimationLayout(items, input)
	if len(got) != len(items) {
		t.Fatalf("overflowing run = %+v, want %d standalone images", got, len(items))
	}
	for i, item := range got {
		if capmodel.IsChainItemKind(item.Kind) {
			t.Fatalf("item %d = %q, want standalone image", i, item.Kind)
		}
	}
}
