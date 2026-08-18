// Package scriptgeneration — audio_automation_ducking_test.go:
// regression-guard for BGM ducking under voiceover → canonical
// AudioAutomation.
package scriptgeneration

import (
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// duckTimeline is a 10s one-scene timeline whose voiceover speaks for the
// first 8 seconds (certified source duration) inside a 10s scene window.
func duckTimeline() audio.CanonicalTimeline {
	return audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 10_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene-1", Index: 0, TimelineStartUS: 0, DurationUS: 10_000_000,
			AudioIntents: []audio.AudioIntent{{
				Mode: audio.AudioVoiceover, VoiceoverAssetID: "vo-1",
				SourceDurationUS: 8_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 10_000_000,
			}},
		}},
	}
}

// duckTwoSceneTimeline is a 20s two-scene timeline with speech windows
// [0,8s) and [10s,17s).
func duckTwoSceneTimeline() audio.CanonicalTimeline {
	return audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 20_000_000,
		Segments: []audio.TimelineSegment{
			{ID: "scene-1", Index: 0, TimelineStartUS: 0, DurationUS: 10_000_000,
				AudioIntents: []audio.AudioIntent{{Mode: audio.AudioVoiceover, VoiceoverAssetID: "vo-1", SourceDurationUS: 8_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 10_000_000}}},
			{ID: "scene-2", Index: 1, TimelineStartUS: 10_000_000, DurationUS: 10_000_000,
				AudioIntents: []audio.AudioIntent{{Mode: audio.AudioVoiceover, VoiceoverAssetID: "vo-2", SourceDurationUS: 7_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 10_000_000}}},
		},
	}
}

func compileDucking(t *testing.T, timeline audio.CanonicalTimeline, bgm []audio.ResolvedBGM) []audio.AudioAutomation {
	t.Helper()
	out, err := NewAudioAutomationCompiler().CompileBGMDucking(timeline, bgm)
	if err != nil {
		t.Fatalf("compile bgm ducking: %v", err)
	}
	return out
}

// TestCompileBGMDucking_PlanExample pins the plan's ducking contract: BGM
// covering the whole video, voiceover speaking 0-8s → one entry lowering
// the bgm track to -30 dB (120ms attack, 350ms release) while the speech
// is present.
func TestCompileBGMDucking_PlanExample(t *testing.T) {
	out := compileDucking(t, duckTimeline(), []audio.ResolvedBGM{{
		AssetID:            "bgm_01",
		TimelineStartUS:    0,
		DurationUS:         10_000_000,
		GainDB:             -22,
		DuckUnderVoiceover: true,
		DuckGainDB:         -30,
		DuckAttackUS:       120_000,
		DuckReleaseUS:      350_000,
	}})
	want := []audio.AudioAutomation{{
		TargetTrackID:  "bgm",
		TriggerTrackID: "voiceover",
		StartUS:        0,
		EndUS:          8_000_000, // speech ends at min(window 10s, certified 8s)
		GainDB:         -30,
		AttackUS:       120_000,
		ReleaseUS:      350_000,
	}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("automation = %+v\nwant         %+v", out, want)
	}
}

// TestCompileBGMDucking_PlanDefaults certifies that duck_under_voiceover
// without explicit values uses the plan defaults (-30 dB / 120 ms / 350 ms).
func TestCompileBGMDucking_PlanDefaults(t *testing.T) {
	out := compileDucking(t, duckTimeline(), []audio.ResolvedBGM{{
		AssetID:            "bgm_01",
		TimelineStartUS:    0,
		DurationUS:         10_000_000,
		DuckUnderVoiceover: true,
	}})
	if len(out) != 1 {
		t.Fatalf("automation = %+v, want exactly one entry", out)
	}
	a := out[0]
	if a.GainDB != DefaultBGMDuckGainDB || a.AttackUS != DefaultBGMDuckAttackUS || a.ReleaseUS != DefaultBGMDuckReleaseUS {
		t.Fatalf("defaults not applied: %+v (want gain=%.1f attack=%d release=%d)", a, DefaultBGMDuckGainDB, DefaultBGMDuckAttackUS, DefaultBGMDuckReleaseUS)
	}
}

// TestCompileBGMDucking_OverlapClampedToLayerWindow certifies that the
// duck window is the intersection of the speech window and the layer
// window: a BGM starting mid-speech ducks only from its own start, and a
// speech ending after the layer window ends is cut at the layer end.
func TestCompileBGMDucking_OverlapClampedToLayerWindow(t *testing.T) {
	out := compileDucking(t, duckTimeline(), []audio.ResolvedBGM{
		{AssetID: "late_start", TimelineStartUS: 5_000_000, DurationUS: 5_000_000, DuckUnderVoiceover: true, DuckGainDB: -30},
		{AssetID: "early_end", TimelineStartUS: 0, DurationUS: 3_000_000, DuckUnderVoiceover: true, DuckGainDB: -30},
	})
	if len(out) != 2 {
		t.Fatalf("automation = %+v, want exactly two entries", out)
	}
	if out[0].StartUS != 5_000_000 || out[0].EndUS != 8_000_000 {
		t.Fatalf("late_start duck window = [%d,%d), want [5000000,8000000)", out[0].StartUS, out[0].EndUS)
	}
	if out[1].StartUS != 0 || out[1].EndUS != 3_000_000 {
		t.Fatalf("early_end duck window = [%d,%d), want [0,3000000)", out[1].StartUS, out[1].EndUS)
	}
}

