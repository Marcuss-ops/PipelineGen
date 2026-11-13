package scriptgeneration

import (
	"context"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"strings"
	"testing"
)

func manifestTestPlan() capmodel.OverlayPlan {
	return capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan,
		PlanID:        "test-per-item-overlay",
		VideoID:       "test-per-item-overlay",
		Width:         1920,
		Height:        1080,
		FPSNum:        30,
		FPSDen:        1,
		DurationMS:    100000,
		Items: []capmodel.OverlayItem{
			{
				ID: "item-01", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
				StartUS: 10_000_000, DurationUS: 2_000_000,
			},
			{
				// 18-second gap after item-01
				ID: "item-02", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
				StartUS: 30_000_000, DurationUS: 2_000_000,
			},
			{
				// Item in scene-02: every item renders independently, regardless
				// of its scene.
				ID: "item-03", SceneID: "scene-02", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
				StartUS: 50_000_000, DurationUS: 2_000_000,
			},
		},
	}
}

// TestEnqueueChrononPlanItemsRejectsBackgroundOnlyPlan pins the single-path
// rule (TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE §9.3.3 punto 2): a plan
// whose renderable-candidate list is empty has no third legacy render route —
// the background must be declared in the plan's Background block, and the
// per-item enqueue fails closed instead of submitting the raw plan to the queue.
func TestLongPhraseClipKeepsFullSpokenWindow(t *testing.T) {
	plan := manifestTestPlan()
	plan.Items = []capmodel.OverlayItem{{
		ID: "long-phrase", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
		StartUS: 10_000_000, DurationUS: 20_000_000,
	}}
	child, metadata, err := separateOverlayItemPlan(plan, plan.Items[0], 0)
	if err != nil {
		t.Fatalf("long phrase child plan: %v", err)
	}
	// Spoken 20s + the one-second settled tail; the old 8s clip cap cut it short.
	if child.DurationMS != 21_000 || child.Items[0].DurationUS != 21_000_000 {
		t.Fatalf("long phrase clip = %dms/%dus, want 21000ms/21000000us", child.DurationMS, child.Items[0].DurationUS)
	}
	if metadata.TargetDurationUS != 21_000_000 {
		t.Fatalf("published target duration = %dus, want 21000000us", metadata.TargetDurationUS)
	}
}

func TestEnqueueChrononPlanItemsRejectsBackgroundOnlyPlan(t *testing.T) {
	e := &QueueRenderEnqueuer{}
	plan := manifestTestPlan()
	plan.Items = []capmodel.OverlayItem{
		{ID: "bg-01", SceneID: "scene-01", Kind: "background", TemplateID: "BACKGROUND",
			StartUS: 0, DurationUS: plan.DurationMS * 1000},
	}
	if got := overlayRenderableCandidates(plan); len(got) != 0 {
		t.Fatalf("background-only plan must have zero renderable candidates, got %d", len(got))
	}
	_, err := e.EnqueueChrononPlanItems(context.Background(), plan)
	if err == nil {
		t.Fatal("background-only plan must fail closed with no render route")
	}
	if !strings.Contains(err.Error(), "no renderable items") {
		t.Fatalf("error must state that the plan has no renderable items: %v", err)
	}
}

