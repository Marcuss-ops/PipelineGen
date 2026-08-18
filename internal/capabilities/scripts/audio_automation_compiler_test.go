// Package scriptgeneration — audio_automation_compiler_test.go:
// regression-guard for BGM fade-in/fade-out → canonical AudioAutomation.
package scriptgeneration

import (
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// TestCompileBGMFades_PlanExample pins the plan's fade contract: a 1200ms
// fade-in and a 1800ms fade-out on a full-video window compile into one
// automation entry on the "bgm" track holding the layer gain between the
// ramps — no brutal cut at the video end.
func TestCompileBGMFades_PlanExample(t *testing.T) {
	out, err := NewAudioAutomationCompiler().CompileBGMFades([]audio.ResolvedBGM{{
		AssetID:         "bgm_documentary_01",
		TimelineStartUS: 0,
		DurationUS:      60_000_000, // end=video_end → CanonicalTimeline.DurationUS
		GainDB:          -24,
		FadeInUS:        1_200_000,
		FadeOutUS:       1_800_000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []audio.AudioAutomation{{
		TargetTrackID: "bgm",
		StartUS:       0,
		EndUS:         60_000_000,
		GainDB:        -24,
		AttackUS:      1_200_000,
		ReleaseUS:     1_800_000,
	}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("automation = %+v\nwant         %+v", out, want)
	}
}

func TestCompileBGMFades_NoFadesProducesNoAutomation(t *testing.T) {
	out, err := NewAudioAutomationCompiler().CompileBGMFades([]audio.ResolvedBGM{{
		AssetID:         "music",
		TimelineStartUS: 0,
		DurationUS:      60_000_000,
		Loop:            true,
		GainDB:          -24,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("automation = %+v, want none (no fades → no automation)", out)
	}
}

func TestCompileBGMFades_OnlyFadeInOrOut(t *testing.T) {
	c := NewAudioAutomationCompiler()
	inOnly, err := c.CompileBGMFades([]audio.ResolvedBGM{{
		AssetID: "music", TimelineStartUS: 5_000_000, DurationUS: 10_000_000, GainDB: -18, FadeInUS: 500_000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inOnly) != 1 || inOnly[0].AttackUS != 500_000 || inOnly[0].ReleaseUS != 0 || inOnly[0].StartUS != 5_000_000 || inOnly[0].EndUS != 15_000_000 {
		t.Fatalf("fade-in only automation = %+v", inOnly)
	}
	outOnly, err := c.CompileBGMFades([]audio.ResolvedBGM{{
		AssetID: "music", TimelineStartUS: 0, DurationUS: 10_000_000, GainDB: -18, FadeOutUS: 750_000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(outOnly) != 1 || outOnly[0].AttackUS != 0 || outOnly[0].ReleaseUS != 750_000 {
		t.Fatalf("fade-out only automation = %+v", outOnly)
	}
}

// TestCompileBGMFades_ClampedToWindow certifies that a fade longer than the
// window covers the whole window instead of overflowing it.
func TestCompileBGMFades_ClampedToWindow(t *testing.T) {
	out, err := NewAudioAutomationCompiler().CompileBGMFades([]audio.ResolvedBGM{{
		AssetID: "music", TimelineStartUS: 0, DurationUS: 1_000_000, GainDB: -24, FadeInUS: 1_200_000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].AttackUS != 1_000_000 {
		t.Fatalf("clamped automation = %+v, want attack == window duration", out)
	}
}

func TestCompileBGMFades_MultiLayerOrdered(t *testing.T) {
	out, err := NewAudioAutomationCompiler().CompileBGMFades([]audio.ResolvedBGM{
		{AssetID: "intro", TimelineStartUS: 0, DurationUS: 10_000_000, GainDB: -20, FadeOutUS: 1_000_000},
		{AssetID: "no_fade", TimelineStartUS: 10_000_000, DurationUS: 10_000_000, GainDB: -20},
		{AssetID: "dark", TimelineStartUS: 20_000_000, DurationUS: 10_000_000, GainDB: -25, FadeInUS: 1_500_000, FadeOutUS: 2_000_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("automation = %+v, want exactly two entries (layers without fades produce none)", out)
	}
	if out[0].StartUS != 0 || out[0].ReleaseUS != 1_000_000 || out[0].AttackUS != 0 {
		t.Fatalf("intro automation = %+v", out[0])
	}
	if out[1].StartUS != 20_000_000 || out[1].EndUS != 30_000_000 || out[1].AttackUS != 1_500_000 || out[1].ReleaseUS != 2_000_000 || out[1].GainDB != -25 {
		t.Fatalf("dark automation = %+v", out[1])
	}
}

func TestCompileBGMFades_Deterministic(t *testing.T) {
	layers := []audio.ResolvedBGM{
		{AssetID: "music", TimelineStartUS: 2_000_000, DurationUS: 50_000_000, GainDB: -10, FadeInUS: 800_000, FadeOutUS: 900_000},
	}
	c := NewAudioAutomationCompiler()
	first, err := c.CompileBGMFades(layers)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		got, err := c.CompileBGMFades(layers)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("fade compilation is not deterministic:\n%+v\n%+v", got, first)
		}
	}
}

func TestCompileBGMFades_FailClosed(t *testing.T) {
	tests := []struct {
		name  string
		layer audio.ResolvedBGM
	}{
		{name: "blank_asset_id", layer: audio.ResolvedBGM{AssetID: " ", DurationUS: 10_000_000, FadeInUS: 100_000}},
		{name: "no_window", layer: audio.ResolvedBGM{AssetID: "music", DurationUS: 0, FadeInUS: 100_000}},
		{name: "negative_start", layer: audio.ResolvedBGM{AssetID: "music", TimelineStartUS: -1, DurationUS: 10_000_000, FadeInUS: 100_000}},
		{name: "negative_fade_in", layer: audio.ResolvedBGM{AssetID: "music", DurationUS: 10_000_000, FadeInUS: -100_000}},
		{name: "negative_fade_out", layer: audio.ResolvedBGM{AssetID: "music", DurationUS: 10_000_000, FadeOutUS: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewAudioAutomationCompiler().CompileBGMFades([]audio.ResolvedBGM{tt.layer}); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}
