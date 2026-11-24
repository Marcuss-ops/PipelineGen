package scriptgeneration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestBuildFinalJobPayloadsRejectsFolderOnlyStockBinding pins the concrete
// visual contract: a folder is only a selection surface, never a renderable
// source. The Producer must refuse it rather than synthesize an asset without
// a SHA, size or duration.
func TestBuildFinalJobPayloadsRejectsFolderOnlyStockBinding(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{DurationUS: 2_000_000, Segments: []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 2_000_000}}},
		FinalAudio:        certifiedFinalAudio(2000),
		Scenes:            []Scene{{ID: "scene-1", Stock: &scriptpkg.StockBinding{FolderID: "folder-1", StartMs: 0, EndMs: 2000, DurationMs: 2000}}},
	}
	_, _, err := BuildFinalJobPayloads(context.Background(), "run-boundary", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err == nil || !strings.Contains(err.Error(), "folder-only stock binding") {
		t.Fatalf("BuildFinalJobPayloads error = %v, want fail-closed folder-only rejection", err)
	}
}

// TestBuildFinalJobPayloadsCarriesStockBindingWindow proves the Producer is a
// pass-through: the plan carries the stockpipeline's exact window, including a
// partial window inside a longer saved file, and never pads it to whole-file.
func TestBuildFinalJobPayloadsCarriesStockBindingWindow(t *testing.T) {
	const windowStartMS, windowEndMS = int64(1000), int64(4000)
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{DurationUS: 3_000_000, Segments: []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 3_000_000}}},
		FinalAudio:        certifiedFinalAudio(3000),
		Scenes:            []Scene{{ID: "scene-1", Stock: concreteStockBinding("drive-asset-5000", windowStartMS, windowEndMS)}},
	}
	pre, _, err := BuildFinalJobPayloads(context.Background(), "run-boundary", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	plan := decodeFinalJobPlanForTest(t, pre)
	if len(plan.VideoTracks) != 1 || len(plan.VideoTracks[0].Segments) != 1 {
		t.Fatalf("compiled stock segments = %+v, want one pass-through window", plan.VideoTracks)
	}
	segment := plan.VideoTracks[0].Segments[0]
	if segment.SourceInUS != windowStartMS*1000 || segment.SourceDurationUS != (windowEndMS-windowStartMS)*1000 || segment.FrameCount != 72 {
		t.Fatalf("stock window = %d+%d over %d frames, want %d+%d over 72 frames", segment.SourceInUS, segment.SourceDurationUS, segment.FrameCount, windowStartMS*1000, (windowEndMS-windowStartMS)*1000)
	}
	var stock *finalJobPlanAsset
	for i := range plan.Assets {
		if plan.Assets[i].Kind == "video" && plan.Assets[i].DurationUS == 5_000_000 {
			stock = &plan.Assets[i]
			break
		}
	}
	if stock == nil {
		t.Fatalf("compiled plan has no 5s stock asset: %+v", plan.Assets)
	}
}

// TestFinalJobProducerDoesNotOwnStockPlanning is the forward-prevention gate:
// the Producer final-job files must not reintroduce folder listing, file
// selection or stock-duration alignment.
func TestFinalJobProducerDoesNotOwnStockPlanning(t *testing.T) {
	files := []string{
		"final_job.go",
		"final_job_payload.go",
		"final_job_compiled_plan.go",
		"final_job_compiled_plan_assets.go",
		"final_job_source.go",
	}
	forbidden := []string{
		"finalJobStockPlanner",
		"stockPlanner.sceneClips(",
		"ListFinalJobStockFolder(",
		"alignFinalJobStockSceneDurations",
	}
	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join("finaljob", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, symbol := range forbidden {
			if strings.Contains(string(raw), symbol) {
				t.Errorf("%s still owns Producer-side stock planning symbol %q", name, symbol)
			}
		}
	}
}
