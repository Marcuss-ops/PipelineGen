package scriptgeneration

// render_queue_split_test.go pins the per-scene overlay split contract
// (TICKET-OVERLAY-RENDER-PER-SCENE-2026-10-29): plan-absolute entries, N
// batches in plan order, per-item artifact rows, an identical refuse-set at
// plan level, and a fail-closed fallback to the single-timeline submission.
// The gate DEFAULT (OVERLAY_RENDER_PER_SCENE unset → enabled) is the certified
// fast path; only an explicit kill switch restores the single submission.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"go.uber.org/zap"
)

// splitTestPlan builds a two-scene plan whose ordinary items are split across
// scene-01 and scene-02, plus one plan-scoped map (dedicated path) in scene-01.
func splitTestPlan() capoverlay.OverlayPlan {
	plan := manifestTestPlan()
	plan.Items = append(plan.Items, capoverlay.OverlayItem{
		ID: "map-01", SceneID: "scene-01", Kind: string(capoverlay.KindMap), TemplateID: "MAP",
		StartUS: 20_000_000, DurationUS: 3_000_000,
		Map: &capoverlay.MapOverlay{Provider: "local", SourceID: "test-map", SourceLicense: "operator-supplied",
			Center: capoverlay.MapCenter{Latitude: 0, Longitude: 0}, Zoom: 8,
			Width: 1920, Height: 1080, Attribution: "Map data supplied by operator",
			DynamicRendererOnly: true},
	})
	return plan
}

// TestMicroBatchesNeverSplitOverlappingItems pins the micro-batch contract:
// items whose literal windows overlap in one scene compose together, while
// disjoint windows become separate batches in plan order.
func TestMicroBatchesNeverSplitOverlappingItems(t *testing.T) {
	plan := manifestTestPlan()
	plan.Items = []capoverlay.OverlayItem{
		{ID: "a", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "uno due tre", StartUS: 0, DurationUS: 4_000_000},
		{ID: "b", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "quattro cinque sei", StartUS: 3_000_000, DurationUS: 4_000_000},
		{ID: "c", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "sette otto nove", StartUS: 20_000_000, DurationUS: 2_000_000},
	}
	manifest, _ := partitionOverlayRenderWork(plan)
	groups, err := groupManifestCandidatesIntoMicroBatches(plan, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("micro-batches = %d, want 2 (a+b overlap, c disjoint)", len(groups))
	}
	if len(groups[0].candidates) != 2 || groups[0].candidates[0].source.ID != "a" || groups[0].candidates[1].source.ID != "b" {
		t.Fatalf("overlapping a+b must share one micro-batch in plan order")
	}
	if len(groups[1].candidates) != 1 || groups[1].candidates[0].source.ID != "c" {
		t.Fatalf("disjoint c must form its own micro-batch")
	}
}

func TestPlanSpansMultipleScenesOnlyCountsManifestWork(t *testing.T) {
	plan := splitTestPlan()
	if !planSpansMultipleScenes(plan) {
		t.Fatal("two-scene plan must split")
	}
	single := manifestTestPlan()
	single.Items = single.Items[:2] // scene-01 only
	if planSpansMultipleScenes(single) {
		t.Fatal("single-scene manifest plan must not split")
	}
	empty := manifestTestPlan()
	empty.Items = []capoverlay.OverlayItem{{ID: "bg-01", SceneID: "scene-01", Kind: "background", TemplateID: "BACKGROUND", StartUS: 0, DurationUS: 1_000_000}}
	if planSpansMultipleScenes(empty) {
		t.Fatal("background-only plan has no work at all and must never split")
	}
}

