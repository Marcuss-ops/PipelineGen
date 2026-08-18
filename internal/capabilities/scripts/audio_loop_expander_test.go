// Package scriptgeneration — audio_loop_expander_test.go: regression-guard
// for the deterministic BGM loop expansion (Go decides, Rust executes).
package scriptgeneration

import (
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func expand(t *testing.T, bgm audio.ResolvedBGM, sourceDurationUS int64) []audio.AudioLayer {
	t.Helper()
	layers, err := NewAudioLoopExpander().Expand(bgm, sourceDurationUS)
	if err != nil {
		t.Fatalf("expand %+v (source %dus): %v", bgm, sourceDurationUS, err)
	}
	return layers
}

// TestAudioLoopExpander_PlanExample145s40s pins the plan's canonical
// example: video 145s, music 40s, loop → 4 deterministic events, the last
// truncated exactly on the window end.
func TestAudioLoopExpander_PlanExample145s40s(t *testing.T) {
	layers := expand(t, audio.ResolvedBGM{
		AssetID:         "music_abc",
		TimelineStartUS: 0,
		DurationUS:      145_000_000, // end=video_end → CanonicalTimeline.DurationUS
		Loop:            true,
		GainDB:          -24,
	}, 40_000_000)
	want := []audio.AudioLayer{
		{AssetID: "music_abc", TimelineStartUS: 0, DurationUS: 40_000_000, GainDB: -24},
		{AssetID: "music_abc", TimelineStartUS: 40_000_000, DurationUS: 40_000_000, GainDB: -24},
		{AssetID: "music_abc", TimelineStartUS: 80_000_000, DurationUS: 40_000_000, GainDB: -24},
		{AssetID: "music_abc", TimelineStartUS: 120_000_000, DurationUS: 25_000_000, GainDB: -24}, // truncated
	}
	if !reflect.DeepEqual(layers, want) {
		t.Fatalf("layers = %+v\nwant     %+v", layers, want)
	}
}

// TestAudioLoopExpander_FundamentalTest75s20s pins the plan's fundamental
// test case: video 75s, BGM 20s, loop → 4 events (0-20, 20-40, 40-60,
// 60-75) covering the timeline exactly.
func TestAudioLoopExpander_FundamentalTest75s20s(t *testing.T) {
	layers := expand(t, audio.ResolvedBGM{
		AssetID:         "bgm_20s",
		TimelineStartUS: 0,
		DurationUS:      75_000_000,
		Loop:            true,
	}, 20_000_000)
	want := []audio.AudioLayer{
		{AssetID: "bgm_20s", TimelineStartUS: 0, DurationUS: 20_000_000},
		{AssetID: "bgm_20s", TimelineStartUS: 20_000_000, DurationUS: 20_000_000},
		{AssetID: "bgm_20s", TimelineStartUS: 40_000_000, DurationUS: 20_000_000},
		{AssetID: "bgm_20s", TimelineStartUS: 60_000_000, DurationUS: 15_000_000},
	}
	if !reflect.DeepEqual(layers, want) {
		t.Fatalf("layers = %+v\nwant     %+v", layers, want)
	}
}

// TestAudioLoopExpander_NonLoopSourceShorterThanWindow certifies the
// plan's non-loop semantics: the music ends, the remaining window is BGM
// silence (no synthetic filler events).
func TestAudioLoopExpander_NonLoopSourceShorterThanWindow(t *testing.T) {
	layers := expand(t, audio.ResolvedBGM{
		AssetID:         "music_20s",
		TimelineStartUS: 5_000_000,
		DurationUS:      30_000_000,
		Loop:            false,
		GainDB:          -18,
	}, 20_000_000)
	want := []audio.AudioLayer{
		{AssetID: "music_20s", TimelineStartUS: 5_000_000, DurationUS: 20_000_000, GainDB: -18},
	}
	if !reflect.DeepEqual(layers, want) {
		t.Fatalf("layers = %+v\nwant     %+v", layers, want)
	}
}

func TestAudioLoopExpander_NonLoopSourceLongerThanWindowTruncates(t *testing.T) {
	layers := expand(t, audio.ResolvedBGM{
		AssetID:         "music_60s",
		TimelineStartUS: 10_000_000,
		DurationUS:      30_000_000,
		Loop:            false,
	}, 60_000_000)
	if len(layers) != 1 || layers[0].DurationUS != 30_000_000 || layers[0].TimelineStartUS != 10_000_000 {
		t.Fatalf("layers = %+v, want one truncated event [10s,40s)", layers)
	}
}

func TestAudioLoopExpander_LoopSourceLongerThanWindowTruncates(t *testing.T) {
	layers := expand(t, audio.ResolvedBGM{
		AssetID:         "music_60s",
		TimelineStartUS: 0,
		DurationUS:      30_000_000,
		Loop:            true,
	}, 60_000_000)
	if len(layers) != 1 || layers[0].DurationUS != 30_000_000 {
		t.Fatalf("layers = %+v, want one truncated event (loop never repeats a source longer than the window)", layers)
	}
}

func TestAudioLoopExpander_LoopSourceExactlyMatchesWindow(t *testing.T) {
	layers := expand(t, audio.ResolvedBGM{
		AssetID:         "music_20s",
		TimelineStartUS: 0,
		DurationUS:      20_000_000,
		Loop:            true,
	}, 20_000_000)
	if len(layers) != 1 || layers[0].DurationUS != 20_000_000 {
		t.Fatalf("layers = %+v, want exactly one full event", layers)
	}
}

func TestAudioLoopExpander_Deterministic(t *testing.T) {
	bgm := audio.ResolvedBGM{AssetID: "music", TimelineStartUS: 2_000_000, DurationUS: 50_000_000, Loop: true, GainDB: -10}
	first := expand(t, bgm, 7_000_000)
	for i := 0; i < 5; i++ {
		if got := expand(t, bgm, 7_000_000); !reflect.DeepEqual(got, first) {
			t.Fatalf("expansion is not deterministic:\n%+v\n%+v", got, first)
		}
	}
}

func TestAudioLoopExpander_FailClosed(t *testing.T) {
	tests := []struct {
		name             string
		bgm              audio.ResolvedBGM
		sourceDurationUS int64
	}{
		{name: "blank_asset_id", bgm: audio.ResolvedBGM{AssetID: " ", DurationUS: 10_000_000}, sourceDurationUS: 10_000_000},
		{name: "no_window", bgm: audio.ResolvedBGM{AssetID: "music", DurationUS: 0}, sourceDurationUS: 10_000_000},
		{name: "negative_window", bgm: audio.ResolvedBGM{AssetID: "music", DurationUS: -1}, sourceDurationUS: 10_000_000},
		{name: "negative_start", bgm: audio.ResolvedBGM{AssetID: "music", TimelineStartUS: -1, DurationUS: 10_000_000}, sourceDurationUS: 10_000_000},
		{name: "unknown_source_loop", bgm: audio.ResolvedBGM{AssetID: "music", DurationUS: 10_000_000, Loop: true}, sourceDurationUS: 0},
		{name: "unknown_source_no_loop", bgm: audio.ResolvedBGM{AssetID: "music", DurationUS: 10_000_000}, sourceDurationUS: -5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewAudioLoopExpander().Expand(tt.bgm, tt.sourceDurationUS); err == nil {
				t.Fatalf("expected error for %s (bgm=%+v source=%d)", tt.name, tt.bgm, tt.sourceDurationUS)
			}
		})
	}
}
