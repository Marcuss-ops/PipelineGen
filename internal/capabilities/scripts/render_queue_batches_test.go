package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func manifestTestPlan() capoverlay.OverlayPlan {
	return capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        "test-manifest-timeline",
		VideoID:       "test-manifest-timeline",
		Width:         1920,
		Height:        1080,
		FPSNum:        30,
		FPSDen:        1,
		DurationMS:    100000,
		Items: []capoverlay.OverlayItem{
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
				// Item in scene-02: the manifest timeline is plan-scoped, so
				// cross-scene candidates compose in the same artifact.
				ID: "item-03", SceneID: "scene-02", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
				StartUS: 50_000_000, DurationUS: 2_000_000,
			},
		},
	}
}

// TestFusedOverlayBatchPlanKeepsPlanAbsoluteWindows pins the composition
// contract (OVERLAY-MANIFEST-CONTRACT.md §1/§3): every item keeps its LITERAL
// plan-absolute window [in_us, out_us) — including across scenes — because the
// manifest projects those windows onto the artifact's frames with no origin
// offset.
func TestFusedOverlayBatchPlanKeepsPlanAbsoluteWindows(t *testing.T) {
	plan := manifestTestPlan()
	candidates := overlayRenderableCandidates(plan)
	if len(candidates) != 3 {
		t.Fatalf("candidates len = %d, want 3", len(candidates))
	}

	work := overlayBatchWork{
		sceneID:    "scene-01",
		startUS:    0,
		endUS:      overlayBatchWireEndUS(0, 52_000_000),
		candidates: candidates,
	}
	work.id = overlayBatchIdentity(plan, work)
	fused, err := fusedOverlayBatchPlan(plan, work)
	if err != nil {
		t.Fatalf("fusedOverlayBatchPlan failed: %v", err)
	}
	if len(fused.Items) != 3 {
		t.Fatalf("fused.Items len = %d, want 3", len(fused.Items))
	}
	for i, item := range fused.Items {
		want := candidates[i].source.StartUS
		if item.StartUS != want {
			t.Errorf("fused.Items[%d].StartUS = %d, want %d (plan-absolute)", i, item.StartUS, want)
		}
		if item.DurationUS != candidates[i].source.DurationUS {
			t.Errorf("fused.Items[%d].DurationUS = %d, want %d (literal window)", i, item.DurationUS, candidates[i].source.DurationUS)
		}
	}
	if fused.DurationMS != 52_000 {
		t.Errorf("fused.DurationMS = %d, want 52000", fused.DurationMS)
	}
}

// TestFusedOverlayBatchPlanAcceptsSingleVoice pins that a single voice still
// composes one manifest timeline artifact (the cutover removed the old
// multi-item minimum).
func TestFusedOverlayBatchPlanAcceptsSingleVoice(t *testing.T) {
	plan := manifestTestPlan()
	candidates := overlayRenderableCandidates(plan)[:1]
	start, end := overlayCandidateRenderWindow(plan, candidates[0])
	work := overlayBatchWork{
		sceneID:    "scene-01",
		startUS:    0,
		endUS:      overlayBatchWireEndUS(0, end),
		candidates: candidates,
	}
	work.id = overlayBatchIdentity(plan, work)
	if start != 10_000_000 {
		t.Fatalf("render window start = %d, want 10000000", start)
	}
	fused, err := fusedOverlayBatchPlan(plan, work)
	if err != nil {
		t.Fatalf("fusedOverlayBatchPlan failed: %v", err)
	}
	if len(fused.Items) != 1 {
		t.Fatalf("fused.Items len = %d, want 1", len(fused.Items))
	}
}

// TestPartitionOverlayRenderWork pins the cutover boundary: ordinary items
// compose the manifest timeline; maps and chain items keep their dedicated
// per-item renders.
func TestPartitionOverlayRenderWork(t *testing.T) {
	plan := manifestTestPlan()
	plan.Items = append(plan.Items,
		capoverlay.OverlayItem{
			ID: "map-01", SceneID: "scene-02", Kind: "dynamic_map",
			StartUS: 5_000_000, DurationUS: 4_000_000,
			Map: &capoverlay.MapOverlay{},
		},
		capoverlay.OverlayItem{
			ID: "chain-01", SceneID: "scene-02", Kind: string(capoverlay.KindPhraseChain),
			StartUS: 60_000_000, DurationUS: 4_000_000,
		},
	)
	manifest, dedicated := partitionOverlayRenderWork(plan)
	if len(manifest) != 3 {
		t.Fatalf("manifest candidates = %d, want 3", len(manifest))
	}
	if len(dedicated) != 2 {
		t.Fatalf("dedicated candidates = %d, want 2", len(dedicated))
	}
	for _, candidate := range dedicated {
		if candidate.source.Map == nil && !capoverlay.IsChainItemKind(candidate.source.Kind) {
			t.Errorf("dedicated candidate %q is neither map nor chain", candidate.source.ID)
		}
	}
}

