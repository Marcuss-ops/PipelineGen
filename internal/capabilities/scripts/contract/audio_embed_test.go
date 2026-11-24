package contract

import (
	"encoding/json"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func TestGenerateRequestEmbedsAudioConfigWithFlatWireKeys(t *testing.T) {
	req := GenerateRequest{AudioConfig: AudioConfig{
		Audio:              capabilityaudio.AudioModeCombinedTimeline,
		VoiceoverLanguages: []Language{"it"},
		MixPolicy:          capabilityaudio.MixVoiceoverWithDuckedClip,
		VoiceoverFolderID:  "folder-1",
		TTSRate:            "+5%",
	}}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"audio_mode", "voiceover_languages", "mix_policy", "voiceover_folder_id", "tts_rate"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("request JSON lost flat key %q: %s", key, raw)
		}
	}
	if _, ok := fields["AudioConfig"]; ok {
		t.Fatalf("request JSON nested the audio config under its field name: %s", raw)
	}
	var decoded GenerateRequest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Audio != req.Audio || decoded.MixPolicy != req.MixPolicy || decoded.VoiceoverFolderID != req.VoiceoverFolderID {
		t.Fatalf("request round-trip lost audio fields: %+v", decoded.AudioConfig)
	}
}

func TestGenerateResultEmbedsAudioOutputWithFlatWireKeys(t *testing.T) {
	result := GenerateResult{AudioOutput: AudioOutput{
		AudioMode:     capabilityaudio.AudioModeCombinedTimeline,
		AudioStrategy: capabilityaudio.FinalAudioCopy,
		FinalAudio:    &FinalAudioReference{AssetID: "final-audio-1"},
		AudioMetrics:  &AudioPipelineMetrics{MixMS: 12},
		AudioPrefetch: &AudioPrefetchResult{BGMRequested: 1, BGMSFXResolved: 1, DurationMS: 7},
	}}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"audio_mode", "audio_strategy", "final_audio", "audio_metrics", "audio_prefetch"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("result JSON lost flat key %q: %s", key, raw)
		}
	}
	if _, ok := fields["AudioOutput"]; ok {
		t.Fatalf("result JSON nested the audio output under its field name: %s", raw)
	}
	var decoded GenerateResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AudioMode != result.AudioMode || decoded.FinalAudio == nil || decoded.FinalAudio.AssetID != "final-audio-1" {
		t.Fatalf("result round-trip lost audio fields: %+v", decoded.AudioOutput)
	}
	if decoded.AudioMetrics == nil || decoded.AudioMetrics.MixMS != 12 {
		t.Fatalf("result round-trip lost audio metrics: %+v", decoded.AudioMetrics)
	}
	if decoded.AudioPrefetch == nil || decoded.AudioPrefetch.BGMSFXResolved != 1 {
		t.Fatalf("result round-trip lost audio prefetch: %+v", decoded.AudioPrefetch)
	}
}
