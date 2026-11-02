package scriptgeneration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.uber.org/zap"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// The per-scene split produces one composition per scene window, so the
// final-job handoff receives N artifacts instead of one. r63 failed the whole
// run on exactly that shape: the gate demanded `frame_count > 0`, while a
// packed scene composite certifies `frame_count: -1` (the frames are only
// addressable through the manifest windows, and the assembler never reads a
// frame count). These tests pin the certifiable shape, the fail-closed
// variants, and the bounded fallback that keeps one uncertifiable batch from
// costing a paid run.

// packedBatchArtifact mirrors the certified per-scene artifact the worker
// returned at r64: real bytes, real duration, unknown frame count.
func packedBatchArtifact(t *testing.T, durationUS int64) *RenderArtifact {
	t.Helper()
	return &RenderArtifact{
		ID: "scene-batch", DriveFileID: "scene-batch-drive",
		SHA256: strings.Repeat("b", 64), SizeBytes: 165288,
		FrameCount: -1, DurationUS: durationUS,
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Container: "mp4", PixelFormat: "yuv420p",
	}
}

func packedBatchReference(t *testing.T, items []capoverlay.OverlayItem, artifact *RenderArtifact) RenderReference {
	t.Helper()
	ref := manifestOverlayRender(items, artifact)
	batch := OverlayBatchRenderReference{
		BatchID: "batch-scene-01", SceneID: "scene-1", JobID: "scene-batch-job", Status: "COMPLETED",
		Artifact: artifact, Manifest: ref.Manifest,
		ItemIDs: make([]string, 0, len(items)),
	}
	for _, item := range items {
		batch.ItemIDs = append(batch.ItemIDs, item.ID)
		batch.StartUS, batch.EndUS = 0, artifact.DurationUS
	}
	// The split assembles exactly this shape: one batch per scene, the first
	// scene's identity at the top level, and NO single-timeline manifest — the
	// handoff must therefore read the batch set, not a top-level manifest.
	return RenderReference{
		JobID: "scene-batch-job", Status: "COMPLETED",
		Artifact: artifact, Items: ref.Items,
		Batches: []OverlayBatchRenderReference{batch},
	}
}

// garlascoPackedBatchReference builds the exact batch r69's split produced and
// the certificate rejected: a 3 s packed scene canvas holding an overlay whose
// PLAN receipt is 26,283,000us..28,020,000us while its frames live at
// 1,263,000us..3,000,000us on that canvas.
func garlascoPackedBatchReference(t *testing.T, item capoverlay.OverlayItem, canvasUS int64) RenderReference {
	t.Helper()
	artifact := packedBatchArtifact(t, canvasUS)
	ref := manifestOverlayRender([]capoverlay.OverlayItem{item}, artifact)
	entry := &ref.Manifest.Entries[0]
	artifactIn, artifactOut := int64(1_263_000), int64(3_000_000)
	entry.ArtifactInUS, entry.ArtifactOutUS = &artifactIn, &artifactOut
	batch := OverlayBatchRenderReference{
		BatchID: "d38e04e8", SceneID: item.SceneID, JobID: "scene-batch-job", Status: "COMPLETED",
		Artifact: artifact, Manifest: ref.Manifest, ItemIDs: []string{item.ID},
		StartUS: 0, EndUS: canvasUS,
	}
	return RenderReference{
		JobID: "scene-batch-job", Status: "COMPLETED", Artifact: artifact, Items: ref.Items,
		Batches: []OverlayBatchRenderReference{batch},
	}
}

