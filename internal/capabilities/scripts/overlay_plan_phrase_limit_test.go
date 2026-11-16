// Package scriptgeneration — overlay_plan_phrase_limit_test.go certifies that
// the caller-selected run-level grounded-phrase ceiling
// (request max_phrase_overlays) reaches the persisted phrase budget.
package scriptgeneration

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// phraseCeilingFixture builds one scene with eight three-word grounded phrases,
// each anchored to certified word timing (100ms per word, 24 words).
func phraseCeilingFixture(t *testing.T) *GenerateResult {
	t.Helper()
	words := []string{"alpha", "one", "bravo", "two", "charlie", "three", "delta", "four", "echo", "five", "foxtrot", "six", "golf", "seven", "hotel", "eight", "india", "juliet", "kilo", "lima", "mike", "november", "oscar", "papa"}
	wordTimings := make([]capabilityaudio.SpeechWordTiming, len(words))
	for i, word := range words {
		wordTimings[i] = capabilityaudio.SpeechWordTiming{
			Index: i, Text: word,
			StartUS: int64(i) * 100_000, EndUS: int64(i+1) * 100_000,
		}
	}
	timing := capabilityaudio.SpeechTimingArtifact{
		Version:      capabilityaudio.SpeechTimingVersion,
		Provider:     "edge_tts",
		BoundaryMode: capabilityaudio.BoundaryWord,
		Language:     "en",
		TextSHA256:   "ceiling-text",
		AudioSHA256:  "ceiling-audio",
		DurationUS:   int64(len(words)) * 100_000,
		Words:        wordTimings,
	}
	phrases := make([]scriptpkg.AnnotationSpan, 0, 8)
	for i := 0; i < 8; i++ {
		phrases = append(phrases, scriptpkg.AnnotationSpan{
			Text:  fmt.Sprintf("%s %s %s", words[i*3], words[i*3+1], words[i*3+2]),
			Score: 0.9 - float64(i)*0.1,
		})
	}
	text := ""
	for i, word := range words {
		if i > 0 {
			text += " "
		}
		text += word
	}
	return &GenerateResult{
		Scenes: []Scene{{
			ID:          "scene-0",
			Index:       0,
			Text:        map[Language]string{"en": text},
			Voiceover:   map[Language]AudioReference{"en": {ID: "vo-scene-0-en", Duration: 2.4, Timing: &timing}},
			Annotations: &scriptpkg.SceneAnnotations{Version: 1, Language: "en", Status: "completed", ImportantPhrases: phrases},
		}},
		ResolvedScenes: []ResolvedScene{{ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 2_400_000}},
	}
}

// TestCompileResultOverlayPlanBudgetEchoesCallerCeiling certifies the reported
// phrase budget is measured against the caller's ceiling, not the default.
func TestCompileResultOverlayPlanBudgetEchoesCallerCeiling(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{
		{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "A scene without certified timing."}},
	}}
	canvas := GoldenOverlayCanvas
	canvas.MaxPhraseOverlays = capabilityoverlay.MaxPhraseOverlaysHardLimit + 10
	require.NoError(t, compileResultOverlayPlan(context.Background(), result, "en", "plan-budget", "project-budget", "", canvas, nil, nil))
	require.Nil(t, result.OverlayPlan)
	require.NotNil(t, result.PhraseOverlayBudget)
	require.Equal(t, capabilityoverlay.PhraseOverlayBudget{
		Requested: capabilityoverlay.MaxPhraseOverlaysHardLimit,
		Shortfall: capabilityoverlay.MaxPhraseOverlaysHardLimit,
	}, *result.PhraseOverlayBudget)
}
