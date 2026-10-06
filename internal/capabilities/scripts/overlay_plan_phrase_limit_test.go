// Package scriptgeneration — overlay_plan_phrase_limit_test.go certifies that
// the caller-selected run-level grounded-phrase ceiling
// (request max_phrase_overlays) reaches the overlay budget: it rides the
// OverlayCanvasSpec into the planner and into the persisted phrase budget.
package scriptgeneration

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// phraseCeilingFixture builds one scene with eight two-word grounded phrases,
// each anchored to certified word timing (100ms per word, 16 words).
func phraseCeilingFixture(t *testing.T) *GenerateResult {
	t.Helper()
	words := []string{"alpha", "one", "bravo", "two", "charlie", "three", "delta", "four", "echo", "five", "foxtrot", "six", "golf", "seven", "hotel", "eight"}
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
			Text:  fmt.Sprintf("%s %s", words[i*2], words[i*2+1]),
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
			Voiceover:   map[Language]AudioReference{"en": {ID: "vo-scene-0-en", Duration: 1.6, Timing: &timing}},
			Annotations: &scriptpkg.SceneAnnotations{Version: 1, Language: "en", Status: "completed", ImportantPhrases: phrases},
		}},
		ResolvedScenes: []ResolvedScene{{ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 1_600_000}},
	}
}

func countPlanPhrases(plan *capabilityoverlay.OverlayPlan) int {
	if plan == nil {
		return 0
	}
	count := 0
	for _, item := range plan.Items {
		if item.Kind == "text_phrase" {
			count++
		}
	}
	return count
}

// TestCompileOverlayPlanHonoursCallerPhraseCeiling certifies the end-to-end
// plumbing: eight grounded phrases are available, the caller ceiling admits
// exactly that many, and an absent ceiling keeps the certified default.
func TestCompileOverlayPlanHonoursCallerPhraseCeiling(t *testing.T) {
	result := phraseCeilingFixture(t)

	canvas := GoldenOverlayCanvas
	canvas.MaxPhraseOverlays = 3
	plan, err := CompileOverlayPlan(result, "en", canvas, "plan-ceiling-3", "video-ceiling", "project-ceiling")
	require.NoError(t, err)
	require.NotNil(t, plan, "the scene must derive an OverlayPlan")
	require.Equal(t, 3, countPlanPhrases(plan), "caller ceiling 3 must cap the grounded phrases")

	// Absent ceiling (zero) keeps the certified default.
	plan, err = CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "plan-ceiling-default", "video-ceiling", "project-ceiling")
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.Equal(t, capabilityoverlay.MaxPhraseOverlaysPerRun, countPlanPhrases(plan), "absent ceiling must keep the certified default")

	// The hard maximum includes every candidate in this fixture (and is
	// greater than the old default), so the caller-selected value admits all.
	raised := GoldenOverlayCanvas
	raised.MaxPhraseOverlays = capabilityoverlay.MaxPhraseOverlaysHardLimit
	plan, err = CompileOverlayPlan(result, "en", raised, "plan-ceiling-hard-max", "video-ceiling", "project-ceiling")
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.Equal(t, 8, countPlanPhrases(plan), "the approved hard maximum must admit every grounded candidate")

	// Oversized caller requests are clamped by the same canonical budget
	// resolver used by the production planner.
	raised.MaxPhraseOverlays = capabilityoverlay.MaxPhraseOverlaysHardLimit + 10
	plan, err = CompileOverlayPlan(result, "en", raised, "plan-ceiling-clamped", "video-ceiling", "project-ceiling")
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.Equal(t, 8, countPlanPhrases(plan), "available grounded candidates remain intact after clamping")
}

// TestCompileResultOverlayPlanBudgetEchoesCallerCeiling certifies the reported
// phrase budget is measured against the caller's ceiling, not the default.
func TestCompileResultOverlayPlanBudgetEchoesCallerCeiling(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{
		{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "A scene without certified timing."}},
	}}
	canvas := GoldenOverlayCanvas
	canvas.MaxPhraseOverlays = capabilityoverlay.MaxPhraseOverlaysHardLimit + 10
	require.NoError(t, compileResultOverlayPlan(result, "en", "plan-budget", "project-budget", "", canvas, nil))
	require.Nil(t, result.OverlayPlan)
	require.NotNil(t, result.PhraseOverlayBudget)
	require.Equal(t, capabilityoverlay.PhraseOverlayBudget{
		Requested: capabilityoverlay.MaxPhraseOverlaysHardLimit,
		Shortfall: capabilityoverlay.MaxPhraseOverlaysHardLimit,
	}, *result.PhraseOverlayBudget)
}
