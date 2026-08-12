package scriptgeneration

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

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
	if timeline.DurationUS != 28000000 || plan.DurationUS != timeline.DurationUS || len(plan.Events) != 3 {
		t.Fatalf("timeline=%+v plan=%+v", timeline, plan)
	}
	if plan.Events[1].TimelineStartUS != 14000000 || plan.Events[1].SourceInUS != 34000000 || plan.Events[1].SourceDurationUS != 12000000 {
		t.Fatalf("clip event=%+v", plan.Events[1])
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