// TestCertifyFinalJobOverlayBatchAcceptsTheR69PackedSceneBatch is the regression
// pin for the batch the old certificate rejected: the plan receipt sits at
// 28,020,500us on a 3,000,000us canvas, which is only an error if the plan time
// is mistaken for an artifact position.
func TestCertifyFinalJobOverlayBatchAcceptsTheR69PackedSceneBatch(t *testing.T) {
	item := capoverlay.OverlayItem{
		ID:      "phrase-apple-clean-25-opacity-soft-reveal__pavia-identifica-la-provincia__phrase__01",
		SceneID: "scene-01", Kind: "text_phrase", StartUS: 26_283_000, DurationUS: 1_737_000,
	}
	ref := garlascoPackedBatchReference(t, item, 3_000_000)
	if err := certifiableFinalJobOverlayBatches(true, ref, 24, 1); err != nil {
		t.Fatalf("the r69 packed scene batch must certify at its ARTIFACT window: %v", err)
	}
	// The certificate still fails closed when the artifact window itself runs
	// past the canvas: the relaxation is about which window is measured, not
	// about trusting the manifest. The plan receipt is widened first so the
	// manifest's own §3 span rule cannot be the thing that rejects this case.
	longer := capoverlay.OverlayItem{
		ID:      "phrase-apple-clean-25-opacity-soft-reveal__pavia-identifica-la-provincia__phrase__01",
		SceneID: "scene-01", Kind: "text_phrase", StartUS: 26_000_000, DurationUS: 3_000_000,
	}
	overrun := garlascoPackedBatchReference(t, longer, 4_000_000)
	artifactIn, artifactOut := int64(1_000_000), int64(3_500_000)
	overrun.Batches[0].Manifest.Entries[0].ArtifactInUS = &artifactIn
	overrun.Batches[0].Manifest.Entries[0].ArtifactOutUS = &artifactOut
	overrun.Batches[0].Artifact.DurationUS = 3_000_000
	err := certifiableFinalJobOverlayBatches(true, overrun, 24, 1)
	if err == nil || !strings.Contains(err.Error(), "on the artifact") {
		t.Fatalf("an artifact window past the canvas must fail closed, got %v", err)
	}
}

// TestFinalJobOverlayHandoffMakesThePackedSceneBatchConsumable pins the wire the
// worker validates: the packed batch reaches the set document with its artifact
// positions, and the asset facts carry a POSITIVE frame count even though the
// render receipt reported -1 (a multi-artifact manifest is refused otherwise).
func TestFinalJobOverlayHandoffMakesThePackedSceneBatchConsumable(t *testing.T) {
	item := capoverlay.OverlayItem{
		ID:      "phrase-apple-clean-25-opacity-soft-reveal__pavia-identifica-la-provincia__phrase__01",
		SceneID: "scene-1", Kind: "text_phrase", TemplateID: "phrase", StartMs: 26_283, EndMs: 28_020, Text: "pavia",
	}
	result := ssotGateResult([]capoverlay.OverlayItem{item}, packedBatchArtifact(t, 3_000_000))
	// The certified base is the final audio: r69's last overlay sits at 28 s of
	// a 272 s film, so the fixture needs a base long enough to admit it.
	result.FinalAudio.DurationMS = 30_000
	ref := garlascoPackedBatchReference(t, item, 3_000_000)
	result.OverlayRender = &ref
	_, wire, err := finalJobOverlayHandoff(context.Background(), result, nil)
	if err != nil {
		t.Fatalf("the packed scene batch must reach the final-job handoff: %v", err)
	}
	manifests := wire["manifests"].([]any)
	if len(manifests) != 1 {
		t.Fatalf("manifests = %d, want one per certified scene batch", len(manifests))
	}
	document := manifests[0].(map[string]any)
	entries := document["entries"].([]any)
	entry := entries[0].(map[string]any)
	if entry["artifact_in_us"] != int64(1_263_000) || entry["artifact_out_us"] != int64(3_000_000) {
		t.Fatalf("entry = %v, want the packed artifact positions carried on the wire", entry)
	}
	asset := document["asset"].(map[string]any)
	frames, _ := asset["frame_count"].(int64)
	// 3,000,000us at 24/1 is exactly 72 frames — the same grid the worker
	// probes on the file, which is what its set validation compares against.
	if frames != 72 {
		t.Fatalf("asset frame_count = %v, want the derived 72 (a set with -1 is refused by the worker)", asset["frame_count"])
	}
}

