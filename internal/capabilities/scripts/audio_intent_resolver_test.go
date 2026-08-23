// Package scriptgeneration — audio_intent_resolver_test.go: regression-guard
// for the SFX intent → absolute timestamp resolution.
package scriptgeneration

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// sfxTestTimeline is a 30s canonical timeline with three 10s scenes:
// scene_1 [0,10s), scene_2 [10s,20s), scene_3 [20s,30s).
func sfxTestTimeline() audio.CanonicalTimeline {
	return audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 30_000_000,
		Segments: []audio.TimelineSegment{
			{ID: "scene_1", Index: 0, TimelineStartUS: 0, DurationUS: 10_000_000},
			{ID: "scene_2", Index: 1, TimelineStartUS: 10_000_000, DurationUS: 10_000_000},
			{ID: "scene_3", Index: 2, TimelineStartUS: 20_000_000, DurationUS: 10_000_000},
		},
	}
}

func resolveSFX(t *testing.T, timeline audio.CanonicalTimeline, intents ...scriptpkg.SoundEffectIntent) []audio.ResolvedSFX {
	t.Helper()
	out, err := NewAudioIntentResolver().ResolveSoundEffects(timeline, intents)
	if err != nil {
		t.Fatalf("resolve sound effects: %v", err)
	}
	return out
}

func TestAudioIntentResolver_AbsolutePlacement(t *testing.T) {
	out := resolveSFX(t, sfxTestTimeline(),
		scriptpkg.SoundEffectIntent{AssetID: "whoosh", AtMS: 5000},
		scriptpkg.SoundEffectIntent{AssetID: "impact", AtMS: 0}, // video start is a valid placement
	)
	if len(out) != 2 {
		t.Fatalf("out = %+v, want two placements", out)
	}
	if out[0].TimelineStartUS != 5_000_000 {
		t.Fatalf("whoosh start = %dus, want 5000000", out[0].TimelineStartUS)
	}
	if out[1].TimelineStartUS != 0 {
		t.Fatalf("impact start = %dus, want 0", out[1].TimelineStartUS)
	}
}

// TestAudioIntentResolver_SceneRelativeAnchors certifies the three anchors
// against a 10s scene: start + 250ms → 10.25s, middle → 15s, end - 300ms →
// 29.7s (scene_3 end), plus the default anchor (empty → start).
func TestAudioIntentResolver_SceneRelativeAnchors(t *testing.T) {
	intents := []scriptpkg.SoundEffectIntent{
		{AssetID: "whoosh", SceneID: "scene_2", Anchor: scriptpkg.SFXAnchorStart, OffsetMS: 250},
		{AssetID: "rise", SceneID: "scene_2", Anchor: scriptpkg.SFXAnchorMiddle},
		{AssetID: "impact", SceneID: "scene_3", Anchor: scriptpkg.SFXAnchorEnd, OffsetMS: -300},
		{AssetID: "click", SceneID: "scene_2", OffsetMS: 500}, // empty anchor defaults to start
	}
	out := resolveSFX(t, sfxTestTimeline(), intents...)
	wantStarts := []int64{10_250_000, 15_000_000, 29_700_000, 10_500_000}
	for i, want := range wantStarts {
		if out[i].TimelineStartUS != want {
			t.Fatalf("%s start = %dus, want %dus", intents[i].AssetID, out[i].TimelineStartUS, want)
		}
	}
}

// TestAudioIntentResolver_OffsetMayCrossSceneBoundaries certifies that a
// negative offset on "start" may land in the previous scene and a positive
// offset on "end" may bleed into the next one — the result is validated
// against the whole timeline, not the scene.
func TestAudioIntentResolver_OffsetMayCrossSceneBoundaries(t *testing.T) {
	out := resolveSFX(t, sfxTestTimeline(),
		scriptpkg.SoundEffectIntent{AssetID: "whoosh", SceneID: "scene_2", Anchor: scriptpkg.SFXAnchorStart, OffsetMS: -250}, // 9.75s (scene_1 tail)
		scriptpkg.SoundEffectIntent{AssetID: "boom", SceneID: "scene_2", Anchor: scriptpkg.SFXAnchorEnd, OffsetMS: 250},      // 20.25s (scene_3 head)
	)
	if out[0].TimelineStartUS != 9_750_000 {
		t.Fatalf("whoosh start = %dus, want 9750000", out[0].TimelineStartUS)
	}
	if out[1].TimelineStartUS != 20_250_000 {
		t.Fatalf("boom start = %dus, want 20250000", out[1].TimelineStartUS)
	}
}

