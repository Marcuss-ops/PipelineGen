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
	if final.Status != RunStatusFailed || final.FailedStage != StageCompilingAudio {
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

// TestRunnerFinalAudioTimelineAndAudioPlanShareOneAsset certifies the
// production runner flow (not just the compile helpers) keeps a single
// master asset across the audio-side artifacts:
//
//	final_audio.m4a        (CombinedAudioRenderer → result.FinalAudio)
//	CanonicalTimeline      (result.CanonicalTimeline)
//	AudioPlan              (result.AudioPlan)
//
// The renderer produces final_audio.m4a certified against the canonical
// plan; the final audio reference must carry the exact same plan hash so
// the certified master is the one and only audio artifact. PipelineGen is
// audio-only: there is no video render surface anymore.
func TestRunnerFinalAudioTimelineAndAudioPlanShareOneAsset(t *testing.T) {
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
	if res == nil || res.FinalAudio == nil || res.AudioPlan == nil || res.CanonicalTimeline == nil {
		t.Fatalf("result missing final_audio/audio_plan/timeline: %+v", res)
	}

	// final_audio.m4a was certified against this exact canonical plan.
	if res.FinalAudio.PlanSHA256 != res.AudioPlan.PlanSHA256 {
		t.Fatalf("final_audio plan_sha256=%q, want %q", res.FinalAudio.PlanSHA256, res.AudioPlan.PlanSHA256)
	}
}

// TestRunnerCombinedTimelineFeedsAudioMetrics certifies the runner persists
// AudioPipelineMetrics for a COMBINED_TIMELINE run and exercises the
// compile-timing + renderer-metric merge path. Wall-clock assertions are
// intentionally limited to the deterministic renderer fields; the >0 checks
// for mix/encode/probe/hash/upload/compile timings are done by the live
// observability certification.
func TestRunnerCombinedTimelineFeedsAudioMetrics(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	runID := "run-combined-metrics"
	if err := repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}); err != nil {
		t.Fatal(err)
	}
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	if final.Status != RunStatusCompleted {
		t.Fatalf("status=%s error=%s", final.Status, final.ErrorMessage)
	}
	if final.Result == nil || final.Result.AudioMetrics == nil {
		t.Fatalf("audio metrics must be persisted for COMBINED_TIMELINE: %+v", final.Result)
	}
	if final.Result.AudioMetrics.AudioDurationMS <= 0 {
		t.Fatalf("renderer audio duration must be durable: %+v", final.Result.AudioMetrics)
	}
}
