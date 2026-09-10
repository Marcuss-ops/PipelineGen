package cliprender

import (
	"context"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"strings"
	"testing"
)

func fullRenderOutcome() *RenderOutcome {
	return &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		DurationSec: 3,
		Width:       1920,
		Height:      1080,
		FPSNum:      24,
		FPSDen:      1,
		Backend:     BackendChrononVulkan,
		FFmpegMS:    1234,
	}
}

// TestWorker_DestinationSubfolder_ResolvedOncePerJob verifies the canonical
// script/batch destination rule: a request carrying
// destination.subfolder_name makes the worker resolve the leaf folder ONCE
// per job through the DestinationFolderResolver, and the publisher receives
// the fully-resolved leaf folder ID (the publisher never creates folders
// and never sees the raw subfolder name).
func TestWorker_DestinationSubfolder_ResolvedOncePerJob(t *testing.T) {
	w, _, _ := newTestWorker(t)
	w.WithRenderExecutor(&fakeRenderExecutor{outcome: fullRenderOutcome()})
	publisher := &fakeRenderPublisher{}
	w.WithRenderPublisher(publisher)
	resolver := &fakeDestinationFolderResolver{out: "leaf-script-folder-123"}
	w.WithDestinationFolderResolver(resolver)

	req := baseRenderRequest()
	req.Destination = &DestinationSpec{
		DriveFolderID: "root-folder-abc",
		SubfolderName: "Matt Damon 5 Clips Verification",
	}
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-folder-resolve", Payload: renderJobPayload(t, req)}, nil)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if result["phase"] != "rendered" {
		t.Fatalf("phase = %v, want rendered", result["phase"])
	}
	// Exactly one resolution per job, with the caller's root + raw name
	// (sanitisation is the adapter's job, not the worker's).
	if resolver.calls != 1 {
		t.Fatalf("destination resolver calls = %d, want 1 (once per job)", resolver.calls)
	}
	if resolver.input.RootFolderID != "root-folder-abc" || resolver.input.SubfolderName != "Matt Damon 5 Clips Verification" {
		t.Fatalf("resolver input = %+v, want root root-folder-abc + subfolder name", resolver.input)
	}
	// The publisher must receive the RESOLVED leaf, never the root and never
	// the raw subfolder name — it stays dumb by contract.
	if publisher.input.DriveFolderID != "leaf-script-folder-123" {
		t.Fatalf("publisher destination = %q, want the resolved leaf folder", publisher.input.DriveFolderID)
	}
}

// TestWorker_DestinationSubfolder_FailClosedWithoutResolver verifies that a
// subfolder_name declared without a wired DestinationFolderResolver is a
// typed failure before any preparation runs — the publisher must never
// silently fall back to a root upload when a script folder was requested.
func TestWorker_DestinationSubfolder_FailClosedWithoutResolver(t *testing.T) {
	w, mat, _ := newTestWorker(t) // no WithDestinationFolderResolver
	w.WithRenderExecutor(&fakeRenderExecutor{outcome: fullRenderOutcome()})

	req := baseRenderRequest()
	req.Destination = &DestinationSpec{DriveFolderID: "root-folder-abc", SubfolderName: "Some Script"}
	_, err := w.Handle(context.Background(), &job.Job{ID: "job-folder-nores", Payload: renderJobPayload(t, req)}, nil)
	if err == nil {
		t.Fatal("subfolder_name without a wired resolver must fail closed")
	}
	if !strings.Contains(err.Error(), "DestinationFolderResolver") {
		t.Fatalf("expected the resolver-missing typed error, got %v", err)
	}
	if len(mat.calls) != 0 {
		t.Errorf("preparation must not run when destination resolution fails, got %v", mat.calls)
	}
}

