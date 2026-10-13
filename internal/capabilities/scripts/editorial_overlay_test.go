package scriptgeneration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func editorialOverlayFixture() (*GenerateResult, []Scene, []ResolvedScene, []capabilityoverlay.SceneInput) {
	text := "Steel output is collapsing. Duisburg factory orders fell sharply."
	words := strings.Fields(text)
	boundaries := make([]capabilityaudio.SpeechWordTiming, len(words))
	for i, word := range words {
		boundaries[i] = capabilityaudio.SpeechWordTiming{Index: i, Text: word, StartUS: int64(i) * 100_000, EndUS: int64(i+1) * 100_000}
	}
	timing := capabilityaudio.SpeechTimingArtifact{Version: capabilityaudio.SpeechTimingVersion, Provider: "test", BoundaryMode: capabilityaudio.BoundaryWord, Language: "en", TextSHA256: "text", AudioSHA256: "audio", DurationUS: int64(len(words)) * 100_000, Words: boundaries}
	title := "Steel output is collapsing."
	transcript := "Steel output is collapsing.\n\nDuisburg factory orders fell sharply."
	manifest := &scriptpkg.EditorialManifest{SchemaVersion: scriptpkg.EditorialManifestVersion, SourceLanguage: "en", ProfileVersion: "highlights.v1", Certified: true, Fingerprint: strings.Repeat("a", 64), Scenes: []scriptpkg.EditorialScene{{
		SceneID: "s1", Title: &title, TitleStatus: "resolved", TitleSource: "topic_validated", GloballyIndexed: true,
		Bullets: []scriptpkg.EditorialSceneBullet{{SentenceStart: 1, SentenceEnd: 2, Text: "Duisburg factory orders fell sharply."}}, Highlights: []scriptpkg.EditorialHighlight{},
	}}}
	scene := Scene{ID: "s1", Index: 0, Text: map[Language]string{"en": text}, Voiceover: map[Language]AudioReference{"en": {ID: "vo-s1", Timing: &timing}}}
	result := &GenerateResult{SourceLanguage: "en", Output: GenerateOutput{Text: transcript}, Editorial: manifest, Scenes: []Scene{scene}}
	return result, []Scene{scene}, []ResolvedScene{{ID: "s1", Index: 0, TimelineStartUS: 2_000_000, DurationUS: 3_000_000}}, []capabilityoverlay.SceneInput{{ID: "s1"}}
}

func TestProjectEditorialOverlaysGroundedAndAudioTimed(t *testing.T) {
	result, scenes, resolved, inputs := editorialOverlayFixture()
	got := projectEditorialOverlays(result, "en", scenes, resolved, inputs)
	require.Len(t, got, 1)
	require.Len(t, got[0].Phrases, 2)
	roles := map[string]capabilityoverlay.TimedAnnotation{}
	for _, phrase := range got[0].Phrases {
		roles[phrase.PresentationRole] = phrase
		require.GreaterOrEqual(t, phrase.StartUS, int64(2_000_000))
		require.LessOrEqual(t, phrase.StartUS+phrase.DurationUS, int64(5_000_000))
	}
	title := roles["scene_title"]
	require.Equal(t, "Steel output is collapsing.", title.Text)
	require.Equal(t, int64(2_000_000), title.StartUS)
	bullet := roles["scene_bullet"]
	require.Equal(t, "Duisburg factory orders fell sharply.", bullet.Text)
	require.True(t, bullet.StartUS >= title.StartUS+title.DurationUS, "bullet begins after its title span")
}

func TestProjectEditorialOverlaysFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GenerateResult, []Scene)
	}{
		{"uncertified", func(r *GenerateResult, _ []Scene) { r.Editorial.Certified = false }},
		{"bad fingerprint", func(r *GenerateResult, _ []Scene) { r.Editorial.Fingerprint = "" }},
		{"language mismatch", func(r *GenerateResult, _ []Scene) { r.Editorial.SourceLanguage = "it" }},
		{"unmatched scene", func(r *GenerateResult, _ []Scene) { r.Editorial.Scenes[0].SceneID = "other" }},
		{"ungrounded title", func(r *GenerateResult, _ []Scene) { bad := "invented title"; r.Editorial.Scenes[0].Title = &bad }},
		{"source language mismatch", func(r *GenerateResult, _ []Scene) { r.SourceLanguage = "it" }},
		{"unsupported title source", func(r *GenerateResult, _ []Scene) { r.Editorial.Scenes[0].TitleSource = "model_generated" }},
		{"ungrounded bullet", func(r *GenerateResult, _ []Scene) { r.Editorial.Scenes[0].Bullets[0].Text = "Not spoken." }},
		{"invalid bullet span", func(r *GenerateResult, _ []Scene) { r.Editorial.Scenes[0].Bullets[0].SentenceEnd = 0 }},
		{"missing timing", func(r *GenerateResult, scenes []Scene) { scenes[0].Voiceover = nil; r.Scenes = scenes }},
		{"missing scene bounds", func(_ *GenerateResult, _ []Scene) {}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, scenes, resolved, inputs := editorialOverlayFixture()
			tc.mutate(result, scenes)
			if tc.name == "missing scene bounds" {
				resolved = nil
			}
			got := projectEditorialOverlays(result, "en", scenes, resolved, inputs)
			require.Empty(t, got[0].Phrases, "invalid editorial input cannot leak into visual candidates")
		})
	}
}

func TestEditorialChartValuesMustBeGroundedStrictLiterals(t *testing.T) {
	transcript := "The metric increased from 42% to €18.5, while thirteen jobs changed."
	title := "metric increased"
	base := scriptpkg.EditorialScene{
		SceneID: "scene-1", Title: &title, TitleStatus: "resolved", TitleSource: "topic_validated",
		Bullets: []scriptpkg.EditorialSceneBullet{}, Highlights: []scriptpkg.EditorialHighlight{},
		ChartValues: []string{"42%", "€18.5"},
	}
	text := "The metric increased from 42% to €18.5, while thirteen jobs changed."
	require.True(t, validEditorialManifestScene(base, text, transcript))
	base.ChartValues = []string{"42%", "thirteen"}
	require.False(t, validEditorialManifestScene(base, text, transcript), "number words must not authorize charts")
	base.ChartValues = []string{"42%", "999%"}
	require.False(t, validEditorialManifestScene(base, text, transcript), "invented values must not authorize charts")
}

func TestProjectEditorialOverlaysRequiresExactVoiceoverLanguageAndSceneText(t *testing.T) {
	result, scenes, resolved, inputs := editorialOverlayFixture()
	result.Editorial.SourceLanguage = "it"
	got := projectEditorialOverlays(result, "en", scenes, resolved, inputs)
	require.Empty(t, got[0].Phrases)

	result, scenes, resolved, inputs = editorialOverlayFixture()
	scenes[0].Text["en"] = "Translated steel output is collapsing. Duisburg factory orders fell sharply."
	timing := *scenes[0].Voiceover["en"].Timing
	timing.Words[0].Text = "Translated"
	scenes[0].Voiceover["en"] = AudioReference{ID: "vo-s1", Timing: &timing}
	result.Scenes = scenes
	got = projectEditorialOverlays(result, "en", scenes, resolved, inputs)
	require.Empty(t, got[0].Phrases, "both title/bullet must ground in the actual scene and timing")
}
