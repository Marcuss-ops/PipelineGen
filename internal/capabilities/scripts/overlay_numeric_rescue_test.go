package scriptgeneration

// Regression coverage for the live C1 break (job_1792433783340043590):
// NLP wraps numbers in leading prose ("si attesta sull'18"), TTS speaks
// digits as words (locate fails), and the same word yields two annotations
// ("42" NUMBER + "42%" PERCENT). All three must still produce one clean
// "18%" number with certified timing and no twin bars.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestCleanNumericOverlayValue(t *testing.T) {
	cases := map[string]string{
		"42%":                            "42%",
		"42":                             "42",
		"8 ottobre 2026 per":             "8 ottobre 2026",
		"partendo dal 13 agosto 2007":    "13 agosto 2007",
		"si attesta sull'18":             "18",
		"l'avversario resta al diciotto": "diciotto", // Compound Italian number words were never admitted by
		// isNumericOverlayValue (only bare "quaranta" is): unchanged.
		"quarantaquattro": "",
		"circa":           "",
		"molto":           "",
		"":                "",
	}
	for in, want := range cases {
		assert.Equal(t, want, cleanNumericOverlayValue(in), "clean(%q)", in)
	}
}

func TestRecoverNumericSuffix(t *testing.T) {
	text := "mentre la forza avversaria fatica a superare sull'18% dei voti"
	mention := scriptpkg.AnnotationSpan{Text: "sull'18", StartRune: 51, EndRune: 58}
	// Compute the real offsets instead of hardcoding them.
	runes := []rune(text)
	start := -1
	for i := 0; i+len([]rune("sull'18")) <= len(runes); i++ {
		if string(runes[i:i+len([]rune("sull'18"))]) == "sull'18" {
			start = i
			break
		}
	}
	require.NotEqual(t, -1, start)
	mention.StartRune, mention.EndRune = start, start+len([]rune("sull'18"))
	assert.Equal(t, "18%", recoverNumericSuffix(text, "18", []scriptpkg.AnnotationSpan{mention}))
	assert.Equal(t, "18", recoverNumericSuffix("nessun segno qui 18 voti", "18", []scriptpkg.AnnotationSpan{mention}))
	assert.Equal(t, "diciotto", recoverNumericSuffix(text, "diciotto", []scriptpkg.AnnotationSpan{mention}))
	assert.Equal(t, "", recoverNumericSuffix(text, "", []scriptpkg.AnnotationSpan{mention}))
}

func rescueScene() Scene {
	text := "Il partito raggiunge il 42% dei consensi, mentre la forza avversaria fatica a superare sull'18% dei voti espressi dagli elettori presenti."
	return Scene{
		ID:   "s1",
		Text: map[Language]string{"it": text},
		Annotations: &scriptpkg.SceneAnnotations{
			Version: 1, Language: "it",
			SecondaryEntities: []scriptpkg.AnnotatedEntity{
				{CanonicalName: "42%", Type: "PERCENT", Confidence: 0.9,
					Mentions: []scriptpkg.AnnotationSpan{{Text: "42%", StartRune: 24, EndRune: 27}}},
				{CanonicalName: "si attesta sull'18", Type: "NUMBER", Confidence: 0.7,
					Mentions: []scriptpkg.AnnotationSpan{{Text: "si attesta sull'18", StartRune: 0, EndRune: 1}}},
			},
		},
	}
}

