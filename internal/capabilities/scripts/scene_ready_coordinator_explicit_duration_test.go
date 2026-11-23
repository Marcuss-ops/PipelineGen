package scriptgeneration

import (
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestMaterializeGeneratedScenesCarriesExplicitDuration(t *testing.T) {
	req := GenerateRequest{
		Source:         Source{SourceText: "Body clip with a short intro."},
		SourceLanguage: "en",
		ScriptParams: scriptpkg.ScriptSpec{SegmentWords: 10, Segments: []scriptpkg.ScriptSegment{
			{ID: "body-01", DurationMS: 43_000, Topic: "body", SourceText: "Body clip with a short intro."},
		}},
	}
	got := materializeGeneratedScenes(req, []Scene{{ID: "scene-0", Text: map[Language]string{"en": "Body clip with a short intro."}}})
	if len(got) != 1 || got[0].ID != "body-01" {
		t.Fatalf("materialized scenes = %+v, want one body-01 scene", got)
	}
	if got[0].DurationMS != 43_000 {
		t.Fatalf("materialized duration = %dms, want 43000ms", got[0].DurationMS)
	}
}

func TestApplyExplicitSegmentDurationOnStreamedScene(t *testing.T) {
	req := GenerateRequest{ScriptParams: scriptpkg.ScriptSpec{Segments: []scriptpkg.ScriptSegment{
		{ID: "body-01", DurationMS: 43_000},
	}}}
	scene := Scene{ID: "body-01", Index: 0, DurationMS: 8_568}
	applyExplicitSegmentDuration(req, &scene)
	if scene.DurationMS != 43_000 {
		t.Fatalf("streamed duration = %dms, want 43000ms", scene.DurationMS)
	}
}

func TestSealedSceneDurationPreservesExplicitClipDuration(t *testing.T) {
	// A 43s body clip with an 8.568s lead-in must remain 43s: the voiceover is
	// mixed inside the clip, not used to trim it.
	if got := sealedSceneDurationMS(43_000, capabilityaudio.AudioModeCombinedTimeline, true, 8.568); got != 43_000 {
		t.Fatalf("explicit clip duration = %dms, want 43000ms", got)
	}
	// Without an explicit duration the TTS span remains the seal (historical
	// narration-driven behaviour).
	if got := sealedSceneDurationMS(0, capabilityaudio.AudioModeCombinedTimeline, true, 8.568); got != 8_568 {
		t.Fatalf("voiceover-driven duration = %dms, want 8568ms", got)
	}
	// A clip-less scene still adopts its voiceover span.
	if got := sealedSceneDurationMS(0, capabilityaudio.AudioModeCombinedTimeline, false, 8.568); got != 8_568 {
		t.Fatalf("clip-less voiceover duration = %dms, want 8568ms", got)
	}
	// CODEX-09 / R34: a clip-less scene with a 9s planning estimate and a 5s
	// narration must seal to 5s, otherwise the extra 4s is dead air.
	if got := sealedSceneDurationMS(9_000, capabilityaudio.AudioModeCombinedTimeline, false, 5.0); got != 5_000 {
		t.Fatalf("clip-less measured duration = %dms, want 5000ms (planning estimate must not leave dead air)", got)
	}
}
