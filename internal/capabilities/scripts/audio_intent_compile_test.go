// Package scriptgeneration — audio_intent_compile_test.go: regression-guard
// for the canonical intent → compiled plan pipeline
// (CompileAudioWithIntents → audio.CompileWithLayers).
package scriptgeneration

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// intentCompileTimeline is a 75s three-scene timeline (25s each) with
// voiceover speech windows [0,20s), [25s,47s) and [50s,68s).
func intentCompileTimeline() audio.CanonicalTimeline {
	return audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 75_000_000,
		Segments: []audio.TimelineSegment{
			{ID: "scene-1", Index: 0, TimelineStartUS: 0, DurationUS: 25_000_000,
				AudioIntents: []audio.AudioIntent{{Mode: audio.AudioVoiceover, VoiceoverAssetID: "vo-1", SourceDurationUS: 20_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 25_000_000}}},
			{ID: "scene-2", Index: 1, TimelineStartUS: 25_000_000, DurationUS: 25_000_000,
				AudioIntents: []audio.AudioIntent{{Mode: audio.AudioVoiceover, VoiceoverAssetID: "vo-2", SourceDurationUS: 22_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 25_000_000}}},
			{ID: "scene-3", Index: 2, TimelineStartUS: 50_000_000, DurationUS: 25_000_000,
				AudioIntents: []audio.AudioIntent{{Mode: audio.AudioVoiceover, VoiceoverAssetID: "vo-3", SourceDurationUS: 18_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 25_000_000}}},
		},
	}
}

// intentCompileSource builds the fake asset source with certified
// durations: BGM 20s, three SFX of 1s / 0.5s / 0.3s.
func intentCompileSource() *fakeAudioAssetSource {
	source := newFakeAudioAssetSource(map[string]string{
		"bgm_20s":    "/m/bgm.m4a",
		"sfx_whoosh": "/m/whoosh.m4a",
		"sfx_impact": "/m/impact.m4a",
		"sfx_boom":   "/m/boom.m4a",
	})
	source.assets["bgm_20s"] = audio.ResolvedAudioAsset{AssetID: "bgm_20s", Path: "/m/bgm.m4a", DurationUS: 20_000_000}
	// whoosh must be >= source_in(250ms) + duration(900ms) = 1.15s
	source.assets["sfx_whoosh"] = audio.ResolvedAudioAsset{AssetID: "sfx_whoosh", Path: "/m/whoosh.m4a", DurationUS: 2_000_000}
	source.assets["sfx_impact"] = audio.ResolvedAudioAsset{AssetID: "sfx_impact", Path: "/m/impact.m4a", DurationUS: 500_000}
	source.assets["sfx_boom"] = audio.ResolvedAudioAsset{AssetID: "sfx_boom", Path: "/m/boom.m4a", DurationUS: 300_000}
	return source
}

func intentBGM() []scriptpkg.BackgroundMusicIntent {
	return []scriptpkg.BackgroundMusicIntent{{
		AssetID:            "bgm_20s",
		StartMS:            0,
		End:                &scriptpkg.AudioTimelineEnd{VideoEnd: true},
		Loop:               true,
		GainDB:             -24,
		FadeInMS:           1000,
		FadeOutMS:          1800,
		DuckUnderVoiceover: true,
		DuckGainDB:         -30,
		DuckAttackMS:       120,
		DuckReleaseMS:      350,
	}}
}

func intentSFX() []scriptpkg.SoundEffectIntent {
	return []scriptpkg.SoundEffectIntent{
		{AssetID: "sfx_whoosh", AtMS: 12_000, SourceInMS: 250, DurationMS: 900, GainDB: -8},
		{AssetID: "sfx_impact", AtMS: 31_000, GainDB: -5}, // no explicit duration → sized from source
		{AssetID: "sfx_boom", AtMS: 69_000, GainDB: -3},
	}
}

