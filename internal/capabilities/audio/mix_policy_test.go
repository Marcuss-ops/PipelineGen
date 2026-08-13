package audio

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

func TestCompileWithMixPolicy_VoiceoverOnlyDropsClipAudio(t *testing.T) {
	plan, err := CompileWithMixPolicy(combinedTimeline(), DefaultAudioProfile(), MixVoiceoverOnly)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MixPolicy != MixVoiceoverOnly {
		t.Fatalf("mix policy = %q, want %q", plan.MixPolicy, MixVoiceoverOnly)
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
	plan, err := CompileWithMixPolicy(combinedTimeline(), DefaultAudioProfile(), MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MixPolicy != MixVoiceoverWithDuckedClip {
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
	if clip.Events[0].GainDB != DuckClipBaseGainDB {
		t.Fatalf("clip gain = %v, want %v", clip.Events[0].GainDB, DuckClipBaseGainDB)
	}

	if len(plan.Automation) != 1 {
		t.Fatalf("automation = %+v, want exactly one ducking entry", plan.Automation)
	}
	a := plan.Automation[0]
	if a.TargetTrackID != "clip_audio" || a.TriggerTrackID != "voiceover" || a.GainDB != DuckClipActiveGainDB {
		t.Fatalf("ducking automation = %+v", a)
	}
	if a.StartUS != 0 || a.EndUS != 10_000_000 {
		t.Fatalf("duck window = [%d,%d), want [0,10000000)", a.StartUS, a.EndUS)
	}
	if a.AttackUS != DuckAttackUS || a.ReleaseUS != DuckReleaseUS {
		t.Fatalf("duck ramps = [%d,%d), want [%d,%d)", a.AttackUS, a.ReleaseUS, DuckAttackUS, DuckReleaseUS)
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
	plan, err := CompileWithMixPolicy(tl, DefaultAudioProfile(), MixVoiceoverWithDuckedClip)
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
	plan, err := CompileWithMixPolicy(tl, DefaultAudioProfile(), MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatal(err)
	}
	clip := findTrack(plan.Tracks, TrackClipAudio)
	if clip == nil || len(clip.Events) != 1 || clip.Events[0].GainDB != -6 {
		t.Fatalf("explicit clip gain must be preserved: %+v", clip)
	}
}
