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
