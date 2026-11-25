package audio

import (
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func explicitClipSFXTimeline() capabilityaudio.CanonicalTimeline {
	return capabilityaudio.CanonicalTimeline{
		Version:    capabilityaudio.TimelineVersion,
		DurationUS: 6_000_000,
		Segments: []capabilityaudio.TimelineSegment{
			{
				ID: "intro", Index: 0, TimelineStartUS: 0, DurationUS: 2_000_000, FixedMedia: true,
				Video: capabilityaudio.VideoSegment{AssetID: "intro-a", TimelineOffsetUS: 0, TimelineDurationUS: 2_000_000},
			},
			{
				ID: "body", Index: 1, TimelineStartUS: 2_000_000, DurationUS: 4_000_000,
				VideoSegments: []capabilityaudio.VideoSegment{
					{AssetID: "clip-a", TimelineOffsetUS: 0, TimelineDurationUS: 2_000_000},
					{AssetID: "clip-b", TimelineOffsetUS: 2_000_000, TimelineDurationUS: 2_000_000},
				},
			},
		},
	}
}

func TestClipStartSFXIntentsUseExplicitCueAtEachStart(t *testing.T) {
	gain := -4.0
	duration := int64(400)
	cues := []scriptpkg.ClipStartSoundEffectIntent{
		{ClipID: "intro-a", AssetID: "impact", GainDB: &gain, DurationMS: &duration},
		{ClipID: "clip-a", AssetID: "riser"},
		{ClipID: "clip-b", AssetID: "whoosh1"},
	}
	intents, err := ClipStartSFXIntents(explicitClipSFXTimeline(), cues)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 3 {
		t.Fatalf("intents = %d, want intro, first body clip and second body clip", len(intents))
	}
	wantStarts := []int64{0, 2000, 4000}
	wantAssets := []string{"impact", "riser", "whoosh1"}
	for i, intent := range intents {
		if intent.AtMS != wantStarts[i] || intent.AssetID != wantAssets[i] {
			t.Errorf("intent %d = %+v, want %s at %dms", i, intent, wantAssets[i], wantStarts[i])
		}
		if i == 0 {
			if intent.GainDB != -4 || intent.DurationMS != 400 {
				t.Errorf("explicit intro cue = %+v, want -4dB/400ms", intent)
			}
			continue
		}
		if intent.GainDB != scriptpkg.DefaultClipStartSFXGainDB || intent.DurationMS != scriptpkg.DefaultClipStartSFXDurationMS {
			t.Errorf("intent %d default = %+v, want -6dB/250ms", i, intent)
		}
	}
}

func TestClipStartSFXIntentsFailClosed(t *testing.T) {
	if _, err := ClipStartSFXIntents(explicitClipSFXTimeline(), []scriptpkg.ClipStartSoundEffectIntent{{ClipID: "missing", AssetID: "whoosh1"}}); err == nil || !strings.Contains(err.Error(), "not present") {
		t.Fatalf("missing clip error = %v", err)
	}
	if _, err := ClipStartSFXIntents(explicitClipSFXTimeline(), []scriptpkg.ClipStartSoundEffectIntent{
		{ClipID: "clip-a", AssetID: "whoosh1"},
		{ClipID: "clip-a", AssetID: "whoosh2"},
	}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate cue error = %v", err)
	}
	if _, err := ClipStartSFXIntents(explicitClipSFXTimeline(), []scriptpkg.ClipStartSoundEffectIntent{{ClipID: "clip-a"}}); err == nil {
		t.Fatal("missing asset_id must fail")
	}
}

func TestRandomClipStartSFXExcludesExplicitClips(t *testing.T) {
	pool := []string{"pool-a", "pool-b", "pool-c"}
	intents := RandomClipStartSFXIntentsWithPoolExcluding(explicitClipSFXTimeline(), pool, []string{"intro-a", "clip-a"})
	if len(intents) != 1 {
		t.Fatalf("intents = %d, want only the unpinned clip-b", len(intents))
	}
	if intents[0].AtMS != 4000 {
		t.Fatalf("random cue = %+v, want clip-b at 4000ms", intents[0])
	}
}