func TestAudioIntentResolver_TrimsNormalizedToMicroseconds(t *testing.T) {
	out := resolveSFX(t, sfxTestTimeline(),
		scriptpkg.SoundEffectIntent{AssetID: "hit", AtMS: 12500, SourceInMS: 250, DurationMS: 900, GainDB: -3},
	)
	if out[0].SourceInUS != 250_000 {
		t.Fatalf("source_in = %dus, want 250000", out[0].SourceInUS)
	}
	if out[0].DurationUS != 900_000 {
		t.Fatalf("duration = %dus, want 900000", out[0].DurationUS)
	}
	if out[0].GainDB != -3 {
		t.Fatalf("gain = %v, want -3", out[0].GainDB)
	}
}

func TestAudioIntentResolver_EmptyIntentsYieldEmptyResult(t *testing.T) {
	out := resolveSFX(t, sfxTestTimeline())
	if len(out) != 0 {
		t.Fatalf("out = %+v, want none", out)
	}
}

func TestAudioIntentResolver_FailClosed(t *testing.T) {
	timeline := sfxTestTimeline()
	tests := []struct {
		name    string
		intents []scriptpkg.SoundEffectIntent
	}{
		{name: "unknown_scene", intents: []scriptpkg.SoundEffectIntent{{AssetID: "whoosh", SceneID: "scene_99"}}},
		{name: "invalid_anchor", intents: []scriptpkg.SoundEffectIntent{{AssetID: "whoosh", SceneID: "scene_2", Anchor: "around"}}},
		{name: "dual_placement", intents: []scriptpkg.SoundEffectIntent{{AssetID: "whoosh", AtMS: 1000, SceneID: "scene_2"}}},
		{name: "anchor_without_scene", intents: []scriptpkg.SoundEffectIntent{{AssetID: "whoosh", AtMS: 1000, Anchor: scriptpkg.SFXAnchorEnd}}},
		{name: "offset_without_scene", intents: []scriptpkg.SoundEffectIntent{{AssetID: "whoosh", AtMS: 1000, OffsetMS: 200}}},
		{name: "negative_at_ms", intents: []scriptpkg.SoundEffectIntent{{AssetID: "whoosh", AtMS: -100}}},
		{name: "absolute_beyond_timeline", intents: []scriptpkg.SoundEffectIntent{{AssetID: "whoosh", AtMS: 30000}}},
		{name: "end_anchor_beyond_timeline", intents: []scriptpkg.SoundEffectIntent{{AssetID: "whoosh", SceneID: "scene_3", Anchor: scriptpkg.SFXAnchorEnd, OffsetMS: 100}}},
		{name: "start_anchor_before_timeline", intents: []scriptpkg.SoundEffectIntent{{AssetID: "whoosh", SceneID: "scene_1", Anchor: scriptpkg.SFXAnchorStart, OffsetMS: -200}}},
		{name: "explicit_duration_exceeds_timeline", intents: []scriptpkg.SoundEffectIntent{{AssetID: "whoosh", AtMS: 25000, DurationMS: 6000}}},
		{name: "negative_source_in", intents: []scriptpkg.SoundEffectIntent{{AssetID: "whoosh", AtMS: 1000, SourceInMS: -50}}},
		{name: "negative_duration", intents: []scriptpkg.SoundEffectIntent{{AssetID: "whoosh", AtMS: 1000, DurationMS: -50}}},
		{name: "missing_asset_id", intents: []scriptpkg.SoundEffectIntent{{AtMS: 1000}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewAudioIntentResolver().ResolveSoundEffects(timeline, tt.intents); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}

// TestAudioIntentResolver_DuplicateSceneIDFailsClosed certifies that a
// canonical timeline with duplicate segment ids is rejected before any
// placement could silently resolve to the wrong scene.
func TestAudioIntentResolver_DuplicateSceneIDFailsClosed(t *testing.T) {
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 20_000_000,
		Segments: []audio.TimelineSegment{
			{ID: "scene_1", Index: 0, TimelineStartUS: 0, DurationUS: 10_000_000},
			{ID: "scene_1", Index: 1, TimelineStartUS: 10_000_000, DurationUS: 10_000_000},
		},
	}
	if _, err := NewAudioIntentResolver().ResolveSoundEffects(timeline,
		[]scriptpkg.SoundEffectIntent{{AssetID: "whoosh", SceneID: "scene_1"}},
	); err == nil {
		t.Fatal("expected error for duplicate scene ids")
	}
}