func TestCompileBGMDucking_NoOverlapProducesNoEntry(t *testing.T) {
	out := compileDucking(t, duckTimeline(), []audio.ResolvedBGM{{
		AssetID:            "after_speech",
		TimelineStartUS:    8_000_000, // speech ends exactly at 8s
		DurationUS:         2_000_000,
		DuckUnderVoiceover: true,
		DuckGainDB:         -30,
	}})
	if len(out) != 0 {
		t.Fatalf("automation = %+v, want none (no speech overlap)", out)
	}
}

func TestCompileBGMDucking_DisabledLayersProduceNoEntries(t *testing.T) {
	out := compileDucking(t, duckTimeline(), []audio.ResolvedBGM{{
		AssetID:         "music",
		TimelineStartUS: 0,
		DurationUS:      10_000_000,
		// DuckUnderVoiceover intentionally false
	}})
	if len(out) != 0 {
		t.Fatalf("automation = %+v, want none (ducking disabled)", out)
	}
}

// TestCompileBGMDucking_MultiSceneMultiLayer certifies one entry per
// speech×layer overlap, in deterministic order (layer order, then speech
// order).
func TestCompileBGMDucking_MultiSceneMultiLayer(t *testing.T) {
	out := compileDucking(t, duckTwoSceneTimeline(), []audio.ResolvedBGM{{
		AssetID:            "bgm_full",
		TimelineStartUS:    0,
		DurationUS:         20_000_000,
		DuckUnderVoiceover: true,
		DuckGainDB:         -30,
	}})
	if len(out) != 2 {
		t.Fatalf("automation = %+v, want exactly two entries (one per speech window)", out)
	}
	if out[0].StartUS != 0 || out[0].EndUS != 8_000_000 {
		t.Fatalf("first duck window = [%d,%d), want [0,8000000)", out[0].StartUS, out[0].EndUS)
	}
	if out[1].StartUS != 10_000_000 || out[1].EndUS != 17_000_000 {
		t.Fatalf("second duck window = [%d,%d), want [10000000,17000000)", out[1].StartUS, out[1].EndUS)
	}
}

// TestCompileBGMDucking_SpeechLongerThanWindowUsesWindow certifies the
// same clamping as the clip ducking: the duck zone never exceeds the
// scene window even when the certified speech is longer.
func TestCompileBGMDucking_SpeechLongerThanWindowUsesWindow(t *testing.T) {
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 10_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene-1", Index: 0, TimelineStartUS: 0, DurationUS: 10_000_000,
			AudioIntents: []audio.AudioIntent{{Mode: audio.AudioVoiceover, VoiceoverAssetID: "vo-1", SourceDurationUS: 12_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 10_000_000}},
		}},
	}
	out := compileDucking(t, timeline, []audio.ResolvedBGM{{
		AssetID:            "bgm",
		TimelineStartUS:    0,
		DurationUS:         10_000_000,
		DuckUnderVoiceover: true,
		DuckGainDB:         -30,
	}})
	if len(out) != 1 || out[0].EndUS != 10_000_000 {
		t.Fatalf("duck window = %+v, want [0,10000000) (speech clamped to scene window)", out)
	}
}

func TestCompileBGMDucking_Deterministic(t *testing.T) {
	bgm := []audio.ResolvedBGM{{
		AssetID:            "bgm",
		TimelineStartUS:    0,
		DurationUS:         20_000_000,
		DuckUnderVoiceover: true,
		DuckGainDB:         -30,
	}}
	c := NewAudioAutomationCompiler()
	timeline := duckTwoSceneTimeline()
	first, err := c.CompileBGMDucking(timeline, bgm)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		got, err := c.CompileBGMDucking(timeline, bgm)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("ducking compilation is not deterministic:\n%+v\n%+v", got, first)
		}
	}
}

func TestCompileBGMDucking_FailClosed(t *testing.T) {
	tests := []struct {
		name     string
		timeline audio.CanonicalTimeline
		layer    audio.ResolvedBGM
	}{
		{name: "negative_duck_attack", timeline: duckTimeline(), layer: audio.ResolvedBGM{AssetID: "bgm", DurationUS: 10_000_000, DuckUnderVoiceover: true, DuckAttackUS: -1}},
		{name: "negative_duck_release", timeline: duckTimeline(), layer: audio.ResolvedBGM{AssetID: "bgm", DurationUS: 10_000_000, DuckUnderVoiceover: true, DuckReleaseUS: -1}},
		{name: "blank_asset_id", timeline: duckTimeline(), layer: audio.ResolvedBGM{AssetID: " ", DurationUS: 10_000_000, DuckUnderVoiceover: true}},
		{name: "no_window", timeline: duckTimeline(), layer: audio.ResolvedBGM{AssetID: "bgm", DurationUS: 0, DuckUnderVoiceover: true}},
		{name: "invalid_timeline", layer: audio.ResolvedBGM{AssetID: "bgm", DurationUS: 10_000_000, DuckUnderVoiceover: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewAudioAutomationCompiler().CompileBGMDucking(tt.timeline, []audio.ResolvedBGM{tt.layer}); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}