func TestGroupManifestCandidatesBySceneKeepsPlanOrder(t *testing.T) {
	plan := splitTestPlan()
	manifest, dedicated := partitionOverlayRenderWork(plan)
	if len(dedicated) != 1 || dedicated[0].source.ID != "map-01" {
		t.Fatalf("map must route to the dedicated path, got manifest=%d dedicated=%d", len(manifest), len(dedicated))
	}
	groups := groupManifestCandidatesByScene(manifest)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].sceneID != "scene-01" || groups[1].sceneID != "scene-02" {
		t.Fatalf("group order must follow first plan index, got %q then %q", groups[0].sceneID, groups[1].sceneID)
	}
	if groups[0].groupFirstIndex != 0 || groups[1].groupFirstIndex != 2 {
		t.Fatalf("group first indices = %d/%d, want 0/2", groups[0].groupFirstIndex, groups[1].groupFirstIndex)
	}
	if len(groups[0].candidates) != 2 {
		t.Fatalf("scene-01 group must hold items 01+02, got %d", len(groups[0].candidates))
	}
	if groups[1].candidates[0].source.ID != "item-03" {
		t.Fatalf("scene-02 group must hold item-03, got %q", groups[1].candidates[0].source.ID)
	}
}

// TestEnqueueChrononPlanSplitFailsClosedWithoutClient pins that the split
// can never invent artifacts without a queue client: with a nil client every
// submission path (per-scene composition or dedicated item) fails closed.
// Cross-scene colliding captions stay impossible to ADMIT: partition routes
// them to the dedicated per-item path before the split's plan-level gate ever
// sees them (pre-existing single-path behaviour, unchanged).
func TestEnqueueChrononPlanSplitFailsClosedWithoutClient(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PER_SCENE", "enabled")
	e := &QueueRenderEnqueuer{}
	plan := splitTestPlan()
	// Distinct content identity: two verbatim-identical phrases collapse to
	/* the same content hash and the manifest refuses them (one image one
	overlay) before any submission — a different, earlier failure layer. */
	plan.Items[1].Text = "corpo narrativo distinto per il secondo contenuto"
	_, err := e.EnqueueChrononPlanSplit(context.Background(), plan)
	if err == nil {
		t.Fatal("a plan submission without a queue client must fail closed, never silently succeed")
	}
}

func TestEnqueueChrononPlanSplitRoutesBackgroundOnlyPlanFailClosed(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PER_SCENE", "enabled")
	e := &QueueRenderEnqueuer{}
	plan := manifestTestPlan()
	plan.Items = []capoverlay.OverlayItem{{ID: "bg-01", SceneID: "scene-01", Kind: "background", TemplateID: "BACKGROUND", StartUS: 0, DurationUS: 1_000_000}}
	_, err := e.EnqueueChrononPlanSplit(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "Background block") {
		t.Fatalf("background-only plan must fail closed naming the SSOT rule, got %v", err)
	}
}

// splitEnqueuerStub implements BOTH capabilities so the runner's capability
// check and fallback path can be pinned without a live queue client.
type splitEnqueuerStub struct {
	splitRef    RenderReference
	splitErr    error
	singleRef   RenderReference
	singleErr   error
	splitCalls  int
	singleCalls int
}

func (e *splitEnqueuerStub) EnqueueChrononPlanSplit(ctx context.Context, plan capoverlay.OverlayPlan) (RenderReference, error) {
	e.splitCalls++
	return e.splitRef, e.splitErr
}

func (e *splitEnqueuerStub) EnqueueChrononPlan(ctx context.Context, plan capoverlay.OverlayPlan) (RenderReference, error) {
	e.singleCalls++
	return e.singleRef, e.singleErr
}

func multiScenePointerFor(t *testing.T) *capoverlay.OverlayPlan {
	t.Helper()
	plan := splitTestPlan()
	return &plan
}

func TestRenderOverlayPlanAtBoundaryUsesSplitWhenEnabled(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PER_SCENE", "enabled")
	stub := &splitEnqueuerStub{splitRef: RenderReference{JobID: "split-job", Status: "completed"}}
	runner := &Runner{overlayRenderEnqueuer: stub, log: zap.NewNop()}
	ref, err := runner.renderOverlayPlanAtBoundary(context.Background(), GenerateRequest{}, multiScenePointerFor(t))
	if err != nil {
		t.Fatalf("split path must satisfy the boundary: %v", err)
	}
	if stub.splitCalls != 1 || stub.singleCalls != 0 {
		t.Fatalf("split=%d single=%d, want split=1 single=0", stub.splitCalls, stub.singleCalls)
	}
	if ref.JobID != "split-job" {
		t.Fatalf("reference = %q, want the split submission", ref.JobID)
	}
}