// TestWorker_DestinationLeafFolder_PassesThrough verifies the legacy
// behaviour: without destination.subfolder_name the request's
// destination.drive_folder_id IS the resolved leaf and is handed to the
// publisher verbatim (no resolution, no folder creation).
func TestWorker_DestinationLeafFolder_PassesThrough(t *testing.T) {
	w, _, _ := newTestWorker(t)
	w.WithRenderExecutor(&fakeRenderExecutor{outcome: fullRenderOutcome()})
	publisher := &fakeRenderPublisher{}
	w.WithRenderPublisher(publisher)
	resolver := &fakeDestinationFolderResolver{}
	w.WithDestinationFolderResolver(resolver)

	req := baseRenderRequest() // default destination = DefaultDriveRootFolderID
	if _, err := w.Handle(context.Background(), &job.Job{ID: "job-folder-passthrough", Payload: renderJobPayload(t, req)}, nil); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("destination resolver calls = %d, want 0 (no subfolder_name)", resolver.calls)
	}
	if publisher.input.DriveFolderID != DefaultDriveRootFolderID {
		t.Fatalf("publisher destination = %q, want the request leaf %q verbatim", publisher.input.DriveFolderID, DefaultDriveRootFolderID)
	}
}

// TestWorker_OverlayLineageProjectedIntoResult certifies Gate 7's final
// binding: a clip.render request that declares an overlay must surface the
// complete overlay lineage (render job id + plan fingerprint + render key +
// source video asset id) on the final video result alongside the published
// asset (final_video_asset_id + Drive file), so the final video asset proves
// WHICH overlay it composites.
func TestWorker_OverlayLineageProjectedIntoResult(t *testing.T) {
	w, _, _ := newTestWorker(t)
	renderer := &fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		DurationSec: 3,
		Width:       1920,
		Height:      1080,
		FPSNum:      24,
		FPSDen:      1,
		Backend:     BackendChrononVulkan,
	}}
	publisher := &fakeRenderPublisher{}
	w.WithRenderExecutor(renderer)
	w.WithRenderPublisher(publisher)

	req := baseRenderRequest()
	req.Overlay = &OverlayRefSpec{
		RenderJobID:        "render-michael-jordan-overlay-001",
		PlanFingerprint:    "fp-michael-jordan",
		RenderKey:          "rk-michael-jordan",
		SourceVideoAssetID: "source-video-asset-001",
		StartUS:            50000,
		EndUS:              950000,
	}
	resolver := &fakeOverlayResolver{segment: &OverlaySegment{
		RenderJobID: "render-michael-jordan-overlay-001",
		RenderKey:   "rk-michael-jordan",
		LocalPath:   "/work/overlay-segment.mp4",
		SHA256:      "segment-sha256",
		SizeBytes:   4096,
	}}
	compositor := &fakeOverlayCompositor{composite: &OverlayCompositeResult{
		OutputPath:  "/work/composited-clip.mp4",
		SHA256:      "composited-sha256",
		SizeBytes:   8192,
		CompositeMS: 137,
	}}
	w.WithOverlaySegmentResolver(resolver)
	w.WithOverlayCompositor(compositor)
	// Post-composite probe is mandatory when overlay is declared.
	w.WithOutputProber(&fakeOutputProber{probe: &OutputProbe{
		Container: "mp4", HasVideo: true, HasAudio: true,
		VideoCodec: "h264", VideoProfile: "high", PixelFormat: "yuv420p",
		Width: 1920, Height: 1080, FPS: 24.0, FPSNum: 24, FPSDen: 1,
		AudioCodec: "aac", AudioProfile: "LC", SampleRate: 48000, Channels: 2,
		ChannelLayout: "stereo", AudioBitrate: "128k",
		VideoStreams: 1, AudioStreams: 1, StartPTS: 0,
	}})

	result, err := w.Handle(context.Background(), &job.Job{ID: "job-overlay-lineage", Payload: renderJobPayload(t, req)}, nil)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	overlay, ok := result["overlay"].(map[string]any)
	if !ok {
		t.Fatalf("result must carry an overlay block, got %+v", result)
	}
	if overlay["render_job_id"] != "render-michael-jordan-overlay-001" {
		t.Errorf("overlay render_job_id = %v", overlay["render_job_id"])
	}
	if overlay["plan_fingerprint"] != "fp-michael-jordan" {
		t.Errorf("overlay plan_fingerprint = %v", overlay["plan_fingerprint"])
	}
	if overlay["render_key"] != "rk-michael-jordan" {
		t.Errorf("overlay render_key = %v", overlay["render_key"])
	}
	if overlay["source_video_asset_id"] != "source-video-asset-001" {
		t.Errorf("overlay source_video_asset_id = %v", overlay["source_video_asset_id"])
	}
	if overlay["start_us"] != int64(50000) || overlay["end_us"] != int64(950000) {
		t.Errorf("overlay window = %v..%v, want 50000..950000", overlay["start_us"], overlay["end_us"])
	}

	// The compositing pass must have been invoked with the exact declared
	// lineage + window: the resolver got the render_job_id, the compositor
	// got the resolved segment and the [start_us, end_us) window.
	if resolver.got.RenderJobID != "render-michael-jordan-overlay-001" || resolver.got.RenderKey != "rk-michael-jordan" {
		t.Errorf("overlay resolver input = %+v", resolver.got)
	}
	if compositor.got.Segment == nil || compositor.got.Segment.SHA256 != "segment-sha256" {
		t.Errorf("overlay compositor segment = %+v", compositor.got.Segment)
	}
	if compositor.got.StartUS != 50000 || compositor.got.EndUS != 950000 {
		t.Errorf("overlay compositor window = %d..%d, want 50000..950000", compositor.got.StartUS, compositor.got.EndUS)
	}
	if compositor.got.SourcePath != "/work/rendered-clip.mp4" {
		t.Errorf("overlay compositor source = %q", compositor.got.SourcePath)
	}
	if overlay["composited"] != true || overlay["sha256"] != "composited-sha256" || overlay["composite_ms"] != int64(137) {
		t.Errorf("overlay compositing facts = %v", overlay)
	}

	// The final video asset block carries the Drive identity of the derived
	// asset: source_video_asset_id (request) → final_video_asset_id + Drive.
	// The published file must be the COMPOSITED output, never the raw render.
	assetBlock, ok := result["asset"].(map[string]any)
	if !ok {
		t.Fatalf("result must carry a published asset block, got %+v", result)
	}
	if assetBlock["asset_id"] != "final-video-asset-001" {
		t.Errorf("final video asset_id = %v", assetBlock["asset_id"])
	}
	if assetBlock["drive_file_id"] != "drive-file-001" {
		t.Errorf("final video drive_file_id = %v", assetBlock["drive_file_id"])
	}
	if publisher.input.OutputPath != "/work/composited-clip.mp4" {
		t.Errorf("published output = %q, want the composited clip", publisher.input.OutputPath)
	}
	if result["source_asset_id"] != "asset-source" {
		t.Errorf("source_asset_id = %v", result["source_asset_id"])
	}
	// The full publication wall (probe + overlay + Drive upload) is folded
	// into the V2 report as publication_total_ms without overwriting the
	// renderer-owned finalize timing.
	renderBlock, ok := result["render"].(map[string]any)
	if !ok {
		t.Fatalf("result must carry a render block, got %+v", result["render"])
	}
	if metrics, ok := renderBlock["metrics_v2"].(*RenderMetricsV2); ok && metrics != nil {
		if int64(metrics.PublicationTotalMS) == NotInstrumented {
			t.Fatalf("publication_total_ms = %d, want the measured publication wall", int64(metrics.PublicationTotalMS))
		}
		if int64(metrics.ArtifactPublishMS) == NotInstrumented {
			t.Fatalf("artifact_publish_ms = %d, want the measured publisher boundary", int64(metrics.ArtifactPublishMS))
		}
		if int64(metrics.RendererOutputFinalizeMS) != NotInstrumented {
			t.Fatalf("renderer_finalize_ms = %d, must not be overwritten by worker publication timing", int64(metrics.RendererOutputFinalizeMS))
		}
	} else {
		t.Fatalf("metrics_v2 = %v, want the V2 report in the render block", renderBlock["metrics_v2"])
	}
}