// TestCompileAudioWithIntents_FundamentalPlan pins the plan's fundamental
// test: video 75s, BGM 20s loop → 4 BGM events (0-20, 20-40, 40-60,
// 60-75), 3 SFX at 12s/31s/69s, voiceover preserved, ducking + fades in
// the automation, master 75s exactly.
func TestCompileAudioWithIntents_FundamentalPlan(t *testing.T) {
	result, err := CompileAudioWithIntents(
		context.Background(),
		intentCompileTimeline(),
		audio.DefaultAudioProfile(),
		"voiceover_with_ducked_clip", // wire alias must normalize to the canonical policy
		intentBGM(),
		intentSFX(),
		intentCompileSource(),
	)
	if err != nil {
		t.Fatalf("compile audio with intents: %v", err)
	}
	plan := result.Plan

	// BGM: 4 deterministic loop events covering [0,75s) exactly.
	bgm := eventsForRole(plan, audio.TrackBGM)
	wantBGM := []audio.AudioEvent{
		{EventID: "bgm-0", Type: audio.EventBGM, AssetID: "bgm_20s", TimelineStartUS: 0, DurationUS: 20_000_000, SourceInUS: 0, SourceDurationUS: 20_000_000, GainDB: audio.BackgroundMusicGainDB},
		{EventID: "bgm-1", Type: audio.EventBGM, AssetID: "bgm_20s", TimelineStartUS: 20_000_000, DurationUS: 20_000_000, SourceInUS: 0, SourceDurationUS: 20_000_000, GainDB: audio.BackgroundMusicGainDB},
		{EventID: "bgm-2", Type: audio.EventBGM, AssetID: "bgm_20s", TimelineStartUS: 40_000_000, DurationUS: 20_000_000, SourceInUS: 0, SourceDurationUS: 20_000_000, GainDB: audio.BackgroundMusicGainDB},
		{EventID: "bgm-3", Type: audio.EventBGM, AssetID: "bgm_20s", TimelineStartUS: 60_000_000, DurationUS: 15_000_000, SourceInUS: 0, SourceDurationUS: 15_000_000, GainDB: audio.BackgroundMusicGainDB},
	}
	if len(bgm) != len(wantBGM) {
		t.Fatalf("bgm events = %+v, want %d", bgm, len(wantBGM))
	}
	for i := range wantBGM {
		if bgm[i] != wantBGM[i] {
			t.Fatalf("bgm[%d] = %+v\nwant      %+v", i, bgm[i], wantBGM[i])
		}
	}

	// SFX: whoosh with trims, impact/boom sized from their source assets.
	sfx := eventsForRole(plan, audio.TrackSFX)
	wantSFX := []audio.AudioEvent{
		{EventID: "sfx-0", Type: audio.EventSFX, AssetID: "sfx_whoosh", TimelineStartUS: 12_000_000, DurationUS: 900_000, SourceInUS: 250_000, SourceDurationUS: 900_000, GainDB: audio.SoundEffectGainDB},
		{EventID: "sfx-1", Type: audio.EventSFX, AssetID: "sfx_impact", TimelineStartUS: 31_000_000, DurationUS: 500_000, SourceInUS: 0, SourceDurationUS: 500_000, GainDB: audio.SoundEffectGainDB},
		{EventID: "sfx-2", Type: audio.EventSFX, AssetID: "sfx_boom", TimelineStartUS: 69_000_000, DurationUS: 300_000, SourceInUS: 0, SourceDurationUS: 300_000, GainDB: audio.SoundEffectGainDB},
	}
	if len(sfx) != len(wantSFX) {
		t.Fatalf("sfx events = %+v, want %d", sfx, len(wantSFX))
	}
	for i := range wantSFX {
		if sfx[i] != wantSFX[i] {
			t.Fatalf("sfx[%d] = %+v\nwant     %+v", i, sfx[i], wantSFX[i])
		}
	}

	// Voiceover preserved: 3 events on the VO track.
	if len(eventsForRole(plan, audio.TrackVoiceover)) != 3 {
		t.Fatalf("voiceover events = %d, want 3 (voiceover must be preserved)", len(eventsForRole(plan, audio.TrackVoiceover)))
	}

	// Master duration exactly the video duration; plan sealed and valid.
	if plan.DurationUS != 75_000_000 {
		t.Fatalf("plan duration = %dus, want 75000000", plan.DurationUS)
	}
	if plan.MixPolicy != audio.MixVoiceoverWithDuckedClip {
		t.Fatalf("mix policy = %q, want %q (wire alias must normalize)", plan.MixPolicy, audio.MixVoiceoverWithDuckedClip)
	}
	if plan.PlanSHA256 == "" {
		t.Fatal("plan must be sealed")
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("sealed plan is invalid: %v", err)
	}

	// Automation: 1 fade + 3 duck windows (one per speech window).
	if len(plan.Automation) != 4 {
		t.Fatalf("automation = %+v, want 1 fade + 3 duck entries", plan.Automation)
	}
	fade := plan.Automation[0]
	if fade.TargetTrackID != "bgm" || fade.AttackUS != 1_000_000 || fade.ReleaseUS != 1_800_000 || fade.GainDB != audio.BackgroundMusicGainDB {
		t.Fatalf("fade automation = %+v", fade)
	}
	wantDuckWindows := [][2]int64{{0, 20_000_000}, {25_000_000, 47_000_000}, {50_000_000, 68_000_000}}
	for i, w := range wantDuckWindows {
		d := plan.Automation[i+1]
		if d.TargetTrackID != "bgm" || d.TriggerTrackID != "voiceover" || d.StartUS != w[0] || d.EndUS != w[1] || d.GainDB != audio.BackgroundMusicGainDB || d.AttackUS != 120_000 || d.ReleaseUS != 350_000 {
			t.Fatalf("duck automation[%d] = %+v, want window [%d,%d) at %.1fdB", i, d, w[0], w[1], audio.BackgroundMusicGainDB)
		}
	}

	// Asset table: 4 entries (deduped BGM + 3 SFX).
	if len(result.Assets) != 4 {
		t.Fatalf("assets = %+v, want 4 entries", result.Assets)
	}
}

