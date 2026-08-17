package scriptgeneration

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestDurableResultToDomainPreservesOutputTextAndWordCount(t *testing.T) {
	in := &GenerateResult{
		Output:    GenerateOutput{Text: "La civiltà Maya prosperò.", WordCount: 4},
		WordCount: 99,
	}

	out := DurableResultToDomain(in)
	require.NotNil(t, out)
	assert.Equal(t, "La civiltà Maya prosperò.", out.Output.Text)
	assert.Equal(t, 4, out.Output.WordCount)
}

// TestDurableResultToDomainMapsEntitiesIntoArtifacts certifies the durable
// entity surfacing fix end-to-end: the entity aggregate carried on the durable
// GenerateResult must reach the domain Artifacts.Entities block so the
// persisted domain surface and the wire result agree on the same typed
// persons/places/concepts projection.
func TestDurableResultToDomainMapsEntitiesIntoArtifacts(t *testing.T) {
	in := &GenerateResult{
		Entities: &scriptpkg.EntityResult{
			Persons:  []scriptpkg.Entity{{Value: "Jackie Chan", Type: "PERSON"}},
			Places:   []scriptpkg.Entity{{Value: "Hong Kong", Type: "LOCATION"}},
			Concepts: []scriptpkg.Entity{{Value: "martial arts", Type: "CONCEPT"}},
		},
	}
	out := DurableResultToDomain(in)
	require.NotNil(t, out)
	require.NotNil(t, out.Artifacts.Entities, "durable entities must be mapped into Artifacts.Entities")
	assert.Equal(t, "Jackie Chan", out.Artifacts.Entities.Persons[0].Value)
	assert.Equal(t, "Hong Kong", out.Artifacts.Entities.Places[0].Value)
	assert.Equal(t, "martial arts", out.Artifacts.Entities.Concepts[0].Value)
}

// TestDurableResultToDomainMapsTimingBundle certifies the published timing
// bundle (timing.json SSOT + optional SRT/VTT links + hashes) reaches the
// legacy domain VoiceoverBinding.Timing surface so the persisted domain
// envelope exposes the same timing references as the durable capability
// result.
func TestDurableResultToDomainMapsTimingBundle(t *testing.T) {
	in := &GenerateResult{
		Scenes: []Scene{{
			ID:    "scene-0",
			Index: 0,
			Text:  map[Language]string{"en": "Hello world."},
			Voiceover: map[Language]AudioReference{
				"en": {
					URL:      "https://drive.google.com/file/d/voice-en/view",
					FilePath: "/tmp/voice-en.mp3",
					Duration: 1.0,
					TimingBundle: &scriptpkg.VoiceoverTimingBinding{
						Status:       "completed",
						JSONLink:     "https://drive.google.com/file/d/timing-en-json/view",
						SRTLink:      "https://drive.google.com/file/d/timing-en-srt/view",
						VTTLink:      "https://drive.google.com/file/d/timing-en-vtt/view",
						BoundaryMode: "word",
						WordCount:    2,
						TextSHA256:   "text-hash",
						AudioSHA256:  "audio-hash",
					},
				},
			},
		}},
	}

	out := DurableResultToDomain(in)
	require.NotNil(t, out)
	require.Len(t, out.Output.SpecScene.Scenes, 1)
	binding := out.Output.SpecScene.Scenes[0].Bindings.Voiceover
	require.NotNil(t, binding, "voiceover binding must be mapped")
	require.Contains(t, binding.Timing, "en", "timing bundle must be mapped per language")
	timing := binding.Timing["en"]
	assert.Equal(t, "completed", timing.Status)
	assert.Equal(t, "https://drive.google.com/file/d/timing-en-json/view", timing.JSONLink)
	assert.Equal(t, "https://drive.google.com/file/d/timing-en-srt/view", timing.SRTLink)
	assert.Equal(t, "https://drive.google.com/file/d/timing-en-vtt/view", timing.VTTLink)
	assert.Equal(t, "word", timing.BoundaryMode)
	assert.Equal(t, 2, timing.WordCount)
	assert.Equal(t, "text-hash", timing.TextSHA256)
	assert.Equal(t, "audio-hash", timing.AudioSHA256)
}

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