func TestRenderOverlayPlanAtBoundaryFallsBackOnSplitError(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PER_SCENE", "enabled")
	stub := &splitEnqueuerStub{
		splitErr:  errors.New("per-scene submission refused"),
		singleRef: RenderReference{JobID: "single-job", Status: "completed"},
	}
	runner := &Runner{overlayRenderEnqueuer: stub, log: zap.NewNop()}
	ref, err := runner.renderOverlayPlanAtBoundary(context.Background(), GenerateRequest{}, multiScenePointerFor(t))
	if err != nil {
		t.Fatalf("split error must fall back to the single path, not fail the run: %v", err)
	}
	if stub.singleCalls != 1 {
		t.Fatalf("single path must be resubmitted after the split failed, single=%d", stub.singleCalls)
	}
	if ref.JobID != "single-job" {
		t.Fatalf("reference = %q, want the single-timeline submission", ref.JobID)
	}
}

func TestRenderOverlayPlanAtBoundaryFinalJobNeverSplits(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PER_SCENE", "enabled")
	stub := &splitEnqueuerStub{splitRef: RenderReference{JobID: "split-job", Status: "completed"}}
	runner := &Runner{overlayRenderEnqueuer: stub, log: zap.NewNop()}
	if _, err := runner.renderOverlayPlanAtBoundary(context.Background(), GenerateRequest{FinalJob: true}, multiScenePointerFor(t)); err != nil {
		t.Fatalf("final job must submit through the single path: %v", err)
	}
	if stub.splitCalls != 0 || stub.singleCalls != 1 {
		t.Fatalf("final job split=0 single=1, got split=%d single=%d", stub.splitCalls, stub.singleCalls)
	}
}

func TestRenderOverlayPlanAtBoundaryDefaultsToSplit(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PER_SCENE", "")
	stub := &splitEnqueuerStub{splitRef: RenderReference{JobID: "split-job", Status: "completed"}}
	runner := &Runner{overlayRenderEnqueuer: stub, log: zap.NewNop()}
	if !overlayPerSceneSplitEnabled() {
		t.Fatal("flag unset must read as enabled: the split is the certified fast path")
	}
	ref, err := runner.renderOverlayPlanAtBoundary(context.Background(), GenerateRequest{}, multiScenePointerFor(t))
	if err != nil {
		t.Fatalf("default split must satisfy the boundary: %v", err)
	}
	if stub.splitCalls != 1 || stub.singleCalls != 0 {
		t.Fatalf("default splits: split=%d single=%d, want split=1 single=0", stub.splitCalls, stub.singleCalls)
	}
	if ref.JobID != "split-job" {
		t.Fatalf("reference = %q, want the split submission", ref.JobID)
	}
}

func TestRenderOverlayPlanAtBoundaryExplicitKillSwitchKeepsSinglePath(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PER_SCENE", "disabled")
	stub := &splitEnqueuerStub{splitRef: RenderReference{JobID: "split-job", Status: "completed"}}
	runner := &Runner{overlayRenderEnqueuer: stub, log: zap.NewNop()}
	if overlayPerSceneSplitEnabled() {
		t.Fatal("explicit 'disabled' must read as off")
	}
	if _, err := runner.renderOverlayPlanAtBoundary(context.Background(), GenerateRequest{}, multiScenePointerFor(t)); err != nil {
		t.Fatalf("kill switch must keep today's single-path behaviour: %v", err)
	}
	if stub.splitCalls != 0 || stub.singleCalls != 1 {
		t.Fatalf("kill switch splits nothing: split=%d single=%d", stub.splitCalls, stub.singleCalls)
	}
}

func TestOverlayPerSceneSplitWorkersDefaultMatchesGPULanes(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PER_SCENE_WORKERS", "")
	if got := overlayPerSceneSplitWorkers(); got != 3 {
		t.Fatalf("default workers = %d, want the certified 3 GPU lanes", got)
	}
	t.Setenv("OVERLAY_RENDER_PER_SCENE_WORKERS", "5")
	if got := overlayPerSceneSplitWorkers(); got != 5 {
		t.Fatalf("explicit workers = %d, want 5", got)
	}
}