func TestCertifyFinalJobOverlayBatchAcceptsPackedSceneComposite(t *testing.T) {
	items := []capoverlay.OverlayItem{
		{ID: "overlay-a", SceneID: "scene-1", Kind: "text_phrase", StartUS: 0, DurationUS: 1_000_000},
	}
	artifact := packedBatchArtifact(t, 6_000_000)
	ref := packedBatchReference(t, items, artifact)
	if err := certifiableFinalJobOverlayBatches(true, ref, 24, 1); err != nil {
		t.Fatalf("a packed scene composite with frame_count=-1 is the r64 green shape and must certify: %v", err)
	}
	// A frame count the worker did report must still cover the window it
	// advertises, so the relaxed check is not a blanket exemption.
	artifact.FrameCount = 24
	artifact.DurationUS = 1_000_000
	if err := certifiableFinalJobOverlayBatches(true, ref, 24, 1); err != nil {
		t.Fatalf("a counted artifact inside its duration must certify: %v", err)
	}
	// Non-final runs never consume the batch certificate set.
	if err := certifiableFinalJobOverlayBatches(false, RenderReference{Batches: []OverlayBatchRenderReference{{BatchID: "raw"}}}, 24, 1); err != nil {
		t.Fatalf("the batch certificate is a final-job gate only, got %v", err)
	}
}

