package scriptgeneration

import (
	"encoding/json"
	"testing"
)

// TestDurableResultToDomainPreservesFinalAudioContainerAndDurationUS
// certifies the persisted final_audio block carries the two certified
// certification fields (container + duration_us) alongside the existing
// codec/profile/hash surface, so the technical JSON emitted to consumers is
// the same block the document renders — never a re-derived copy.
func TestDurableResultToDomainPreservesFinalAudioContainerAndDurationUS(t *testing.T) {
	in := &GenerateResult{
		FinalAudio: &FinalAudioReference{
			AssetID:       "final-audio-it",
			Path:          "/tmp/final_audio_it.m4a",
			Container:     "m4a",
			Codec:         "aac",
			Profile:       "LC",
			SampleRate:    48000,
			Channels:      2,
			ChannelLayout: "stereo",
			DurationUS:    45_000_000,
			DurationMS:    45_000,
			FinalMix:      true,
			CopyEligible:  true,
		},
	}

	out := DurableResultToDomain(in)
	if out == nil || out.FinalAudio == nil {
		t.Fatal("final audio was not mapped to the domain result")
	}
	if out.FinalAudio.Container != "m4a" || out.FinalAudio.DurationUS != 45_000_000 {
		t.Fatalf("container/duration_us not preserved: %+v", out.FinalAudio)
	}

	encoded, err := json.Marshal(out.FinalAudio)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["container"] != "m4a" {
		t.Fatalf("container missing from persisted final_audio JSON: %s", encoded)
	}
	if wire["duration_us"] != float64(45_000_000) {
		t.Fatalf("duration_us missing from persisted final_audio JSON: %s", encoded)
	}
}
