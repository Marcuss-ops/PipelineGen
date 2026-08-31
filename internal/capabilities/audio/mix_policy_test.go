package audio

import kernelaudio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"

import "testing"

func combinedTimeline() CanonicalTimeline {
	return CanonicalTimeline{
		Version:    TimelineVersion,
		DurationUS: 10_000_000,
		Segments: []TimelineSegment{{
			ID: "scene-1", Index: 0, TimelineStartUS: 0, DurationUS: 10_000_000,
			AudioIntents: []AudioIntent{
				{Mode: AudioVoiceover, VoiceoverAssetID: "vo-1", SourceDurationUS: 8_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 10_000_000},
				{Mode: AudioClip, ClipAssetID: "clip-1", SourceInUS: 0, SourceDurationUS: 10_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 10_000_000, UseOriginalAudio: true},
			},
		}},
	}
}

func protectedFixedTimeline() CanonicalTimeline {
	return CanonicalTimeline{
		Version:    TimelineVersion,
		DurationUS: 10_000_000,
		Segments: []TimelineSegment{
			{
				ID: "intro", Index: 0, TimelineStartUS: 0, DurationUS: 3_000_000,
				AudioIntents: []AudioIntent{
					{Mode: AudioVoiceover, VoiceoverAssetID: "fixed-vo-forbidden", SourceDurationUS: 3_000_000, TimelineDurationUS: 3_000_000},
					{Mode: AudioClip, ClipAssetID: "intro-clip", SourceDurationUS: 3_000_000, TimelineDurationUS: 3_000_000, UseOriginalAudio: true, ProtectedOriginalAudio: true},
				},
			},
			{
				ID: "body", Index: 1, TimelineStartUS: 3_000_000, DurationUS: 7_000_000,
				AudioIntents: []AudioIntent{
					{Mode: AudioVoiceover, VoiceoverAssetID: "body-vo", SourceDurationUS: 7_000_000, TimelineDurationUS: 7_000_000},
					{Mode: AudioClip, ClipAssetID: "body-clip", SourceDurationUS: 7_000_000, TimelineDurationUS: 7_000_000, UseOriginalAudio: true},
				},
			},
		},
	}
}

func TestCompileWithMixPolicy_VoiceoverOnlyKeepsProtectedFixedAudio(t *testing.T) {
	plan, err := CompileWithMixPolicy(protectedFixedTimeline(), DefaultAudioProfile(), kernelaudio.MixVoiceoverOnly)
	if err != nil {
		t.Fatal(err)
	}
	clips := findTrack(plan.Tracks, TrackClipAudio)
	if clips == nil || len(clips.Events) != 1 || clips.Events[0].AssetID != "intro-clip" || !clips.Events[0].ProtectedOriginalAudio {
		t.Fatalf("protected fixed clip must survive VOICEOVER_ONLY: %+v", clips)
	}
	voiceover := findTrack(plan.Tracks, TrackVoiceover)
	if voiceover == nil || len(voiceover.Events) != 1 || voiceover.Events[0].AssetID != "body-vo" {
		t.Fatalf("fixed-media voiceover must be excluded: %+v", voiceover)
	}
}

func TestCompileWithMixPolicy_DuckedPolicyDoesNotTouchProtectedFixedAudio(t *testing.T) {
	plan, err := CompileWithMixPolicy(protectedFixedTimeline(), DefaultAudioProfile(), kernelaudio.MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatal(err)
	}
	clips := findTrack(plan.Tracks, TrackClipAudio)
	if clips == nil || len(clips.Events) != 2 {
		t.Fatalf("both fixed and body clip audio must remain: %+v", clips)
	}
	if clips.Events[0].AssetID != "intro-clip" || clips.Events[0].GainDB != 0 || !clips.Events[0].ProtectedOriginalAudio {
		t.Fatalf("fixed clip was altered by ducking: %+v", clips.Events[0])
	}
	if clips.Events[1].AssetID != "body-clip" || clips.Events[1].GainDB != kernelaudio.DuckClipBaseGainDB {
		t.Fatalf("body clip should use the normal ducked policy: %+v", clips.Events[1])
	}
	if len(plan.Automation) != 1 || plan.Automation[0].StartUS != 3_000_000 || plan.Automation[0].EndUS != 10_000_000 {
		t.Fatalf("ducking must exclude the fixed span: %+v", plan.Automation)
	}
}