// TestAdmitManifestCaptionWindows pins the fitted-caption admission at plan
// scale: caption-bearing voices must have pairwise disjoint render windows on
// the shared frame timeline; everything else is admitted.
func TestAdmitManifestCaptionWindows(t *testing.T) {
	plan := manifestTestPlan()
	plan.Items[0].EntityCaption = "first caption"
	plan.Items[2].EntityCaption = "far away caption"
	candidates := overlayRenderableCandidates(plan)
	if err := admitManifestCaptionWindows(candidates, plan); err != nil {
		t.Fatalf("disjoint caption windows must pass: %v", err)
	}

	plan.Items[1].EntityCaption = "overlapping caption"
	plan.Items[1].StartUS = 10_500_000
	plan.Items[1].DurationUS = 2_000_000
	candidates = overlayRenderableCandidates(plan)
	err := admitManifestCaptionWindows(candidates, plan)
	if err == nil {
		t.Fatal("overlapping caption windows must be refused")
	}
	if !strings.Contains(err.Error(), "item-02") {
		t.Errorf("refusal must name the colliding item, got: %v", err)
	}
}

// TestEnqueueChrononPlanRejectsBackgroundOnlyPlan pins the single-path rule
// (TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE §9.3.3 punto 2): a plan whose
// renderable-candidate list is empty has no third legacy render route —
// the background must be declared in the plan's Background block, and the
// enqueue fails closed instead of submitting the raw plan to the queue.
func TestEnqueueChrononPlanRejectsBackgroundOnlyPlan(t *testing.T) {
	e := &QueueRenderEnqueuer{}
	plan := manifestTestPlan()
	plan.Items = []capoverlay.OverlayItem{
		{ID: "bg-01", SceneID: "scene-01", Kind: "background", TemplateID: "BACKGROUND",
			StartUS: 0, DurationUS: plan.DurationMS * 1000},
	}
	if got := overlayRenderableCandidates(plan); len(got) != 0 {
		t.Fatalf("background-only plan must have zero renderable candidates, got %d", len(got))
	}
	_, err := e.EnqueueChrononPlan(context.Background(), plan)
	if err == nil {
		t.Fatal("background-only plan must fail closed with no render route")
	}
	if !strings.Contains(err.Error(), "Background block") {
		t.Fatalf("error must name the Background SSOT rule: %v", err)
	}
}

// TestSingleMapPlanUsesDedicatedPreMaterializationAndFoldRoute proves a
// one-item map submission and a multi-item map plan use the same map-specific
// preparation, child identity and fold prerequisites. The distinction is the
// parent plan shape only; map submission never escapes the dedicated path.
func TestSingleMapPlanUsesDedicatedPreMaterializationAndFoldRoute(t *testing.T) {
	one := manifestTestPlan()
	mapOverlay := &capoverlay.MapOverlay{
		Provider: "local", SourceID: "test-map", SourceLicense: "operator-supplied",
		Center: capoverlay.MapCenter{Latitude: 0, Longitude: 0}, Zoom: 8,
		Width: 1920, Height: 1080, Attribution: "Map data supplied by operator",
		DynamicRendererOnly: true,
	}
	one.Items = []capoverlay.OverlayItem{{
		ID: "map-01", SceneID: "scene-01", Kind: string(capoverlay.KindMap), TemplateID: "MAP",
		StartMs: 1_000, EndMs: 3_000, StartUS: 1_000_000, DurationUS: 2_000_000, Map: mapOverlay,
	}}
	manifest, dedicated := partitionOverlayRenderWork(one)
	if len(manifest) != 0 || len(dedicated) != 1 {
		t.Fatalf("single-map plan partition: manifest=%d dedicated=%d, want 0/1", len(manifest), len(dedicated))
	}
	if dedicated[0].index != 0 || dedicated[0].source.ID != "map-01" {
		t.Fatalf("single-map dedicated identity = index %d/item %q, want index 0/map-01", dedicated[0].index, dedicated[0].source.ID)
	}
	oneChild, _, err := separateOverlayItemPlan(one, dedicated[0].source, dedicated[0].index)
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
	many.Items = append(append([]capoverlay.OverlayItem(nil), one.Items...), capoverlay.OverlayItem{
		ID: "phrase-02", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
		StartMs: 5_000, EndMs: 7_000, StartUS: 5_000_000, DurationUS: 2_000_000,
	})
	manyManifest, manyDedicated := partitionOverlayRenderWork(many)
	if len(manyManifest) != 1 || len(manyDedicated) != 1 || manyDedicated[0].source.ID != "map-01" {
		t.Fatalf("mixed map plan partition: manifest=%d dedicated=%d", len(manyManifest), len(manyDedicated))
	}
	manyChild, _, err := separateOverlayItemPlan(many, manyDedicated[0].source, manyDedicated[0].index)
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
