package timeline

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func TestFitAudioIntentsToSceneDropsOutOfRangeClipAudio(t *testing.T) {
	intents := []audio.AudioIntent{
		{
			Mode: audio.AudioClip, ClipAssetID: "in-range",
			SourceDurationUS: 8_000_000, TimelineOffsetUS: 4_000_000,
			TimelineDurationUS: 8_000_000,
		},
		{
			Mode: audio.AudioClip, ClipAssetID: "at-boundary",
			SourceDurationUS: 3_000_000, TimelineOffsetUS: 10_000_000,
			TimelineDurationUS: 3_000_000,
		},
		{
			Mode: audio.AudioClip, ClipAssetID: "after-boundary",
			SourceDurationUS: 3_000_000, TimelineOffsetUS: 12_000_000,
			TimelineDurationUS: 3_000_000,
		},
	}

	got := fitAudioIntentsToScene(intents, 10_000_000)
	if len(got) != 1 {
		t.Fatalf("fitted intents = %+v, want only the in-range clip", got)
	}
	if got[0].ClipAssetID != "in-range" || got[0].TimelineDurationUS != 6_000_000 || got[0].SourceDurationUS != 6_000_000 {
		t.Fatalf("in-range intent = %+v, want duration clamped to remaining 6s", got[0])
	}
	if len(intents) != 3 || intents[0].TimelineDurationUS != 8_000_000 {
		t.Fatalf("input intents mutated: %+v", intents)
	}
}
