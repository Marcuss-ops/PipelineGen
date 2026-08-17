package adapters_test

import (
	"encoding/json"
	"html"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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

	out := adapters.BuildSpecSceneDocumentHTML(phraseTimingDocModel(), adapters.SpecSceneDocumentOptions{
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

	// Machine surface: a byte-faithful Scene Speech Timing JSON snapshot.
	raw := extractSectionJSON(t, out, "<h2>Scene Speech Timing JSON</h2><pre><code>")
	var decoded []capabilityaudio.SceneSpeechTiming
	require.NoError(t, json.Unmarshal([]byte(raw), &decoded))
	require.Equal(t, phraseTimingDocProjection(), decoded)
}

func TestDocument_OmitsPhraseTimingWithoutProjection(t *testing.T) {
	t.Parallel()

	out := adapters.BuildSpecSceneDocumentHTML(phraseTimingDocModel(), adapters.SpecSceneDocumentOptions{Title: "No phrase timing"})
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

	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{
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
	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{
		Title:           "Wrong language",
		Language:        "it",
		DefaultLanguage: "it",
	})
	human := humanDocumentHTML(t, out)
	require.NotContains(t, human, "Timing JSON")
	require.NotContains(t, human, "timing-en/view")
}

// extractSectionJSON isolates the embedded JSON snapshot immediately after
// the given section marker and unescapes it for byte-faithful comparison.
func extractSectionJSON(t *testing.T, output, marker string) string {
	t.Helper()
	pos := strings.Index(output, marker)
	require.NotEqual(t, -1, pos, "section marker missing: %s", marker)
	pos += len(marker)
	end := strings.Index(output[pos:], "</code></pre>")
	require.NotEqual(t, -1, end, "section closing marker missing")
	return html.UnescapeString(output[pos : pos+end])
}
