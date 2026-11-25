package audio

import (
	"encoding/json"
	"reflect"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scenecontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/scene"
)

func TestConfigRoundTripPreservesAudioFields(t *testing.T) {
	config := Config{
		Audio:                capabilityaudio.AudioMode("voiceover"),
		VoiceoverLanguages:   []scenecontract.Language{"en", "it"},
		RandomSFXOnClipStart: true, RandomSFXOnImageOverlay: true, RandomSFXOnPhraseOverlay: true,
		MixPolicy:         capabilityaudio.AudioMixPolicy("voiceover_ducked_clip"),
		VoiceoverFolderID: "folder", TTSRate: "1.1",
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Config
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config, decoded) {
		t.Fatalf("config round-trip mismatch:\n got %+v\nwant %+v", decoded, config)
	}
}

func TestOutputRoundTripPreservesFinalAudio(t *testing.T) {
	output := Output{
		AudioMode: capabilityaudio.AudioMode("voiceover"),
		FinalAudio: &FinalAudioReference{
			AssetID: "audio", FinalAudioSHA256: "sha", DurationMS: 1000,
			CopyEligible: true, FinalMix: true,
		},
		CompileTimings: CompileTimings{AudioAssetResolveMS: 1, TimelineCompileMS: 2},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Output
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(output, decoded) {
		t.Fatalf("output round-trip mismatch:\n got %+v\nwant %+v", decoded, output)
	}
}