func TestOverlaySceneInputRescuesLeadingProseNumber(t *testing.T) {
	scene := rescueScene()
	text := scene.Text["it"]
	// Fix the dirty mention span to the real offsets in the text above.
	for i := range scene.Annotations.SecondaryEntities {
		e := &scene.Annotations.SecondaryEntities[i]
		if e.CanonicalName == "si attesta sull'18" {
			// The scene text says "superare sull'18%": ground the mention there.
			runes := []rune(text)
			needle := []rune("sull'18")
			start := -1
			for j := 0; j+len(needle) <= len(runes); j++ {
				if string(runes[j:j+len(needle)]) == "sull'18" {
					start = j
					break
				}
			}
			require.NotEqual(t, -1, start, "mention must ground in scene text")
			e.Mentions = []scriptpkg.AnnotationSpan{{Text: "sull'18", StartRune: start, EndRune: start + len(needle)}}
			// The canonical name keeps the dirty NLP surface; only the
			// mention is re-grounded to the scene's own wording.
			e.CanonicalName = "superare sull'18"
		}
	}
	occurrences := []capabilityentities.EntityOccurrence{
		{EntityID: capabilityentities.StableEntityID("PERCENT", "42%"), Name: "42%", Type: "PERCENT",
			SceneID: "s1", AudioStartUS: 3_550_000, AudioEndUS: 4_525_000},
		{EntityID: capabilityentities.StableEntityID("NUMBER", "superare sull'18"), Name: "superare sull'18", Type: "NUMBER",
			SceneID: "s1", AudioStartUS: 7_762_500, AudioEndUS: 8_850_000},
	}
	// Empty word timing: TTS speaks digits as words, so locate() of the
	// cleaned digit surface fails and the occurrence window must win.
	var timing capabilityaudio.SpeechTimingArtifact
	in, err := overlaySceneInput(scene, "it", "it", timing, 0, occurrences, nil)
	require.NoError(t, err)
	require.NotNil(t, in)
	texts := map[string]capabilityoverlay.TimedAnnotation{}
	for _, n := range in.Numbers {
		texts[n.Text] = n
	}
	require.Contains(t, texts, "42%")
	rescued, ok := texts["18%"]
	require.True(t, ok, "leading-prose number rescued with %% suffix, got %v", in.Numbers)
	assert.Equal(t, int64(7_762_500), rescued.StartUS)
	assert.Equal(t, int64(8_850_000-7_762_500), rescued.DurationUS)
}

// Live M1 regression: VisualNER mirrors the dirty NLP surface ("dove si
// registra solamente l'18") and the strict parser refused it, so a scene
// with two spoken percentages produced no chart at all. The candidate must
// be cleaned to "18%" (unit recovered from the scene text) and authorize the
// grounded number.
func TestMergeChartValuesRescuesDirtyCandidateSurface(t *testing.T) {
	text := "Il consenso raggiunge il 42% dei voti, mentre nelle zone rurali si registra solamente l'18%."
	got := mergeChartValues(nil, []ChartValueCandidate{
		{Text: "42%", Value: 42},
		{Text: "dove si registra solamente l'18", Value: 18},
	}, text)
	require.Equal(t, []string{"42%", "18%"}, got, "dirty candidate rescued to its verbatim surface")

	// A value the worker did not carry, or a surface absent from the scene,
	// is still rejected — no invention, no coercion.
	assert.Empty(t, mergeChartValues(nil, []ChartValueCandidate{{Text: "l'18", Value: 99}}, text))
	assert.Empty(t, mergeChartValues(nil, []ChartValueCandidate{{Text: "qualitative", Value: 3}}, text))
	assert.Empty(t, mergeChartValues(nil, []ChartValueCandidate{{Text: "42%", Value: 42}}, "nessun numero qui"))
}

func TestApplyRustChartValuesDedupesTwinAnnotations(t *testing.T) {
	mk := func(text string, startUS int64) capabilityoverlay.TimedAnnotation {
		return capabilityoverlay.TimedAnnotation{Text: text, StartUS: startUS, DurationUS: 975_000, Score: 0.9}
	}
	input := &capabilityoverlay.SceneInput{ID: "s1", Numbers: []capabilityoverlay.TimedAnnotation{
		mk("42", 3_550_000), mk("42%", 3_550_000), mk("18%", 7_762_500),
	}}
	applyRustChartValues(input, []scriptpkg.SceneHighlight{{
		SceneID: "s1", ChartValues: []string{"42%", "18%"},
	}}, "s1", true)
	require.Len(t, input.ChartNumbers, 2, "twin bars for one spoken fact collapse")
	got := map[string]bool{}
	for _, n := range input.ChartNumbers {
		got[n.Text] = true
	}
	assert.True(t, got["42%"], "unit-carrying surface wins, got %v", input.ChartNumbers)
	assert.True(t, got["18%"], "got %v", input.ChartNumbers)
}
