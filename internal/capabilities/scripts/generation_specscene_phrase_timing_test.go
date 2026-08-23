package scriptgeneration_test

import (
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"

	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func phraseTimingDocModel() *scriptpkg.ModelScriptOutputV1 {
	return &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "Scene zero.", Kind: scriptpkg.SceneNarration},
			{ID: "scene-1", Index: 1, Text: "Scene one.", Kind: scriptpkg.SceneNarration},
		},
	}}
}

func phraseTimingDocProjection() []capabilityaudio.SceneSpeechTiming {
	return []capabilityaudio.SceneSpeechTiming{
		{
			SceneID: "scene-1",
			Phrases: []capabilityaudio.PhraseTiming{
				{
					SceneIndex:      1,
					PhraseIndex:     0,
					Text:            "Jackie Chan became known around the world.",
					WordStart:       0,
					WordEnd:         6,
					LocalStartUS:    450_000,
					LocalEndUS:      3_100_000,
					TimelineStartUS: 8_200_000,
					GlobalStartUS:   8_650_000,
					GlobalEndUS:     11_300_000,
				},
			},
		},
	}
}

func TestDocument_PhraseTimingsProjectedInHumanAndMachineSurface(t *testing.T) {
	t.Parallel()

	out := mustRender(t, phraseTimingDocModel(), scriptgeneration.DocumentRenderOptions{
		Title:              "Phrase timing",
		SceneSpeechTimings: phraseTimingDocProjection(),
	})

	// Human surface: phrase text + local/master spans, never word boundaries.
	human := humanDocumentHTML(t, out)
	require.Contains(t, human, "<h3>Phrase Timing</h3>")
	require.Contains(t, human, "<strong>Phrase 1:</strong> Jackie Chan became known around the world.")
	require.Contains(t, human, "Local: 00:00.450 → 00:03.100")
	require.Contains(t, human, "Master: 00:08.650 → 00:11.300")
	require.NotContains(t, human, "word_start")
	require.NotContains(t, human, "word_end")

	// Word-level timing is available through the linked timing artifacts, but
	// is intentionally not embedded as a huge JSON block in the Doc.
	require.NotContains(t, out, "Scene Speech Timing JSON")
	require.NotContains(t, out, `"start_us"`)
	require.NotContains(t, out, `"end_us"`)
}

func TestDocument_OmitsPhraseTimingWithoutProjection(t *testing.T) {
	t.Parallel()

	out := mustRender(t, phraseTimingDocModel(), scriptgeneration.DocumentRenderOptions{Title: "No phrase timing"})
	require.NotContains(t, out, "Scene Speech Timing JSON")
	require.NotContains(t, out, "<h3>Phrase Timing</h3>")
}

func TestDocument_TimingLinksRenderedFromVoiceoverBinding(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{{
			ID:    "scene-0",
			Index: 0,
			Text:  "Scene with timing.",
			Bindings: scriptpkg.SceneBindings{
				Voiceover: &scriptpkg.VoiceoverBinding{
					Status: "completed",
					Links:  map[string]string{"en": "https://drive.google.com/file/d/voice-en/view"},
					Timing: map[string]scriptpkg.VoiceoverTimingBinding{
						"en": {
							Status:   "completed",
							JSONLink: "https://drive.google.com/file/d/timing-en-json/view",
							SRTLink:  "https://drive.google.com/file/d/timing-en-srt/view",
							VTTLink:  "https://drive.google.com/file/d/timing-en-vtt/view",
						},
					},
				},
			},
		}},
	}}

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		Title:           "Timing links",
		Language:        "en",
		DefaultLanguage: "en",
	})
	human := humanDocumentHTML(t, out)
	require.Contains(t, human, "<strong>Timing JSON:</strong>")
	require.Contains(t, human, "https://drive.google.com/file/d/timing-en-json/view")
	require.Contains(t, human, "<strong>Timing SRT:</strong>")
	require.Contains(t, human, "https://drive.google.com/file/d/timing-en-srt/view")
	require.Contains(t, human, "<strong>Timing VTT:</strong>")
	require.Contains(t, human, "https://drive.google.com/file/d/timing-en-vtt/view")
}

func TestDocument_OmitsTimingLinksForWrongLanguage(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{{
			ID:    "scene-0",
			Index: 0,
			Text:  "Scene.",
			Bindings: scriptpkg.SceneBindings{
				Voiceover: &scriptpkg.VoiceoverBinding{
					Timing: map[string]scriptpkg.VoiceoverTimingBinding{
						"en": {JSONLink: "https://drive.google.com/file/d/timing-en/view"},
					},
				},
			},
		}},
	}}

	// The document is built for "it"; the only timing bundle is "en".
	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		Title:           "Wrong language",
		Language:        "it",
		DefaultLanguage: "it",
	})
	human := humanDocumentHTML(t, out)
	require.NotContains(t, human, "Timing JSON")
	require.NotContains(t, human, "timing-en/view")
}