// TestWorker_OverlayCompositing_FailClosedWithoutWiring certifies the
// fail-closed half of compositing: a request that declares an overlay but
// arrives at a worker without a segment resolver (or compositor) fails with
// a typed error — the final video never claims an overlay it cannot
// composite.
func TestWorker_OverlayCompositing_FailClosedWithoutWiring(t *testing.T) {
	for name, wire := range map[string]func(*Worker){
		"no resolver": func(w *Worker) {},
		"no compositor": func(w *Worker) {
			w.WithOverlaySegmentResolver(&fakeOverlayResolver{segment: &OverlaySegment{
				RenderJobID: "render-job-001", RenderKey: "key-001", LocalPath: "/work/seg.mp4", SHA256: "s",
			}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, _, _ := newTestWorker(t)
			w.WithRenderExecutor(&fakeRenderExecutor{outcome: &RenderOutcome{
				OutputPath:  "/work/rendered-clip.mp4",
				SizeBytes:   4096,
				DurationSec: 3,
				Width:       1920,
				Height:      1080,
				FPSNum:      24,
				FPSDen:      1,
				Backend:     BackendChrononVulkan,
			}})
			wire(w)

			req := baseRenderRequest()
			req.Overlay = &OverlayRefSpec{
				RenderJobID:        "render-job-001",
				PlanFingerprint:    "fp-001",
				RenderKey:          "key-001",
				SourceVideoAssetID: "source-video-001",
				StartUS:            50000,
				EndUS:              950000,
			}
			_, err := w.Handle(context.Background(), &job.Job{ID: "job-overlay-fail", Payload: renderJobPayload(t, req)}, nil)
			if err == nil {
				t.Fatal("overlay declared without compositing wiring must fail")
			}
		})
	}
}

// TestWorker_RequireGPU_FailsClosedOnSoftwareBackend certifies the
// ExecutionSpec.RequireGPU contract: a request demanding GPU must never be
// served by the software FFmpeg fallback. The worker reports the specific
// require_gpu violation (before the unconditional Chronon-only gate).
func TestWorker_RequireGPU_FailsClosedOnSoftwareBackend(t *testing.T) {
	w, _, _ := newTestWorker(t)
	w.WithRenderExecutor(&fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		DurationSec: 3,
		Width:       1920,
		Height:      1080,
		FPSNum:      24,
		FPSDen:      1,
		Backend:     BackendFFmpegFallback,
	}})

	req := baseRenderRequest()
	req.Execution = &ExecutionSpec{RequireGPU: true}
	_, err := w.Handle(context.Background(), &job.Job{ID: "job-require-gpu", Payload: renderJobPayload(t, req)}, nil)
	if err == nil {
		t.Fatal("execution.require_gpu=true with a software backend must fail closed")
	}
	if !strings.Contains(err.Error(), "require_gpu") {
		t.Fatalf("expected the require_gpu typed violation, got %v", err)
	}
	if !strings.Contains(err.Error(), string(BackendFFmpegFallback)) {
		t.Fatalf("require_gpu error must name the resolved backend, got %v", err)
	}
}

// TestWorker_RequireGPU_SucceedsOnGPUBackend certifies the happy path: a
// require_gpu request renders normally when the resolved backend is the GPU
// Chronon backend.
func TestWorker_RequireGPU_SucceedsOnGPUBackend(t *testing.T) {
	w, _, _ := newTestWorker(t)
	w.WithRenderExecutor(&fakeRenderExecutor{outcome: fullRenderOutcome()}) // BackendChrononVulkan

	req := baseRenderRequest()
	req.Execution = &ExecutionSpec{RequireGPU: true}
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-require-gpu-ok", Payload: renderJobPayload(t, req)}, nil)
	if err != nil {
		t.Fatalf("require_gpu=true on the GPU backend must succeed, got %v", err)
	}
	if result["phase"] != "rendered" {
		t.Fatalf("phase = %v, want rendered", result["phase"])
	}
}
