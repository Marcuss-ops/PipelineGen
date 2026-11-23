package audio

import (
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestRandomClipStartSFXIntentsUsesPoolOncePerClipStart pins the two
// behaviours the clip-start lane owes the caller: the cue is selected from the
// indexed pool when one is supplied, and exactly one cue is emitted at each
// real video clip start (never repeated inside the clip).
func TestRandomClipStartSFXIntentsUsesPoolOncePerClipStart(t *testing.T) {
	timeline := capabilityaudio.CanonicalTimeline{
		Version:    capabilityaudio.TimelineVersion,
		DurationUS: 12_000_000,
		Segments: []capabilityaudio.TimelineSegment{
			{
				ID: "scene_1", Index: 0, TimelineStartUS: 0, DurationUS: 6_000_000,
				VideoSegments: []capabilityaudio.VideoSegment{
					{AssetID: "clip-a", TimelineOffsetUS: 0, TimelineDurationUS: 6_000_000},
				},
			},
			{
				ID: "scene_2", Index: 1, TimelineStartUS: 6_000_000, DurationUS: 6_000_000,
				VideoSegments: []capabilityaudio.VideoSegment{
					{AssetID: "clip-b", TimelineOffsetUS: 0, TimelineDurationUS: 6_000_000},
				},
			},
		},
	}
	pool := []string{"sfx_impact_boom", "sfx_whoosh_fast", "sfx_riser_tension"}
	intents := RandomClipStartSFXIntentsWithPool(timeline, pool)
	if len(intents) != 2 {
		t.Fatalf("intents = %d, want one cue per clip start", len(intents))
	}
	allowed := map[string]struct{}{}
	for _, id := range pool {
		allowed[id] = struct{}{}
	}
	wantStarts := []int64{0, 6000}
	seen := map[int64]int{}
	for i, intent := range intents {
		if intent.AtMS != wantStarts[i] {
			t.Fatalf("intent %d at %dms, want %dms", i, intent.AtMS, wantStarts[i])
		}
		if _, ok := allowed[intent.AssetID]; !ok {
			t.Fatalf("intent %d asset %q is not in the indexed pool", i, intent.AssetID)
		}
		if intent.DurationMS != 250 || intent.GainDB != -6 {
			t.Fatalf("intent %d = %+v, want the 250ms/-6dB clip-start cue", i, intent)
		}
		seen[intent.AtMS]++
	}
	for at, count := range seen {
		if count != 1 {
			t.Fatalf("%d cues at %dms, want exactly one", count, at)
		}
	}

	// Without an indexed pool the built-in whoosh vocabulary is the fallback.
	for _, intent := range RandomClipStartSFXIntents(timeline) {
		ok := false
		for _, alias := range scriptpkg.BuiltInWhooshAliases() {
			if intent.AssetID == alias {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("fallback cue asset %q is not a built-in whoosh alias", intent.AssetID)
		}
	}
}
