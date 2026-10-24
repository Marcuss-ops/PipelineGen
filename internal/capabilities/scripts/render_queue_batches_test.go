package scriptgeneration

import (
	"strings"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	caprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/render"
)

func batchTestPlan() capoverlay.OverlayPlan {
	return capoverlay.OverlayPlan{PlanID: "batch-plan", FPSNum: 30, FPSDen: 1, Source: &capoverlay.OverlaySource{AssetID: "source", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
}

func batchTestCandidate(id, scene, kind string, startUS, durationUS int64) overlayItemCandidate {
	return overlayItemCandidate{index: int(startUS / 1_000_000), source: capoverlay.OverlayItem{
		ID: id, SceneID: scene, Kind: kind, StartUS: startUS, DurationUS: durationUS,
		StartMs: startUS / 1000, EndMs: (startUS + durationUS) / 1000,
	}}
}

func TestResolveOverlayRenderModeSelectsSceneSingleOrFused(t *testing.T) {
	plan := caprender.GoldenOverlayPlanV1()
	plan.Items = []capoverlay.OverlayItem{
		{ID: "a", SceneID: "scene", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartMs: 0, EndMs: 3000, Text: "FIRST"},
		{ID: "b", SceneID: "scene", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartMs: 2000, EndMs: 5000, Text: "SECOND"},
	}
	enqueuer := &QueueRenderEnqueuer{}
	if got := resolveOverlayRenderMode(enqueuer, plan); got != overlayRenderModeScene {
		t.Fatalf("default route = %q, want scene", got)
	}
	enqueuer.separateItemRenders = true
	if got := resolveOverlayRenderMode(enqueuer, plan); got != overlayRenderModeSingle {
		t.Fatalf("separate route = %q, want single", got)
	}
	enqueuer.overlayWindowBatching = true
	if got := resolveOverlayRenderMode(enqueuer, plan); got != overlayRenderModeFused {
		t.Fatalf("batch route = %q, want fused", got)
	}
}

func TestPlanOverlayBatchWorkFusesCompatibleWindowsDeterministically(t *testing.T) {
	plan := batchTestPlan()
	plan.Source = nil
	candidates := []overlayItemCandidate{
		{index: 0, source: batchTestCandidate("b", "scene-1", "entity_card", 2_000_000, 3_000_000).source},
		{index: 1, source: batchTestCandidate("a", "scene-1", "text_phrase", 0, 3_000_000).source},
	}
	works := planOverlayBatchWork(plan, candidates)
	if len(works) != 1 || len(works[0].candidates) != 2 {
		t.Fatalf("expected one two-item batch, got %+v", works)
	}
	if works[0].candidates[0].source.ID != "b" || works[0].candidates[1].source.ID != "a" {
		t.Fatalf("batch did not preserve the parent's item order: %+v", works[0].candidates)
	}
	again := planOverlayBatchWork(plan, candidates)
	if len(again) != 1 || again[0].id != works[0].id {
		t.Fatalf("batch identity changed: %q vs %+v", works[0].id, again)
	}
	changed := append([]overlayItemCandidate(nil), candidates...)
	changed[0].source.Text = "edited content with same item and plan IDs"
	changedWork := planOverlayBatchWork(plan, changed)
	if len(changedWork) != 1 || changedWork[0].id == works[0].id {
		t.Fatalf("batch identity aliased edited render content: old=%q new=%+v", works[0].id, changedWork)
	}
}

func TestPlanOverlayBatchWorkPreservesSceneAndMapBoundaries(t *testing.T) {
	plan := batchTestPlan()
	plan.Source = nil
	candidates := []overlayItemCandidate{
		batchTestCandidate("a", "scene-1", "text_phrase", 0, 3_000_000),
		batchTestCandidate("b", "scene-2", "entity_card", 1_000_000, 3_000_000),
		batchTestCandidate("c", "scene-2", "map", 1_000_000, 3_000_000),
		batchTestCandidate("d", "scene-2", "text_phrase", 2_000_000, 3_000_000),
	}
	candidates[2].source.Map = &capoverlay.MapOverlay{}
	works := planOverlayBatchWork(plan, candidates)
	for _, work := range works {
		if len(work.candidates) > 1 {
			for _, candidate := range work.candidates {
				if candidate.source.SceneID != work.sceneID || candidate.source.Map != nil {
					t.Fatalf("invalid cross-boundary batch: %+v", work)
				}
			}
		}
	}
	for _, work := range works {
		for _, candidate := range work.candidates {
			if candidate.source.Map != nil && len(work.candidates) != 1 {
				t.Fatalf("map must remain singleton: %+v", work)
			}
		}
	}
}

func TestPlanOverlayBatchWorkDoesNotRestartUnwindowedSource(t *testing.T) {
	plan := batchTestPlan()
	candidates := []overlayItemCandidate{
		batchTestCandidate("a", "scene-1", "text_phrase", 0, 3_000_000),
		batchTestCandidate("b", "scene-1", "entity_card", 2_000_000, 3_000_000),
	}
	if works := planOverlayBatchWork(plan, candidates); len(works) != 2 {
		t.Fatalf("unwindowed source video must not be restarted in a fused window, got %+v", works)
	}
}

func TestPlanOverlayBatchWorkFillsSparseGapOnlyFromLoopingVideoBackground(t *testing.T) {
	plan := batchTestPlan()
	plan.Source = nil
	plan.Background = &capoverlay.OverlayBackground{Kind: "video", Loop: true}
	candidates := []overlayItemCandidate{
		batchTestCandidate("a", "scene-1", "text_phrase", 0, 1_000_000),
		batchTestCandidate("b", "scene-1", "entity_card", 2_000_000, 1_000_000),
	}
	works := planOverlayBatchWork(plan, candidates)
	if len(works) != 1 || len(works[0].candidates) != 2 || works[0].endUS <= 2_000_000 {
		t.Fatalf("looping video background should safely cover the gap: %+v", works)
	}
}

func TestPlanOverlayBatchWorkDoesNotFuseSparseWindowsWithoutVideoBase(t *testing.T) {
	plan := batchTestPlan()
	plan.Source = nil
	plan.Background = &capoverlay.OverlayBackground{Kind: "color"}
	candidates := []overlayItemCandidate{
		batchTestCandidate("a", "scene-1", "text_phrase", 0, 1_000_000),
		batchTestCandidate("b", "scene-1", "entity_card", 20_000_000, 1_000_000),
	}
	works := planOverlayBatchWork(plan, candidates)
	if len(works) != 2 {
		t.Fatalf("sparse windows over a color background must stay separate, got %d works", len(works))
	}
	plan.RequireDriveBeforeReturn = true
	candidates[1].source.StartUS = 500_000
	candidates[1].source.StartMs = 500
	candidates[1].source.EndMs = 1500
	works = planOverlayBatchWork(plan, candidates)
	if len(works) != 2 {
		t.Fatalf("final-job windows without a canonical video binding must stay separate, got %d works", len(works))
	}
	plan.Background = &capoverlay.OverlayBackground{Kind: "video", Loop: true}
	works = planOverlayBatchWork(plan, candidates)
	if len(works) != 2 {
		t.Fatalf("a generic looping background must not authorize final-job fusion, got %d", len(works))
	}
}

func TestFinalJobRejectsBatchReplaceWithoutCanonicalBase(t *testing.T) {
	artifact := &RenderArtifact{ID: "artifact", DriveFileID: "drive", SHA256: strings.Repeat("a", 64), SizeBytes: 100, FrameCount: 150}
	plan := &capoverlay.OverlayPlan{PlanID: "plan-batch", FPSNum: 30, FPSDen: 1, Items: []capoverlay.OverlayItem{
		{ID: "phrase", SceneID: "scene", Kind: "text_phrase", StartUS: 0, DurationUS: 3_000_000},
		{ID: "entity", SceneID: "scene", Kind: "entity_card", StartUS: 2_000_000, DurationUS: 3_000_000},
	}}
	candidates := []overlayItemCandidate{{index: 0, source: plan.Items[0]}, {index: 1, source: plan.Items[1]}}
	batchID := overlayBatchIdentity(*plan, overlayBatchWork{
		sceneID: "scene", startUS: 0, endUS: 5_000_000, candidates: candidates,
	})
	result := &GenerateResult{
		OverlayPlan: plan,
		OverlayRender: &RenderReference{
			Items: []OverlayItemRenderReference{
				{ItemID: "phrase", JobID: "job-batch", Status: RenderQueueStateCompleted, BatchID: batchID},
				{ItemID: "entity", JobID: "job-batch", Status: RenderQueueStateCompleted, BatchID: batchID},
			},
			Batches: []OverlayBatchRenderReference{{BatchID: batchID, SceneID: "scene", ItemIDs: []string{"phrase", "entity"}, StartUS: 0, EndUS: 5_000_000, JobID: "job-batch", Status: RenderQueueStateCompleted, Artifact: artifact}},
		},
	}
	entries, artifacts, err := finalJobOverlayEntries(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].renderID != batchID || entries[0].item.Kind != "composite_batch" {
		t.Fatalf("batch was not collapsed to one final replacement: %+v", entries)
	}
	if artifacts[entries[0].renderID].SHA256 != artifact.SHA256 {
		t.Fatal("batch lost the certified artifact")
	}
	result.FinalAudio = certifiedFinalAudio(10_000)
	if _, err := finalJobOverlayAssetsExcluding(result, nil); err == nil || !strings.Contains(err.Error(), "canonical per-window base-video binding") {
		t.Fatalf("mode=replace must reject a fused batch without a bound source clip, got %v", err)
	}
	phraseClipEntries, _, err := finalJobOverlayEntries(result, map[string]struct{}{"phrase": {}})
	if err != nil || len(phraseClipEntries) != 0 {
		t.Fatalf("fully consumed phrase-only batch should not be emitted again as mode=replace: entries=%+v err=%v", phraseClipEntries, err)
	}
	phraseClipAssets, err := finalJobOverlayAssetsExcluding(result, map[string]struct{}{"phrase": {}})
	if err != nil || len(phraseClipAssets) != 0 {
		t.Fatalf("fully consumed phrase-only batch should produce no second replacement: assets=%+v err=%v", phraseClipAssets, err)
	}
	if _, _, err := finalJobOverlayEntries(result, map[string]struct{}{"entity": {}}); err == nil {
		t.Fatal("partial batch consumption must fail closed")
	}
	result.OverlayRender.Batches[0].Status = RenderQueueStateFailed
	if _, _, err := finalJobOverlayEntries(result, nil); err == nil || !strings.Contains(err.Error(), "incomplete fused overlay batch") {
		t.Fatalf("non-completed batch must fail closed, got %v", err)
	}
	result.OverlayRender.Batches[0].Status = RenderQueueStateCompleted
	brokenRefs := *result.OverlayRender
	brokenRefs.Items = append([]OverlayItemRenderReference(nil), result.OverlayRender.Items[:1]...)
	result.OverlayRender = &brokenRefs
	if _, _, err := finalJobOverlayEntries(result, nil); err == nil || !strings.Contains(err.Error(), "no semantic render reference") {
		t.Fatalf("batch with missing semantic member reference must fail closed, got %v", err)
	}
	brokenRefs = *result.OverlayRender
	brokenRefs.Items = append([]OverlayItemRenderReference(nil), brokenRefs.Items...)
	brokenRefs.Items[0].Artifact = artifact
	entityRef := OverlayItemRenderReference{
		ItemID: "entity", JobID: "job-batch", Status: RenderQueueStateCompleted,
		BatchID: batchID,
	}
	brokenRefs.Items = append(brokenRefs.Items, entityRef, entityRef)
	result.OverlayRender = &brokenRefs
	if _, _, err := finalJobOverlayEntries(result, nil); err == nil || !strings.Contains(err.Error(), "duplicate overlay render reference") {
		t.Fatalf("duplicate semantic member reference must fail closed, got %v", err)
	}
	result.OverlayRender = &RenderReference{
		Items: []OverlayItemRenderReference{
			{ItemID: "phrase", JobID: "job-batch", Status: RenderQueueStateCompleted, BatchID: batchID},
			{ItemID: "entity", JobID: "job-batch", Status: RenderQueueStateCompleted, BatchID: batchID},
		},
		Batches: []OverlayBatchRenderReference{{BatchID: batchID, SceneID: "scene", ItemIDs: []string{"phrase", "entity"}, StartUS: 0, EndUS: 5_000_000, JobID: "job-batch", Status: RenderQueueStateCompleted, Artifact: artifact}},
	}
	result.OverlayRender.Items[1].Artifact = artifact
	if _, _, err := finalJobOverlayEntries(result, nil); err == nil || !strings.Contains(err.Error(), "also has an individual artifact") {
		t.Fatalf("batch member with a competing individual artifact must fail closed, got %v", err)
	}
	result.OverlayRender.Items[1].Artifact = nil

	shortWindow := *result.OverlayRender
	shortBatch := shortWindow.Batches[0]
	shortBatch.EndUS = 4_000_000
	shortWindow.Batches = []OverlayBatchRenderReference{shortBatch}
	result.OverlayRender = &shortWindow
	if _, _, err := finalJobOverlayEntries(result, nil); err == nil || !strings.Contains(err.Error(), "does not cover member") {
		t.Fatalf("batch with an uncovered member window must fail closed, got %v", err)
	}

	result.OverlayRender.Batches[0] = OverlayBatchRenderReference{
		BatchID: batchID, SceneID: "scene", ItemIDs: []string{"phrase", "entity"},
		StartUS: 0, EndUS: 5_000_000, JobID: "job-batch", Status: RenderQueueStateCompleted,
		Artifact: &RenderArtifact{ID: "artifact", DriveFileID: "drive", SHA256: strings.Repeat("a", 64), SizeBytes: 100, FrameCount: 149},
	}
	if _, err := finalJobOverlayAssetsExcluding(result, nil); err == nil || !strings.Contains(err.Error(), "certified frames") {
		t.Fatalf("short batch artifact must not be silently trimmed, got %v", err)
	}
}