// TestCompileAudioWithIntents_NoIntentsCompilesPrimaryPlan certifies that
// an absent intent block still compiles through the canonical path (the
// legacy primary plan, no layers).
func TestCompileAudioWithIntents_NoIntentsCompilesPrimaryPlan(t *testing.T) {
	result, err := CompileAudioWithIntents(
		context.Background(),
		intentCompileTimeline(),
		audio.DefaultAudioProfile(),
		"",
		nil,
		nil,
		intentCompileSource(),
	)
	if err != nil {
		t.Fatalf("compile without intents: %v", err)
	}
	if len(eventsForRole(result.Plan, audio.TrackBGM)) != 0 || len(eventsForRole(result.Plan, audio.TrackSFX)) != 0 {
		t.Fatalf("no-intent plan must carry no layers: %+v", result.Plan.Tracks)
	}
	if len(result.Plan.Automation) != 0 {
		t.Fatalf("no-intent plan must carry no automation: %+v", result.Plan.Automation)
	}
}

func TestCompileAudioWithIntents_FailClosed(t *testing.T) {
	tests := []struct {
		name   string
		bgm    []scriptpkg.BackgroundMusicIntent
		sfx    []scriptpkg.SoundEffectIntent
		source *fakeAudioAssetSource
	}{
		{
			name:   "bgm_without_certified_duration",
			bgm:    intentBGM(),
			source: newFakeAudioAssetSource(map[string]string{"bgm_20s": "/m/bgm.m4a"}), // DurationUS 0
		},
		{
			name:   "unknown_asset",
			bgm:    intentBGM(),
			source: newFakeAudioAssetSource(map[string]string{}),
		},
		{
			name:   "sfx_without_explicit_or_source_duration",
			sfx:    []scriptpkg.SoundEffectIntent{{AssetID: "sfx_mystery", AtMS: 5_000}},
			source: newFakeAudioAssetSource(map[string]string{"sfx_mystery": "/m/x.m4a"}), // DurationUS 0
		},
		{
			name: "sfx_trim_overruns_source",
			sfx:  []scriptpkg.SoundEffectIntent{{AssetID: "sfx_whoosh", AtMS: 5_000, SourceInMS: 900, DurationMS: 500}},
			source: func() *fakeAudioAssetSource {
				s := newFakeAudioAssetSource(map[string]string{"sfx_whoosh": "/m/whoosh.m4a"})
				s.assets["sfx_whoosh"] = audio.ResolvedAudioAsset{AssetID: "sfx_whoosh", Path: "/m/whoosh.m4a", DurationUS: 1_000_000}
				return s
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.source == nil {
				tt.source = intentCompileSource()
			}
			_, err := CompileAudioWithIntents(context.Background(), intentCompileTimeline(), audio.DefaultAudioProfile(), "", tt.bgm, tt.sfx, tt.source)
			if err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}

// TestCompileWithLayersAndPolicy_PolicyRecordedOnPlan certifies the
// editorial policy survives the layers compile and is recorded on the
// sealed plan (VOICEOVER_ONLY drops the clip track through the canonical
// applyMixPolicy path).
func TestCompileWithLayersAndPolicy_PolicyRecordedOnPlan(t *testing.T) {
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 10_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene-1", Index: 0, TimelineStartUS: 0, DurationUS: 10_000_000,
			AudioIntents: []audio.AudioIntent{
				{Mode: audio.AudioVoiceover, VoiceoverAssetID: "vo-1", SourceDurationUS: 8_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 10_000_000},
				{Mode: audio.AudioClip, ClipAssetID: "clip-1", SourceInUS: 0, SourceDurationUS: 10_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 10_000_000, UseOriginalAudio: true},
			},
		}},
	}
	plan, err := audio.CompileWithLayersAndPolicy(timeline, audio.DefaultAudioProfile(), nil, nil, nil, audio.MixVoiceoverOnly)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MixPolicy != audio.MixVoiceoverOnly {
		t.Fatalf("mix policy = %q, want %q", plan.MixPolicy, audio.MixVoiceoverOnly)
	}
	if eventsForRole(plan, audio.TrackClipAudio) != nil {
		t.Fatalf("VOICEOVER_ONLY must drop the clip track: %+v", plan.Tracks)
	}
	if len(eventsForRole(plan, audio.TrackVoiceover)) != 1 {
		t.Fatalf("voiceover must remain: %+v", plan.Tracks)
	}
}