func TestCompileWithLayersAndPolicy_DropsImplicitLayersAndAutomationOnFixedMedia(t *testing.T) {
	bgm := []AudioLayer{{AssetID: "bgm", TimelineStartUS: 0, DurationUS: 10_000_000}}
	sfx := []AudioLayer{{AssetID: "sfx", TimelineStartUS: 1_000_000, DurationUS: 500_000}}
	automation := []AudioAutomation{
		{TargetTrackID: "bgm", StartUS: 1_000_000, EndUS: 2_000_000, GainDB: -30},
		{TargetTrackID: "bgm", StartUS: 3_000_000, EndUS: 4_000_000, GainDB: -30},
	}
	plan, err := CompileWithLayersAndPolicy(protectedFixedTimeline(), DefaultAudioProfile(), bgm, sfx, automation, kernelaudio.MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatal(err)
	}
	bgmTrack := findTrack(plan.Tracks, TrackBGM)
	if bgmTrack == nil || len(bgmTrack.Events) != 1 || bgmTrack.Events[0].TimelineStartUS != 3_000_000 || bgmTrack.Events[0].DurationUS != 7_000_000 {
		t.Fatalf("BGM must be retained only outside the fixed-media span: %+v", bgmTrack)
	}
	if findTrack(plan.Tracks, TrackSFX) != nil {
		t.Fatalf("SFX inside the fixed-media span must be removed: %+v", plan.Tracks)
	}
	if len(plan.Automation) != 2 {
		t.Fatalf("body automation should remain while fixed-span automation is removed: %+v", plan.Automation)
	}
	for _, item := range plan.Automation {
		if item.StartUS < 3_000_000 || item.EndUS > 10_000_000 {
			t.Fatalf("automation must not affect fixed media: %+v", item)
		}
	}
}

func TestCompileWithMixPolicy_VoiceoverOnlyDropsClipAudio(t *testing.T) {
	plan, err := CompileWithMixPolicy(combinedTimeline(), DefaultAudioProfile(), kernelaudio.MixVoiceoverOnly)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MixPolicy != kernelaudio.MixVoiceoverOnly {
		t.Fatalf("mix policy = %q, want %q", plan.MixPolicy, kernelaudio.MixVoiceoverOnly)
	}
	if findTrack(plan.Tracks, TrackClipAudio) != nil {
		t.Fatalf("clip track must be removed, tracks=%+v", plan.Tracks)
	}
	vo := findTrack(plan.Tracks, TrackVoiceover)
	if vo == nil || len(vo.Events) != 1 {
		t.Fatalf("voiceover track must remain, tracks=%+v", plan.Tracks)
	}
}

