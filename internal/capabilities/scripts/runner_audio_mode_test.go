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
	return FinalAudioReference{AssetID: "final-audio-1", Path: "/tmp/final_audio.m4a", AudioContractVersion: capabilityaudio.AudioContractVersion, AudioPlanVersion: plan.Version, PlanSHA256: plan.PlanSHA256, FinalAudioSHA256: "0000000000000000000000000000000000000000000000000000000000000000", Codec: plan.Output.Codec, Profile: plan.Output.Profile, SampleRate: plan.Output.SampleRate, Channels: plan.Output.Channels, ChannelLayout: plan.Output.ChannelLayout, Bitrate: 128000, DurationMS: plan.DurationMS, StartPTS: 0, SizeBytes: 1, FinalMix: true, CopyEligible: true}, AudioPipelineMetrics{AudioDurationMS: plan.DurationMS}, nil
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
