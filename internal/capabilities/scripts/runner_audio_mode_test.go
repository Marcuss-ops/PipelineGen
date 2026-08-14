package scriptgeneration

import (
	"context"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

type stubCombinedAudioRenderer struct{ calls int }

func (s *stubCombinedAudioRenderer) Render(_ context.Context, plan capabilityaudio.CompiledAudioPlan, _ capabilityaudio.ResolvedAudioAssets) (FinalAudioReference, AudioPipelineMetrics, error) {
	s.calls++
	return FinalAudioReference{AssetID: "final-audio-1", Path: "/tmp/final_audio.m4a", AudioContractVersion: capabilityaudio.AudioContractVersion, AudioPlanVersion: plan.Version, PlanSHA256: plan.PlanSHA256, FinalAudioSHA256: "0000000000000000000000000000000000000000000000000000000000000000", Codec: plan.Output.Codec, Profile: plan.Output.Profile, SampleRate: plan.Output.SampleRate, Channels: plan.Output.Channels, ChannelLayout: plan.Output.ChannelLayout, Bitrate: 128000, DurationMS: plan.DurationUS / 1000, StartPTS: 0, SizeBytes: 1, FinalMix: true, CopyEligible: true}, AudioPipelineMetrics{AudioDurationMS: plan.DurationUS / 1000}, nil
}

func TestRunnerCombinedTimelineRequiresCertifiedRenderer(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	runID := "run-combined-no-renderer"
	if err := repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}); err != nil {
		t.Fatal(err)
	}
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	if final.Status != RunStatusFailed || final.FailedStage != StageBuildingRenderPayload {
		t.Fatalf("status=%s failed_stage=%s", final.Status, final.FailedStage)
	}
}

func TestRunnerCombinedTimelinePersistsFinalAudioReference(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	renderer := &stubCombinedAudioRenderer{}
	runner.SetCombinedAudioRenderer(renderer)
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	runID := "run-combined-renderer"
	if err := repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}); err != nil {
		t.Fatal(err)
	}
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	if final.Status != RunStatusCompleted {
		t.Fatalf("status=%s error=%s", final.Status, final.ErrorMessage)
	}
	if renderer.calls != 1 || final.Result == nil || final.Result.FinalAudio == nil || !final.Result.FinalAudio.CopyEligible {
		t.Fatalf("combined audio was not persisted: calls=%d result=%+v", renderer.calls, final.Result)
	}
}

// TestRunnerFinalAudioTimelineAndRenderPlanShareOneAsset certifies the
// production runner flow (not just the compile helpers) keeps a single
// master asset across the three audio-side artifacts:
//
//	final_audio.m4a   (CombinedAudioRenderer → result.FinalAudio)
//	CanonicalTimeline (result.CanonicalTimeline / result.AudioPlan)
//	RenderPlan.FinalAudio (result.RenderPlan.FinalAudio)
//
// The renderer produces final_audio.m4a certified against the canonical
// plan; the render plan must then carry the exact same audio_asset_id,
// plan hash, and file hash so the video executor muxes the same file.
func TestRunnerFinalAudioTimelineAndRenderPlanShareOneAsset(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	renderer := &stubCombinedAudioRenderer{}
	runner.SetCombinedAudioRenderer(renderer)
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	runID := "run-combined-same-asset"
	if err := repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}); err != nil {
		t.Fatal(err)
	}
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	if final.Status != RunStatusCompleted {
		t.Fatalf("status=%s error=%s", final.Status, final.ErrorMessage)
	}

	res := final.Result
	if res == nil || res.FinalAudio == nil || res.RenderPlan == nil || res.RenderPlan.FinalAudio == nil || res.AudioPlan == nil || res.CanonicalTimeline == nil {
		t.Fatalf("result missing final_audio/render_plan/audio_plan/timeline: %+v", res)
	}

	// 1) final_audio.m4a and RenderPlan.FinalAudio share one audio_asset_id.
	if res.RenderPlan.FinalAudio.AssetID != res.FinalAudio.AssetID {
		t.Fatalf("render plan final audio asset_id=%q, want final_audio asset_id=%q", res.RenderPlan.FinalAudio.AssetID, res.FinalAudio.AssetID)
	}

	// 2) The render plan's final audio is the same master: same plan hash,
	//    same file hash, same path.
	if res.RenderPlan.FinalAudio.PlanSHA256 != res.AudioPlan.PlanSHA256 || res.RenderPlan.FinalAudio.SHA256 != res.FinalAudio.FinalAudioSHA256 || res.RenderPlan.FinalAudio.Path != res.FinalAudio.Path {
		t.Fatalf("render plan final audio diverges: %+v vs %+v", res.RenderPlan.FinalAudio, res.FinalAudio)
	}

	// 3) final_audio.m4a was certified against this exact canonical plan.
	if res.FinalAudio.PlanSHA256 != res.AudioPlan.PlanSHA256 {
		t.Fatalf("final_audio plan_sha256=%q, want %q", res.FinalAudio.PlanSHA256, res.AudioPlan.PlanSHA256)
	}

	// 4) The render plan embeds the same canonical timeline the audio plan
	//    was compiled from.
	timelineHash, err := res.CanonicalTimeline.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if res.RenderPlan.TimelineHash != timelineHash {
		t.Fatalf("render plan timeline hash=%q, want canonical %q", res.RenderPlan.TimelineHash, timelineHash)
	}
}
