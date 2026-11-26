package scriptgeneration

import (
	"context"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// failOnOverlayFetch fails the test if the local join asks for a certified
// overlay clip. A final_job must return before any fetch.
type failOnOverlayFetch struct {
	t *testing.T
}

func (f *failOnOverlayFetch) FetchOverlayClip(context.Context, contract.RenderArtifact, string) error {
	f.t.Fatal("a final_job must not fetch overlay clips for a local final join")
	return nil
}

// TestAssembleFinalVideoSkipsLocalJoinForFinalJob pins the boundary in the
// persistence path: the complete final video is Master-owned, so a final_job
// never joins locally even when the run has a complete overlay render, a
// resolvable plan and certified final audio. The fail-on-fetch fixture proves
// the early return happens before I/O; the old local-first default reached the
// fetch and would fail this test.
func TestAssembleFinalVideoSkipsLocalJoinForFinalJob(t *testing.T) {
	runner := &Runner{}
	runner.SetOverlayClipFetcher(&failOnOverlayFetch{t: t})

	result := &contract.GenerateResult{
		OverlayRender: &contract.RenderReference{
			JobID: "render-job-1",
			Items: []contract.OverlayItemRenderReference{{
				ItemID:   "item-1",
				JobID:    "render-job-1",
				Status:   "SUCCEEDED",
				Artifact: &contract.RenderArtifact{ID: "artifact-1", StorageKey: "object/artifact-1.mp4"},
			}},
		},
		OverlayPlan: &capmodel.OverlayPlan{
			PlanID:     "plan-1",
			Width:      1920,
			Height:     1080,
			FPSNum:     24,
			FPSDen:     1,
			DurationMS: 2000,
			Items:      []capmodel.OverlayItem{{ID: "item-1", StartMs: 0, EndMs: 2000}},
		},
		AudioOutput: contract.AudioOutput{
			FinalAudio: &contract.FinalAudioReference{Path: "/tmp/final_audio.m4a", DurationUS: 2_000_000},
		},
	}
	req := contract.GenerateRequest{
		FinalJob: true,
		Render:   scriptpkg.VideoRenderSpec{Enabled: true},
	}

	if err := runner.assembleFinalVideo(context.Background(), "run-final-job", req, result); err != nil {
		t.Fatalf("assembleFinalVideo() error = %v, want nil", err)
	}
	if result.LocalFinalVideo != nil {
		t.Fatalf("final_job produced a local final video at %q", result.LocalFinalVideo.Path)
	}
}
