// Package scriptgeneration — background_music_resolver_test.go:
// regression-guard for BGM intent → ResolvedBGM window resolution.
package scriptgeneration

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func bgmTestTimeline() audio.CanonicalTimeline {
	return audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 75_000_000,
		Segments: []audio.TimelineSegment{
			{ID: "scene-1", Index: 0, TimelineStartUS: 0, DurationUS: 25_000_000},
			{ID: "scene-2", Index: 1, TimelineStartUS: 25_000_000, DurationUS: 25_000_000},
			{ID: "scene-3", Index: 2, TimelineStartUS: 50_000_000, DurationUS: 25_000_000},
		},
	}
}

func resolveBGM(t *testing.T, timeline audio.CanonicalTimeline, intents ...scriptpkg.BackgroundMusicIntent) []audio.ResolvedBGM {
	t.Helper()
	out, err := NewBackgroundMusicResolver().Resolve(timeline, intents)
	if err != nil {
		t.Fatalf("resolve background music: %v", err)
	}
	return out
}

func TestBackgroundMusicResolver_FullVideoWindowDefaultEnd(t *testing.T) {
	out := resolveBGM(t, bgmTestTimeline(),
		scriptpkg.BackgroundMusicIntent{AssetID: "music_123", Loop: true, GainDB: -24},
	)
	if len(out) != 1 {
		t.Fatalf("out = %+v, want exactly one window", out)
	}
	b := out[0]
	if b.AssetID != "music_123" || b.TimelineStartUS != 0 || b.DurationUS != 75_000_000 || !b.Loop || b.GainDB != -24 {
		t.Fatalf("bgm = %+v (omitted end must default to video_end)", b)
	}
}

func TestBackgroundMusicResolver_AbsoluteWindows(t *testing.T) {
	out := resolveBGM(t, bgmTestTimeline(),
		scriptpkg.BackgroundMusicIntent{AssetID: "intro", StartMS: 0, End: &scriptpkg.AudioTimelineEnd{Ms: 60_000}, Loop: true},
		scriptpkg.BackgroundMusicIntent{AssetID: "dark", StartMS: 60_000, End: &scriptpkg.AudioTimelineEnd{VideoEnd: true}, Loop: true},
	)
	if len(out) != 2 {
		t.Fatalf("out = %+v, want two segmented windows", out)
	}
	if out[0].TimelineStartUS != 0 || out[0].DurationUS != 60_000_000 {
		t.Fatalf("intro window = [%d,%d), want [0,60000000)", out[0].TimelineStartUS, out[0].DurationUS)
	}
	if out[1].TimelineStartUS != 60_000_000 || out[1].DurationUS != 15_000_000 {
		t.Fatalf("dark window = [%d,%d), want [60000000,75000000)", out[1].TimelineStartUS, out[1].DurationUS)
	}
}

// TestBackgroundMusicResolver_NormalizesFadesAndDuckingToMicroseconds
// certifies the ms → µs boundary for every intent field the downstream
// compilers consume.
func TestBackgroundMusicResolver_NormalizesFadesAndDuckingToMicroseconds(t *testing.T) {
	out := resolveBGM(t, bgmTestTimeline(),
		scriptpkg.BackgroundMusicIntent{
			AssetID:            "music",
			FadeInMS:           1000,
			FadeOutMS:          1800,
			DuckUnderVoiceover: true,
			DuckGainDB:         -30,
			DuckAttackMS:       120,
			DuckReleaseMS:      350,
		},
	)
	if len(out) != 1 {
		t.Fatalf("out = %+v", out)
	}
	b := out[0]
	if b.FadeInUS != 1_000_000 || b.FadeOutUS != 1_800_000 {
		t.Fatalf("fades = [%d,%d]us, want [1000000,1800000]", b.FadeInUS, b.FadeOutUS)
	}
	if !b.DuckUnderVoiceover || b.DuckGainDB != -30 || b.DuckAttackUS != 120_000 || b.DuckReleaseUS != 350_000 {
		t.Fatalf("ducking = %+v", b)
	}
}

func TestBackgroundMusicResolver_EmptyIntentsYieldEmptyResult(t *testing.T) {
	out := resolveBGM(t, bgmTestTimeline())
	if len(out) != 0 {
		t.Fatalf("out = %+v, want none", out)
	}
}

func TestBackgroundMusicResolver_FailClosed(t *testing.T) {
	timeline := bgmTestTimeline()
	tests := []struct {
		name   string
		intent scriptpkg.BackgroundMusicIntent
	}{
		{name: "blank_asset_id", intent: scriptpkg.BackgroundMusicIntent{AssetID: "  "}},
		{name: "negative_start", intent: scriptpkg.BackgroundMusicIntent{AssetID: "music", StartMS: -100}},
		{name: "start_at_timeline_end", intent: scriptpkg.BackgroundMusicIntent{AssetID: "music", StartMS: 75_000}},
		{name: "end_beyond_timeline", intent: scriptpkg.BackgroundMusicIntent{AssetID: "music", End: &scriptpkg.AudioTimelineEnd{Ms: 80_000}}},
		{name: "end_before_start", intent: scriptpkg.BackgroundMusicIntent{AssetID: "music", StartMS: 10_000, End: &scriptpkg.AudioTimelineEnd{Ms: 5_000}}},
		{name: "negative_fade_in", intent: scriptpkg.BackgroundMusicIntent{AssetID: "music", FadeInMS: -1}},
		{name: "negative_fade_out", intent: scriptpkg.BackgroundMusicIntent{AssetID: "music", FadeOutMS: -1}},
		{name: "negative_duck_attack", intent: scriptpkg.BackgroundMusicIntent{AssetID: "music", DuckAttackMS: -1}},
		{name: "negative_duck_release", intent: scriptpkg.BackgroundMusicIntent{AssetID: "music", DuckReleaseMS: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewBackgroundMusicResolver().Resolve(timeline, []scriptpkg.BackgroundMusicIntent{tt.intent}); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}