// TestSingleMapPlanUsesDedicatedPreMaterializationAndFoldRoute proves a
// one-item map submission and a multi-item map plan use the same map-specific
// preparation, child identity and fold prerequisites. The distinction is the
// parent plan shape only; map submission never escapes the dedicated path.
func TestSingleMapPlanUsesDedicatedPreMaterializationAndFoldRoute(t *testing.T) {
	one := manifestTestPlan()
	mapOverlay := &capmodel.MapOverlay{
		Provider: "local", SourceID: "test-map", SourceLicense: "operator-supplied",
		Center: capmodel.MapCenter{Latitude: 0, Longitude: 0}, Zoom: 8,
		Width: 1920, Height: 1080, Attribution: "Map data supplied by operator",
		DynamicRendererOnly: true,
	}
	one.Items = []capmodel.OverlayItem{{
		ID: "map-01", SceneID: "scene-01", Kind: string(capmodel.KindMap), TemplateID: "MAP",
		StartMs: 1_000, EndMs: 3_000, StartUS: 1_000_000, DurationUS: 2_000_000, Map: mapOverlay,
	}}
	oneCandidates := overlayRenderableCandidates(one)
	if len(oneCandidates) != 1 || oneCandidates[0].index != 0 || oneCandidates[0].source.ID != "map-01" {
		t.Fatalf("single-map candidates = %d, want one map-01 at index 0", len(oneCandidates))
	}
	oneChild, _, err := separateOverlayItemPlan(one, oneCandidates[0].source, oneCandidates[0].index)
	if err != nil {
		t.Fatalf("single-map child plan: %v", err)
	}
	if oneChild.PlanID != overlayItemChildPlanID(one.PlanID, 0, "map-01") || oneChild.VideoID != oneChild.PlanID || len(oneChild.Items) != 1 {
		t.Fatalf("single-map child identity/shape = plan=%q video=%q items=%d", oneChild.PlanID, oneChild.VideoID, len(oneChild.Items))
	}
	oneNormalized, oneDecisions, err := normalizeDynamicMapFoldPlan(oneChild)
	if err != nil {
		t.Fatalf("normalize single-map child plan: %v", err)
	}
	if len(oneDecisions) != 1 || oneDecisions[0].itemID != "map-01" {
		t.Fatalf("single-map fold decisions = %+v, want exactly map-01", oneDecisions)
	}

	many := one
	many.Items = append(append([]capmodel.OverlayItem(nil), one.Items...), capmodel.OverlayItem{
		ID: "phrase-02", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
		StartMs: 5_000, EndMs: 7_000, StartUS: 5_000_000, DurationUS: 2_000_000,
	})
	var manyMap overlayItemCandidate
	for _, candidate := range overlayRenderableCandidates(many) {
		if candidate.source.ID == "map-01" {
			manyMap = candidate
			break
		}
	}
	if manyMap.source.ID != "map-01" {
		t.Fatal("mixed map plan must still contain map-01")
	}
	manyChild, _, err := separateOverlayItemPlan(many, manyMap.source, manyMap.index)
	if err != nil {
		t.Fatalf("multi-item map child plan: %v", err)
	}
	manyNormalized, manyDecisions, err := normalizeDynamicMapFoldPlan(manyChild)
	if err != nil {
		t.Fatalf("normalize multi-item map child plan: %v", err)
	}
	if len(manyDecisions) != len(oneDecisions) || manyDecisions[0] != oneDecisions[0] {
		t.Fatalf("single/multi map decisions diverged: single=%+v multi=%+v", oneDecisions, manyDecisions)
	}
	if oneNormalized.PlanID != manyNormalized.PlanID || oneNormalized.VideoID != manyNormalized.VideoID {
		t.Fatalf("single/multi one-map normalization differs: single=%q/%q multi=%q/%q",
			oneNormalized.PlanID, oneNormalized.VideoID, manyNormalized.PlanID, manyNormalized.VideoID)
	}
	if oneChild.Items[0].StartUS != manyChild.Items[0].StartUS || oneChild.Items[0].DurationUS != manyChild.Items[0].DurationUS {
		t.Fatalf("single/multi map render windows differ: single=%d/%d multi=%d/%d",
			oneChild.Items[0].StartUS, oneChild.Items[0].DurationUS, manyChild.Items[0].StartUS, manyChild.Items[0].DurationUS)
	}
	if keyOne, keyMany := dynamicMapVideoFlightKey(oneNormalized), dynamicMapVideoFlightKey(manyNormalized); keyOne != keyMany {
		t.Fatalf("single/multi normalized map cache-flight keys differ: %q != %q", keyOne, keyMany)
	}
}

// TestStampOutputProfileIsTheSingleOutputIdentity pins the final-job half of
// the contract rule: the packed timeline is composited onto the source video
// and rendered as opaque H.264, so its plan must not keep the alpha-bearing
// overlay-v1 the overlay planner inherited. Both halves of the identity — the
// certification profile AND the media contract derived from it — come from one
// call, and a plan whose stored contract disagrees with its profile must fail
// validation instead of shipping a false statement.
func TestStampOutputProfileIsTheSingleOutputIdentity(t *testing.T) {
	plan := &capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan,
		PlanID:        "plan-opaque-contract",
		VideoID:       "video-opaque-contract",
		Width:         1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		// The planner's alpha-bearing default, as sealed before this stamp.
		MediaContract: capmodel.DefaultOverlayContractV1.ID,
	}
	plan.StampOutputProfile(kernelmedia.OverlayVideoOnlyProfileID)
	if got := plan.OutputProfileID; got != kernelmedia.OverlayVideoOnlyProfileID {
		t.Fatalf("output_profile_id = %q, want %q", got, kernelmedia.OverlayVideoOnlyProfileID)
	}
	if want := capmodel.DefaultOverlayContractNoAlpha.ID; plan.MediaContract != want {
		t.Fatalf("final-job plan contract = %q, want %q", plan.MediaContract, want)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("stamped plan must still validate: %v", err)
	}

	// The alpha default is the other half of the same derivation: an empty
	// profile keeps overlay-v1, so the non-final-job path is unchanged.
	alpha := &capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan,
		PlanID:        "plan-alpha-contract",
		VideoID:       "video-alpha-contract",
		Width:         1920, Height: 1080, FPSNum: 24, FPSDen: 1,
	}
	alpha.StampOutputProfile("")
	if want := capmodel.DefaultOverlayContractV1.ID; alpha.MediaContract != want {
		t.Fatalf("unprofiled plan contract = %q, want %q", alpha.MediaContract, want)
	}
	if err := alpha.Validate(); err != nil {
		t.Fatalf("unprofiled plan must still validate: %v", err)
	}

	// A hand-set contradiction (profile says video-only, contract says ProRes
	// with alpha) is the exact false certificate this rule exists to reject.
	contradicting := *plan
	contradicting.MediaContract = capmodel.DefaultOverlayContractV1.ID
	contradicting.Fingerprint = ""
	if err := contradicting.Validate(); err == nil {
		t.Fatalf("a contract contradicting output_profile_id %q must not validate", contradicting.OutputProfileID)
	}

	var nilPlan *capmodel.OverlayPlan
	nilPlan.StampOutputProfile(kernelmedia.OverlayVideoOnlyProfileID) // a nil plan is not a crash
}