func TestRunSplitOverlayWorkOverlapsDedicatedAndScenes(t *testing.T) {
	plan := splitTestPlan()
	startedScenes := make(chan struct{})
	startedDedicated := make(chan struct{})
	release := make(chan struct{})
	artifact := &RenderArtifact{ID: "scene-artifact", SHA256: strings.Repeat("a", 64)}
	done := make(chan error, 1)
	go func() {
		ref, err := runSplitOverlayWork(context.Background(), plan,
			func(ctx context.Context) ([]RenderReference, error) {
				close(startedScenes)
				select {
				case <-release:
					return []RenderReference{{JobID: "scene", Status: "completed", Artifact: artifact,
						Items:   []OverlayItemRenderReference{{ItemID: "item-01"}},
						Batches: []OverlayBatchRenderReference{{BatchID: "scene", Artifact: artifact}}}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
			func(ctx context.Context) (*RenderReference, error) {
				close(startedDedicated)
				select {
				case <-release:
					return &RenderReference{Items: []OverlayItemRenderReference{{ItemID: "map-01"}}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
		if err == nil && (len(ref.Items) != 2 || ref.Items[0].ItemID != "item-01" || ref.Items[1].ItemID != "map-01") {
			err = errors.New("references were not merged in plan order")
		}
		done <- err
	}()
	for _, started := range []<-chan struct{}{startedScenes, startedDedicated} {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatal("both render branches must start before either finishes")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunSplitOverlayWorkCancelsOtherBranchOnFailure(t *testing.T) {
	for _, failScene := range []bool{true, false} {
		t.Run(map[bool]string{true: "scene", false: "dedicated"}[failScene], func(t *testing.T) {
			failure := errors.New("render branch failed")
			otherStarted := make(chan struct{})
			otherCancelled := make(chan struct{})
			waitForCancellation := func(ctx context.Context) error {
				close(otherStarted)
				<-ctx.Done()
				close(otherCancelled)
				return ctx.Err()
			}
			fail := func(ctx context.Context) error {
				select {
				case <-otherStarted:
					return failure
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := runSplitOverlayWork(ctx, splitTestPlan(),
				func(ctx context.Context) ([]RenderReference, error) {
					if failScene {
						return nil, fail(ctx)
					}
					return nil, waitForCancellation(ctx)
				},
				func(ctx context.Context) (*RenderReference, error) {
					if failScene {
						return nil, waitForCancellation(ctx)
					}
					return nil, fail(ctx)
				})
			if !errors.Is(err, failure) {
				t.Fatalf("expected originating failure, got %v", err)
			}
			select {
			case <-otherCancelled:
			default:
				t.Fatal("other branch did not observe cancellation")
			}
		})
	}
}

func TestRunSplitOverlayWorkRejectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runSplitOverlayWork(ctx, splitTestPlan(),
		func(context.Context) ([]RenderReference, error) { return nil, nil },
		func(context.Context) (*RenderReference, error) { return nil, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled split must fail, got %v", err)
	}
}

// BenchmarkSplitOverlayOverlap isolates orchestration with simulated service
// delays. It is not a GPU or production-throughput benchmark.
func BenchmarkSplitOverlayOverlap(b *testing.B) {
	plan := splitTestPlan()
	artifact := &RenderArtifact{ID: "fixture"}
	scenes := func(context.Context) ([]RenderReference, error) {
		time.Sleep(10 * time.Millisecond)
		return []RenderReference{{Artifact: artifact, Batches: []OverlayBatchRenderReference{{Artifact: artifact}}}}, nil
	}
	dedicated := func(context.Context) (*RenderReference, error) {
		time.Sleep(10 * time.Millisecond)
		return &RenderReference{}, nil
	}
	for _, mode := range []string{"before_serial", "after_overlap"} {
		b.Run(mode, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if mode == "before_serial" {
					refs, err := scenes(context.Background())
					if err != nil {
						b.Fatal(err)
					}
					ded, err := dedicated(context.Background())
					if err != nil {
						b.Fatal(err)
					}
					if _, err = assembleSplitManifestReference(plan, refs, ded); err != nil {
						b.Fatal(err)
					}
				} else if _, err := runSplitOverlayWork(context.Background(), plan, scenes, dedicated); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestAssembleSplitManifestReference pins the assembled language reference:
// N batches in plan order, per-item rows carrying the artifact of the scene
// that owns their frames, dedicated rows merged in editorial order.
func TestAssembleSplitManifestReference(t *testing.T) {
	plan := splitTestPlan()
	scene01Artifact := &RenderArtifact{ID: "a-scene-01", SHA256: strings.Repeat("a", 64)}
	scene02Artifact := &RenderArtifact{ID: "a-scene-02", SHA256: strings.Repeat("b", 64)}
	sceneManifest := &capoverlay.OverlayManifest{PlanID: "plan-01"}
	scene01 := RenderReference{
		JobID: "j1", Status: "completed", Artifact: scene01Artifact, Manifest: sceneManifest,
		Items: []OverlayItemRenderReference{
			{ItemID: "item-01", JobID: "j1", Status: "completed", BatchID: "d1"},
			{ItemID: "item-02", JobID: "j1", Status: "completed", BatchID: "d1"},
		},
		Batches: []OverlayBatchRenderReference{{BatchID: "d1", SceneID: "scene-01", StartUS: 0, EndUS: 32_000_000, JobID: "j1", Status: "completed", Artifact: scene01Artifact}},
	}
	scene02 := RenderReference{
		JobID: "j2", Status: "completed", Artifact: scene02Artifact,
		Items:   []OverlayItemRenderReference{{ItemID: "item-03", JobID: "j2", Status: "completed", BatchID: "d2"}},
		Batches: []OverlayBatchRenderReference{{BatchID: "d2", SceneID: "scene-02", StartUS: 32_000_000, EndUS: 55_000_000, JobID: "j2", Status: "completed", Artifact: scene02Artifact}},
	}
	dedicated := &RenderReference{Items: []OverlayItemRenderReference{{ItemID: "map-01", JobID: "jm", Status: "completed", Artifact: &RenderArtifact{ID: "a-map"}}}}
	ref, err := assembleSplitManifestReference(plan, []RenderReference{scene01, scene02}, dedicated)
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}
	if len(ref.Batches) != 2 {
		t.Fatalf("batches = %d, want one per scene", len(ref.Batches))
	}
	if ref.Batches[0].SceneID != "scene-01" || ref.Batches[1].SceneID != "scene-02" {
		t.Fatalf("batch order must follow plan order, got %q/%q", ref.Batches[0].SceneID, ref.Batches[1].SceneID)
	}
	if ref.Artifact != scene01Artifact {
		t.Fatal("top-level Artifact must be the first scene's identity (legacy consumers)")
	}
	if ref.Manifest != nil {
		t.Fatal("split reference must not carry a top-level single-timeline manifest")
	}
	if ref.Batches[0].Manifest != sceneManifest {
		t.Fatal("split batch must retain its own certified manifest and packed offsets")
	}
	if len(ref.Items) != 4 {
		t.Fatalf("rows = %d, want 3 manifest rows + 1 dedicated row", len(ref.Items))
	}
	wantOrder := []string{"item-01", "item-02", "item-03", "map-01"}
	for i, want := range wantOrder {
		if ref.Items[i].ItemID != want {
			t.Fatalf("row %d = %q, want %q (editorial plan order)", i, ref.Items[i].ItemID, want)
		}
	}
	if ref.Items[0].Artifact != scene01Artifact {
		t.Fatal("item-01 row must carry its scene-01 composition artifact")
	}
	if ref.Items[2].Artifact != scene02Artifact {
		t.Fatal("item-03 row must carry its scene-02 composition artifact")
	}
	if ref.Items[3].Artifact == nil || ref.Items[3].Artifact.ID != "a-map" {
		t.Fatal("dedicated map row must keep its own artifact")
	}
	if renderedArtifacts(ref) == nil {
		t.Fatal("multi-batch reference must expose its artifacts to the chronon metrics")
	}
}