func TestCertifyFinalJobOverlayBatchFailsClosed(t *testing.T) {
	base := func(t *testing.T) RenderReference {
		items := []capoverlay.OverlayItem{
			{ID: "overlay-a", SceneID: "scene-1", Kind: "text_phrase", StartUS: 0, DurationUS: 2_000_000},
		}
		return packedBatchReference(t, items, packedBatchArtifact(t, 6_000_000))
	}
	cases := []struct {
		name   string
		mutate func(*RenderReference)
		want   string
	}{
		{"missing manifest", func(r *RenderReference) { r.Batches[0].Manifest = nil }, "lacks a certified manifest"},
		{"missing artifact", func(r *RenderReference) { r.Batches[0].Artifact = nil }, "lacks a certified manifest"},
		{"short digest", func(r *RenderReference) { r.Batches[0].Artifact.SHA256 = "b1486eec" }, "not a certified artifact"},
		{"zero size", func(r *RenderReference) { r.Batches[0].Artifact.SizeBytes = 0 }, "not a certified artifact"},
		{"foreign bytes", func(r *RenderReference) {
			r.Batches[0].Manifest.TimelineSHA256 = strings.Repeat("c", 64)
		}, "certifies"},
		{"impossible frame count", func(r *RenderReference) { r.Batches[0].Artifact.FrameCount = -2 }, "impossible frame count"},
		{"negative duration", func(r *RenderReference) { r.Batches[0].Artifact.DurationUS = -1 }, "negative duration"},
		{"placement past the artifact end", func(r *RenderReference) {
			r.Batches[0].Artifact.DurationUS = 1_000_000
		}, "but the artifact lasts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := base(t)
			tc.mutate(&ref)
			err := certifiableFinalJobOverlayBatches(true, ref, 24, 1)
			if err == nil {
				t.Fatalf("%s must fail closed", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// TestFinalJobOverlayHandoffCertifiesSplitSetAndIsDeterministic pins both ends
// of the split wire: the certified batch set reaches the payload as a
// manifest-set document, and the SAME input builds byte-identical output on
// every attempt — the content address is the cache key, so a re-run must not
// be able to produce a different document from the same certified bytes.
func TestFinalJobOverlayHandoffCertifiesSplitSetAndIsDeterministic(t *testing.T) {
	items := []capoverlay.OverlayItem{
		{ID: "overlay-a", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "phrase", StartMs: 100, EndMs: 2100, Text: "first"},
		{ID: "overlay-b", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "phrase", StartMs: 3000, EndMs: 5000, Text: "second"},
	}
	build := func(t *testing.T) map[string]any {
		t.Helper()
		artifact := packedBatchArtifact(t, 6_000_000)
		result := ssotGateResult(items, artifact)
		ref := packedBatchReference(t, items, artifact)
		result.OverlayRender = &ref
		rows, wire, err := finalJobOverlayHandoff(context.Background(), result, nil)
		if err != nil {
			t.Fatalf("finalJobOverlayHandoff: %v", err)
		}
		if len(rows) != 0 {
			t.Fatalf("rows = %v, want the whole composition on the manifest-set wire", rows)
		}
		if wire["schema_version"] != "pipelinegen.overlay-manifest-set.v1" {
			t.Fatalf("schema_version = %v, want the batch-set document", wire["schema_version"])
		}
		manifests := wire["manifests"].([]any)
		if len(manifests) != 1 {
			t.Fatalf("manifests = %d, want one per scene batch", len(manifests))
		}
		entries := manifests[0].(map[string]any)["entries"].([]any)
		if len(entries) != len(items) {
			t.Fatalf("entries = %d, want every certified overlay placed", len(entries))
		}
		return wire
	}
	first := build(t)
	second := build(t)
	firstRaw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstRaw) != string(secondRaw) {
		t.Fatalf("the same certified batch set built two different documents:\n%s\n%s", firstRaw, secondRaw)
	}
	// Identity is entry-order independent: a planner that emits the same
	// overlays in another order still addresses the same stored artifact.
	reference := manifestOverlayRender(items, packedBatchArtifact(t, 6_000_000))
	reordered := *reference.Manifest
	reordered.Entries = []capoverlay.OverlayManifestEntry{reference.Manifest.Entries[1], reference.Manifest.Entries[0]}
	if reference.Manifest.Digest() != reordered.Digest() {
		t.Fatal("the manifest content address must not depend on entry order")
	}
}

// TestRenderOverlayPlanAtBoundaryNeverRendersTwiceForAnUncertifiableSet pins
// the r63/r69 cost: an advertised final-job batch set that does not certify is a
// DEFECTIVE artifact, not a shape the plan cannot express. Re-rendering the
// whole timeline to paper over it is the double GPU pass this test forbids —
// the boundary fails closed naming the batch and never calls the single path.
func TestRenderOverlayPlanAtBoundaryNeverRendersTwiceForAnUncertifiableSet(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PER_SCENE", "enabled")
	broken := RenderReference{
		JobID: "split-job", Status: "completed",
		Batches: []OverlayBatchRenderReference{{
			BatchID:  "batch-1",
			Artifact: &RenderArtifact{SHA256: strings.Repeat("b", 64), SizeBytes: 10, FrameCount: -1},
		}},
	}
	stub := &splitEnqueuerStub{splitRef: broken, singleRef: RenderReference{JobID: "single-job", Status: "completed"}}
	runner := &Runner{overlayRenderEnqueuer: stub, log: zap.NewNop()}
	ref, err := runner.renderOverlayPlanAtBoundary(context.Background(), GenerateRequest{FinalJob: true}, multiScenePointerFor(t))
	if err == nil {
		t.Fatal("an uncertifiable final-job split must fail closed, not silently re-render")
	}
	if !strings.Contains(err.Error(), "refusing a second render") {
		t.Fatalf("error = %v, want it to name the refused second render", err)
	}
	if stub.splitCalls != 1 || stub.singleCalls != 0 {
		t.Fatalf("split=%d single=%d, want exactly one render (no fallback re-render)", stub.splitCalls, stub.singleCalls)
	}
	if ref.JobID != "" {
		t.Fatalf("reference = %q, want no reference on a fail-closed boundary", ref.JobID)
	}
}

// TestRenderOverlayPlanAtBoundaryUsesACertifiedSplitWithoutARerender pins the
// r69 fix end to end: once the packed per-scene batches certify, the boundary
// uses them and never submits the single-timeline shape at all.
func TestRenderOverlayPlanAtBoundaryUsesACertifiedSplitWithoutARerender(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PER_SCENE", "enabled")
	plan := multiScenePointerFor(t)
	items := []capoverlay.OverlayItem{
		{ID: "phrase-a", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 26_283_000, DurationUS: 1_737_000},
		{ID: "phrase-b", SceneID: "scene-02", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 50_000_000, DurationUS: 2_000_000},
	}
	garlasco := garlascoPackedBatchReference(t, items[0], 3_000_000)
	second := packedBatchReference(t, items[1:], packedBatchArtifact(t, 2_000_000))
	// The second scene's canvas is 2 s of packed time for a 2 s plan window at
	// 50 s: the same packed shape every per-scene batch has.
	secondIn, secondOut := int64(0), int64(2_000_000)
	second.Batches[0].Manifest.Entries[0].ArtifactInUS = &secondIn
	second.Batches[0].Manifest.Entries[0].ArtifactOutUS = &secondOut
	certified := RenderReference{JobID: "split-job", Status: "completed", Batches: []OverlayBatchRenderReference{garlasco.Batches[0], second.Batches[0]}}
	stub := &splitEnqueuerStub{splitRef: certified, singleRef: RenderReference{JobID: "single-job", Status: "completed"}}
	runner := &Runner{overlayRenderEnqueuer: stub, log: zap.NewNop()}
	if err := finalJobSplitPreflight(true, *plan); err != nil {
		t.Fatalf("the r69 plan shape must pass the pre-render gate: %v", err)
	}
	ref, err := runner.renderOverlayPlanAtBoundary(context.Background(), GenerateRequest{FinalJob: true}, plan)
	if err != nil {
		t.Fatalf("a certified split must satisfy the boundary: %v", err)
	}
	if stub.singleCalls != 0 || ref.JobID != "split-job" {
		t.Fatalf("split=%d single=%d ref=%q, want the split used with no single-timeline render", stub.splitCalls, stub.singleCalls, ref.JobID)
	}
}

// TestFinalJobSplitPreflightRefusesAPlanWhoseBatchesCannotBePlaced pins the
// skip side: when a scene's items have no positive literal window the packed
// canvas cannot be authored, so the split is never submitted and the run pays
// for exactly one (single-timeline) render.
func TestFinalJobSplitPreflightRefusesAPlanWhoseBatchesCannotBePlaced(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PER_SCENE", "enabled")
	plan := splitTestPlan()
	plan.Items = append(plan.Items, capoverlay.OverlayItem{
		ID: "empty-window", SceneID: "scene-02", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
	})
	if err := finalJobSplitPreflight(true, plan); err == nil {
		t.Fatal("a scene whose batch cannot be authored must be refused before rendering")
	}
	stub := &splitEnqueuerStub{
		splitRef:  RenderReference{JobID: "split-job"},
		singleRef: RenderReference{JobID: "single-job", Status: "completed"},
	}
	runner := &Runner{overlayRenderEnqueuer: stub, log: zap.NewNop()}
	ref, err := runner.renderOverlayPlanAtBoundary(context.Background(), GenerateRequest{FinalJob: true}, &plan)
	if err != nil {
		t.Fatalf("a skipped split must go straight to the single path: %v", err)
	}
	if stub.splitCalls != 0 || stub.singleCalls != 1 || ref.JobID != "single-job" {
		t.Fatalf("split=%d single=%d ref=%q, want the preflight to skip the split entirely", stub.splitCalls, stub.singleCalls, ref.JobID)
	}
	// Non-final runs never consume the batch certificate set: the gate is a
	// final-job admission, not a new restriction on ordinary runs.
	if err := finalJobSplitPreflight(false, plan); err != nil {
		t.Fatalf("non-final preflight must pass, got %v", err)
	}
}

// TestRenderOverlayPlanAtBoundaryKeepsSplitForNonFinalRun pins that the
// certificate gate is scoped to final jobs: an ordinary run has no handoff to
// satisfy and must not pay an extra render for a shape it never reads.
func TestRenderOverlayPlanAtBoundaryKeepsSplitForNonFinalRun(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PER_SCENE", "enabled")
	broken := RenderReference{
		JobID: "split-job", Status: "completed",
		Batches: []OverlayBatchRenderReference{{BatchID: "batch-1"}},
	}
	stub := &splitEnqueuerStub{splitRef: broken, singleRef: RenderReference{JobID: "single-job", Status: "completed"}}
	runner := &Runner{overlayRenderEnqueuer: stub, log: zap.NewNop()}
	ref, err := runner.renderOverlayPlanAtBoundary(context.Background(), GenerateRequest{}, multiScenePointerFor(t))
	if err != nil {
		t.Fatalf("non-final boundary: %v", err)
	}
	if stub.singleCalls != 0 || ref.JobID != "split-job" {
		t.Fatalf("split=%d single=%d ref=%q, want the split kept for a non-final run", stub.splitCalls, stub.singleCalls, ref.JobID)
	}
}

// TestCertifyFinalJobOverlayBatchToleratesOneFrameOverhang pins the r70 case:
// a packed window that ends 12,5 ms past a 3,000,000us canvas is the manifest's
// ceil-to-frame rounding, not a defective artifact — the window is rounded UP
// to a frame edge while the duration is a frame count, so one frame of overhang
// is the largest honest rounding error. Anything past that frame still fails.
func TestCertifyFinalJobOverlayBatchToleratesOneFrameOverhang(t *testing.T) {
	item := capoverlay.OverlayItem{ID: "phrase-overlay", SceneID: "scene-01", Kind: "text_phrase", StartUS: 0, DurationUS: 3_000_000}
	ref := garlascoPackedBatchReference(t, item, 3_000_000)
	artifactIn, artifactOut := int64(1_275_000), int64(3_012_500)
	ref.Batches[0].Manifest.Entries[0].ArtifactInUS = &artifactIn
	ref.Batches[0].Manifest.Entries[0].ArtifactOutUS = &artifactOut
	if err := certifiableFinalJobOverlayBatches(true, ref, 24, 1); err != nil {
		t.Fatalf("12,5ms of overhang on a 3s canvas must certify: %v", err)
	}
	if got := finalJobArtifactWindowTolerance(24, 1); got != 41_666 {
		t.Fatalf("tolerance = %dus, want one 24fps frame", got)
	}
	// Two frames past the canvas is the same defect the strict check named.
	beyond := artifactOut + finalJobArtifactWindowTolerance(24, 1)
	ref.Batches[0].Manifest.Entries[0].ArtifactOutUS = &beyond
	if err := certifiableFinalJobOverlayBatches(true, ref, 24, 1); err == nil || !strings.Contains(err.Error(), "but the artifact lasts") {
		t.Fatalf("more than one frame of overhang must fail closed, got %v", err)
	}
	// An unknown frame rate must not silently widen the certificate.
	if got := finalJobArtifactWindowTolerance(0, 0); got != 0 {
		t.Fatalf("tolerance without a frame rate = %dus, want 0", got)
	}
}

// TestFinalJobManifestWireToleratesOneFrameOfReplaceWindow pins the r71 case:
// the same packed span measures 43 frames when both edges are ceil'd and 42
// when the certified duration is rounded to the frame grid, so a one-frame
// difference is arithmetic, not an artifact that cannot serve the window.
// Two frames past the budget still fail closed.
func TestFinalJobManifestWireToleratesOneFrameOfReplaceWindow(t *testing.T) {
	item := capoverlay.OverlayItem{
		ID: "phrase-overlay", SceneID: "scene-1", Kind: "text_phrase",
		StartUS: 1_275_000, DurationUS: 1_737_500,
	}
	ref := garlascoPackedBatchReference(t, item, 3_500_000)
	artifactIn, artifactOut := int64(1_275_000), int64(3_012_500)
	ref.Batches[0].Manifest.Entries[0].ArtifactInUS = &artifactIn
	ref.Batches[0].Manifest.Entries[0].ArtifactOutUS = &artifactOut
	ref.Batches[0].Artifact.FrameCount = -1
	ref.Batches[0].Artifact.DurationUS = 3_500_000
	ref.Batches[0].Artifact.SHA256 = strings.Repeat("d", 64)
	ref.Batches[0].Manifest.TimelineSHA256 = ref.Batches[0].Artifact.SHA256
	result := ssotGateResult([]capoverlay.OverlayItem{item}, ref.Batches[0].Artifact)
	result.OverlayRender = &RenderReference{Manifest: ref.Batches[0].Manifest, Batches: ref.Batches}
	if _, _, err := finalJobOverlayHandoff(context.Background(), result, nil); err != nil {
		t.Fatalf("a one-frame replace-window rounding must reach the wire: %v", err)
	}
	// Two frames past the budget: the artifact genuinely cannot serve it.
	beyond := artifactOut + 2*finalJobArtifactWindowTolerance(24, 1)
	ref.Batches[0].Manifest.Entries[0].ArtifactOutUS = &beyond
	result.OverlayRender = &RenderReference{Manifest: ref.Batches[0].Manifest, Batches: ref.Batches}
	if _, _, err := finalJobOverlayHandoff(context.Background(), result, nil); err == nil {
		t.Fatal("a replace window two frames past the artifact budget must fail closed")
	}
}
