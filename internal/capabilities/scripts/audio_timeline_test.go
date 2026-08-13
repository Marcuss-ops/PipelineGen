package scriptgeneration

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func eventsForRole(plan audio.CompiledAudioPlan, role audio.AudioTrackRole) []audio.AudioEvent {
	var events []audio.AudioEvent
	for _, track := range plan.Tracks {
		if track.Role == role {
			events = append(events, track.Events...)
		}
	}
	return events
}

func TestCompileCanonicalAudioPlanUsesOneTimelineForPrimaryEvents(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{
		{ID: "scene-001", Index: 0, DurationMS: 14000, Audio: audio.AudioIntent{Mode: audio.AudioVoiceover}, Voiceover: map[Language]AudioReference{"en": {ID: "vo-1", FilePath: "/audio/vo-1.mp3"}}},
		{ID: "scene-002", Index: 1, DurationMS: 12000, Clip: &ClipReference{ID: "clip-1", AudioPath: "/video/clip-1.mp4"}, Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-1", SourceInUS: 34000000, SourceDurationUS: 12000000}},
		{ID: "scene-003", Index: 2, DurationMS: 2000, Audio: audio.AudioIntent{Mode: audio.AudioSilence}},
	}}
	timeline, plan, assets, err := CompileCanonicalAudioPlan(result, "en", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	if timeline.DurationUS != 28000000 || plan.DurationUS != timeline.DurationUS || len(eventsForRole(plan, audio.TrackVoiceover))+len(eventsForRole(plan, audio.TrackClipAudio)) != 3 {
		t.Fatalf("timeline=%+v plan=%+v", timeline, plan)
	}
	clipEvents := eventsForRole(plan, audio.TrackClipAudio)
	if clipEvents[0].TimelineStartUS != 14000000 || clipEvents[0].SourceInUS != 34000000 || clipEvents[0].SourceDurationUS != 12000000 {
		t.Fatalf("clip event=%+v", clipEvents[0])
	}
	if len(assets) != 2 || plan.PlanSHA256 == "" {
		t.Fatalf("assets=%+v hash=%q", assets, plan.PlanSHA256)
	}
}

func TestCompileCanonicalAudioPlanMakesVoiceoverTimelinePlacementExplicit(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-vo", Index: 0, DurationUS: 4_500_000,
		Audio:     audio.AudioIntent{Mode: audio.AudioVoiceover},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-1", FilePath: "/audio/vo-1.m4a", Duration: 3.2}},
	}}}
	timeline, _, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	intents := timeline.Segments[0].EffectiveAudioIntents()
	if len(intents) != 1 {
		t.Fatalf("intents=%+v", intents)
	}
	vo := intents[0]
	if vo.Mode != audio.AudioVoiceover || vo.TimelineOffsetUS != 0 || vo.TimelineDurationUS != 4_500_000 || vo.SourceInUS != 0 || vo.SourceDurationUS != 3_200_000 {
		t.Fatalf("voiceover intent must carry explicit timeline placement: %+v", vo)
	}
}

func TestCompileCanonicalAudioPlanFailsClosedForMissingClipAudio(t *testing.T) {
	_, _, _, err := CompileCanonicalAudioPlan(GenerateResult{Scenes: []Scene{{
		ID: "scene-1", Index: 0, DurationMS: 1000, Clip: &ClipReference{ID: "clip-1"}, Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-1"},
	}}}, "en", audio.DefaultAudioProfile())
	if err == nil {
		t.Fatal("missing clip audio must fail closed")
	}
}

func TestCompileCanonicalAudioPlanPreservesUSDurationAndCombinedIntents(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "comedian-a", Index: 0, DurationUS: 5_600_000,
		Clip:      &ClipReference{ID: "clip-a", AudioPath: "/video/a.mp4", SourceInMS: 33200, SourceOutMS: 38800},
		Audio:     audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-a", SourceInUS: 33_200_000, SourceDurationUS: 5_600_000, UseOriginalAudio: true},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-a", FilePath: "/audio/a.m4a"}},
	}}}
	timeline, plan, assets, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	if timeline.Segments[0].DurationUS != 5_600_000 || len(timeline.Segments[0].AudioIntents) != 2 {
		t.Fatalf("resolved scene timing/intents lost: %#v", timeline.Segments[0])
	}
	if len(plan.Tracks) != 2 || len(plan.Tracks[0].Events) != 1 || len(plan.Tracks[1].Events) != 1 {
		t.Fatalf("combined plan not multi-track: %#v", plan.Tracks)
	}
	if len(assets) != 2 {
		t.Fatalf("expected clip and voiceover assets, got %#v", assets)
	}
}

func TestCompileCanonicalAudioPlanUsesEveryClipOwnedByScene(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-multi", Index: 0,
		Clips: []*ClipReference{
			{ID: "clip-a", AudioPath: "/video/a.mp4", SourceOutMS: 2000, Duration: 2},
			{ID: "clip-b", AudioPath: "/video/b.mp4", SourceOutMS: 3000, Duration: 3},
		},
		Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-a", SourceDurationUS: 2000000},
		AudioIntents: []audio.AudioIntent{
			{Mode: audio.AudioClip, ClipAssetID: "clip-a", SourceDurationUS: 2000000, TimelineDurationUS: 2000000, UseOriginalAudio: true},
			{Mode: audio.AudioClip, ClipAssetID: "clip-b", SourceDurationUS: 3000000, TimelineOffsetUS: 2000000, TimelineDurationUS: 3000000, UseOriginalAudio: true},
		},
	}}}
	timeline, _, assets, err := CompileCanonicalAudioPlan(result, "en", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	videos := timeline.Segments[0].EffectiveVideoSegments()
	if len(videos) != 2 || videos[0].AssetID != "clip-a" || videos[1].AssetID != "clip-b" || videos[1].TimelineOffsetUS != 2000000 {
		t.Fatalf("multi-clip video projection = %+v", videos)
	}
	if len(assets) != 2 {
		t.Fatalf("multi-clip audio assets = %+v, want both clip assets", assets)
	}
}

func TestValidateChunkedVoiceoversRequiresOneToOneMapping(t *testing.T) {
	base := GenerateResult{Scenes: []Scene{
		{ID: "scene-1", Index: 0, Text: map[Language]string{"en": "hello"}, Voiceover: map[Language]AudioReference{"en": {ID: "vo-1", FilePath: "/vo-1.mp3"}}},
		{ID: "scene-2", Index: 1, Text: map[Language]string{"en": "world"}, Voiceover: map[Language]AudioReference{"en": {ID: "vo-2", FilePath: "/vo-2.mp3"}}},
	}}
	if err := ValidateChunkedVoiceovers(base); err != nil {
		t.Fatal(err)
	}
	base.Scenes[1].Voiceover["en"] = base.Scenes[0].Voiceover["en"]
	if err := ValidateChunkedVoiceovers(base); err == nil {
		t.Fatal("duplicate voiceover mapping must fail")
	}
	delete(base.Scenes[1].Voiceover, "en")
	if err := ValidateChunkedVoiceovers(base); err == nil {
		t.Fatal("missing voiceover mapping must fail")
	}
}