func TestCompileWithMixPolicy_DuckedClipAppliesGainAndAutomation(t *testing.T) {
	plan, err := CompileWithMixPolicy(combinedTimeline(), DefaultAudioProfile(), kernelaudio.MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MixPolicy != kernelaudio.MixVoiceoverWithDuckedClip {
		t.Fatalf("mix policy = %q", plan.MixPolicy)
	}

	vo := findTrack(plan.Tracks, TrackVoiceover)
	if vo == nil || len(vo.Events) != 1 || vo.Events[0].GainDB != 0 {
		t.Fatalf("voiceover must stay at unity: %+v", vo)
	}

	clip := findTrack(plan.Tracks, TrackClipAudio)
	if clip == nil || len(clip.Events) != 1 {
		t.Fatalf("clip track missing, tracks=%+v", plan.Tracks)
	}
	if clip.Events[0].GainDB != kernelaudio.DuckClipBaseGainDB {
		t.Fatalf("clip gain = %v, want %v", clip.Events[0].GainDB, kernelaudio.DuckClipBaseGainDB)
	}

	if len(plan.Automation) != 1 {
		t.Fatalf("automation = %+v, want exactly one ducking entry", plan.Automation)
	}
	a := plan.Automation[0]
	if a.TargetTrackID != "clip_audio" || a.TriggerTrackID != "voiceover" || a.GainDB != kernelaudio.DuckClipActiveGainDB {
		t.Fatalf("ducking automation = %+v", a)
	}
	if a.StartUS != 0 || a.EndUS != 8_000_000 {
		t.Fatalf("duck window = [%d,%d), want [0,8000000) (ends at the 8s voiceover source duration)", a.StartUS, a.EndUS)
	}
	if a.AttackUS != kernelaudio.DuckAttackUS || a.ReleaseUS != kernelaudio.DuckReleaseUS {
		t.Fatalf("duck ramps = [%d,%d), want [%d,%d)", a.AttackUS, a.ReleaseUS, kernelaudio.DuckAttackUS, kernelaudio.DuckReleaseUS)
	}
}

func TestCompileWithMixPolicy_DuckingCoversWholeWindowWhenSourceDurationMatches(t *testing.T) {
	tl := combinedTimeline()
	// Voiceover source duration equals the scene window: the duck zone must
	// span the full window (no early release).
	tl.Segments[0].AudioIntents[0].SourceDurationUS = 10_000_000
	plan, err := CompileWithMixPolicy(tl, DefaultAudioProfile(), kernelaudio.MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Automation) != 1 {
		t.Fatalf("automation = %+v, want exactly one ducking entry", plan.Automation)
	}
	a := plan.Automation[0]
	if a.StartUS != 0 || a.EndUS != 10_000_000 {
		t.Fatalf("duck window = [%d,%d), want [0,10000000)", a.StartUS, a.EndUS)
	}
}

func TestCompileWithMixPolicy_DuckedClipWithoutVoiceoverLeavesClipAtUnity(t *testing.T) {
	tl := CanonicalTimeline{
		Version:    TimelineVersion,
		DurationUS: 10_000_000,
		Segments: []TimelineSegment{{
			ID: "scene-1", Index: 0, TimelineStartUS: 0, DurationUS: 10_000_000,
			AudioIntents: []AudioIntent{
				{Mode: AudioClip, ClipAssetID: "clip-1", SourceInUS: 0, SourceDurationUS: 10_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 10_000_000, UseOriginalAudio: true},
			},
		}},
	}
	plan, err := CompileWithMixPolicy(tl, DefaultAudioProfile(), kernelaudio.MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatal(err)
	}
	clip := findTrack(plan.Tracks, TrackClipAudio)
	if clip == nil || len(clip.Events) != 1 || clip.Events[0].GainDB != 0 {
		t.Fatalf("clip without voiceover must stay at unity: %+v", clip)
	}
	if len(plan.Automation) != 0 {
		t.Fatalf("no ducking automation expected, got %+v", plan.Automation)
	}
}

// TestNormalize_WireAliasVoiceoverWithDuckedClip certifies that the wire
// spelling documented in the HTTP payload ("voiceover_with_ducked_clip")
// normalizes to the canonical VOICEOVER_DUCKED_CLIP regardless of case.
func TestNormalize_WireAliasVoiceoverWithDuckedClip(t *testing.T) {
	tests := []struct {
		name  string
		input kernelaudio.AudioMixPolicy
		want  kernelaudio.AudioMixPolicy
	}{
		{name: "canonical_ducked", input: kernelaudio.MixVoiceoverWithDuckedClip, want: kernelaudio.MixVoiceoverWithDuckedClip},
		{name: "wire_alias_snake_case", input: "voiceover_with_ducked_clip", want: kernelaudio.MixVoiceoverWithDuckedClip},
		{name: "wire_alias_mixed_case", input: "Voiceover_With_Ducked_Clip", want: kernelaudio.MixVoiceoverWithDuckedClip},
		{name: "canonical_only", input: kernelaudio.MixVoiceoverOnly, want: kernelaudio.MixVoiceoverOnly},
		{name: "wire_alias_only", input: "voiceover_only", want: kernelaudio.MixVoiceoverOnly},
		{name: "empty_stays_empty", input: "", want: ""},
		{name: "unknown_fails_closed", input: "duck_everything", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.Normalize(); got != tt.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCompileWithMixPolicy_EmptyPolicyPreservesLegacyOverlap(t *testing.T) {
	plan, err := CompileWithMixPolicy(combinedTimeline(), DefaultAudioProfile(), "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.MixPolicy != "" {
		t.Fatalf("empty policy must stay empty, got %q", plan.MixPolicy)
	}
	clip := findTrack(plan.Tracks, TrackClipAudio)
	if clip == nil || len(clip.Events) != 1 || clip.Events[0].GainDB != 0 {
		t.Fatalf("legacy overlap must not duck clip: %+v", clip)
	}
	if len(plan.Automation) != 0 {
		t.Fatalf("legacy overlap must not add automation, got %+v", plan.Automation)
	}
}

func TestCompileWithMixPolicy_ExplicitClipGainIsPreserved(t *testing.T) {
	tl := combinedTimeline()
	tl.Segments[0].AudioIntents[1].GainDB = -6
	plan, err := CompileWithMixPolicy(tl, DefaultAudioProfile(), kernelaudio.MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatal(err)
	}
	clip := findTrack(plan.Tracks, TrackClipAudio)
	if clip == nil || len(clip.Events) != 1 || clip.Events[0].GainDB != -6 {
		t.Fatalf("explicit clip gain must be preserved: %+v", clip)
	}
}

func TestCompileWithLayers_EnforcesCanonicalBGMAndSFXLevels(t *testing.T) {
	bgm := []AudioLayer{{AssetID: "music", TimelineStartUS: 0, DurationUS: 10_000_000, GainDB: -2}}
	sfx := []AudioLayer{{AssetID: "whoosh", TimelineStartUS: 2_000_000, DurationUS: 500_000, GainDB: 0}}
	plan, err := CompileWithLayers(combinedTimeline(), DefaultAudioProfile(), bgm, sfx, nil)
	if err != nil {
		t.Fatal(err)
	}
	bgmTrack := findTrack(plan.Tracks, TrackBGM)
	sfxTrack := findTrack(plan.Tracks, TrackSFX)
	if bgmTrack == nil || len(bgmTrack.Events) != 1 || bgmTrack.Events[0].GainDB != kernelaudio.BackgroundMusicGainDB {
		t.Fatalf("BGM gain = %+v, want %.1f dB", bgmTrack, kernelaudio.BackgroundMusicGainDB)
	}
	if sfxTrack == nil || len(sfxTrack.Events) != 1 || sfxTrack.Events[0].GainDB != kernelaudio.SoundEffectGainDB {
		t.Fatalf("SFX gain = %+v, want %.1f dB", sfxTrack, kernelaudio.SoundEffectGainDB)
	}
}
