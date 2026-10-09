package scriptgeneration

import (
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func TestRandomClipStartSFXIntentsFollowRealVideoBoundaries(t *testing.T) {
	timeline := capabilityaudio.CanonicalTimeline{
		Version:    capabilityaudio.TimelineVersion,
		DurationUS: 5_000_000,
		Segments: []capabilityaudio.TimelineSegment{
			{ID: "scene-a", Index: 0, TimelineStartUS: 0, DurationUS: 2_000_000,
				VideoSegments: []capabilityaudio.VideoSegment{
					{AssetID: "clip-a", TimelineOffsetUS: 0, TimelineDurationUS: 800_000},
					{AssetID: "clip-b", TimelineOffsetUS: 800_000, TimelineDurationUS: 1_200_000},
					{AssetID: "clip-a-freeze", TimelineOffsetUS: 2_000_000, TimelineDurationUS: 500_000, Freeze: true},
				}},
			{ID: "scene-b", Index: 1, TimelineStartUS: 2_000_000, DurationUS: 3_000_000,
				Video: capabilityaudio.VideoSegment{AssetID: "clip-c", TimelineOffsetUS: 0, TimelineDurationUS: 3_000_000}},
		},
	}

	got := randomClipStartSFXIntents(timeline)
	if len(got) != 3 {
		t.Fatalf("clip-start cues = %d, want 3 real clips", len(got))
	}
	wantStarts := []int64{0, 800, 2000}
	for i, intent := range got {
		if intent.AtMS != wantStarts[i] {
			t.Errorf("cue %d at_ms = %d, want %d", i, intent.AtMS, wantStarts[i])
		}
		if intent.SceneID != "" || intent.GainDB != -6 || intent.DurationMS != 250 {
			t.Errorf("cue %d parameters = %+v; want absolute, -6 dB requested gain and 250 ms", i, intent)
		}
		if intent.AssetID != "whoosh1" && intent.AssetID != "whoosh2" && intent.AssetID != "whoosh3" {
			t.Errorf("cue %d selected unbound alias %q", i, intent.AssetID)
		}
	}
	again := randomClipStartSFXIntents(timeline)
	for i := range got {
		if got[i] != again[i] {
			t.Errorf("retry changed clip-start cue %d: %+v vs %+v", i, got[i], again[i])
		}
	}
}

func TestRandomClipStartSFXIntentsIgnoreAudioOnlyTimeline(t *testing.T) {
	timeline := capabilityaudio.CanonicalTimeline{
		Version:    capabilityaudio.TimelineVersion,
		DurationUS: 1_000_000,
		Segments:   []capabilityaudio.TimelineSegment{{ID: "scene", Index: 0, TimelineStartUS: 0, DurationUS: 1_000_000}},
	}
	if got := randomClipStartSFXIntents(timeline); len(got) != 0 {
		t.Fatalf("audio-only timeline produced %d clip-start cues", len(got))
	}
}
