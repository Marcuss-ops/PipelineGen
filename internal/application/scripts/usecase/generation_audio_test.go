package usecase

import (
	"context"
	"os"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type audioPlanProcessorStub struct {
	plan capabilityaudio.CompiledAudioPlan
}

func (s *audioPlanProcessorStub) MergeInputs(context.Context, []string, string) error { return nil }
func (s *audioPlanProcessorStub) RemoveSilence(context.Context, string, string) error { return nil }
func (s *audioPlanProcessorStub) Probe(context.Context, string) (*mediaexec.MediaInfo, error) {
	return &mediaexec.MediaInfo{}, nil
}
func (s *audioPlanProcessorStub) RenderAudioPlan(_ context.Context, plan capabilityaudio.CompiledAudioPlan, _ capabilityaudio.ResolvedAudioAssets, _ string) (capabilityaudio.FinalAudioAsset, error) {
	s.plan = plan
	return capabilityaudio.FinalAudioAsset{AssetID: "final", AudioContractVersion: capabilityaudio.AudioContractVersion, AudioPlanVersion: plan.Version, AudioPlanSHA256: plan.PlanSHA256, FinalAudioSHA256: "hash", Codec: plan.Output.Codec, Profile: plan.Output.Profile, SampleRate: plan.Output.SampleRate, Channels: plan.Output.Channels, ChannelLayout: plan.Output.ChannelLayout, Bitrate: 128000, DurationMS: plan.DurationUS / 1000, SizeBytes: 10, FinalMix: true, CopyEligible: true, StartPTS: 0}, nil
}

func TestRenderCombinedAudioCompilesExplicitVoiceoverIntent(t *testing.T) {
	stub := &audioPlanProcessorStub{}
	uc := &GenerateOneUseCase{audioProcessor: stub}
	result := &scriptpkg.GenerationResult{Output: scriptpkg.ScriptOutput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{ID: "scene-1", Index: 0, AudioMode: "VOICEOVER", Bindings: scriptpkg.SceneBindings{Voiceover: &scriptpkg.VoiceoverBinding{Status: "completed", LocalPath: testAudioPath(t), DurationMs: 1000}}}}}}}
	item := scriptpkg.GenerationItemV2{ID: "item-1", Language: "en"}
	if err := uc.renderCombinedAudio(context.Background(), item, result, nil); err != nil {
		t.Fatal(err)
	}
	if stub.plan.DurationUS != 1000000 || len(stub.plan.Events) != 1 || result.FinalAudio == nil || !result.FinalAudio.CopyEligible {
		t.Fatalf("plan=%+v audio=%+v", stub.plan, result.FinalAudio)
	}
}

func testAudioPath(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/voice.mp3"
	if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
