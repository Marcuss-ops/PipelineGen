// persistence_translated_text_test.go — TDD regression guard for
// Bug 3: buildGenerationResult MUST prefer postResult.TranslatedText
// over engineResult.Output.Text when the TranslationProcessor
// succeeded. Without this fix, the API response always showed the
// original (e.g. English) text even when translation was completed.
//
// PR-TRANSLATION-PIPELINE-2026-07-09.
package usecase

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestBuildGenerationResult_PrefersTranslatedOutput(t *testing.T) {
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{
			Text: "Original English text.",
			SpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{
					{ID: "scene-1", Index: 0, Text: "Original English scene."},
				},
			},
		},
		WordCount: 3,
	}

	postResult := &adapters.PipelineResult{
		TranslatedText: "Testo italiano tradotto.",
		TranslatedSpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{ID: "scene-1", Index: 0, Text: "Scena italiana tradotta."},
			},
		},
	}

	item := scriptpkg.GenerationItemV2{ID: "test-item"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Title:    "Test",
		Language: "en",
	}
	timings := scriptpkg.GenerationTimings{}

	result := buildGenerationResult(item, plan, engineResult, postResult, timings)

	if result.Output.Text != "Testo italiano tradotto." {
		t.Errorf("expected translated text in Output.Text, got %q", result.Output.Text)
	}
}

func TestBuildGenerationResult_FallsBackToOriginalWhenNoTranslation(t *testing.T) {
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{
			Text: "Original English text.",
			SpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{
					{ID: "scene-1", Index: 0, Text: "Original English scene."},
				},
			},
		},
		WordCount: 3,
	}

	// No translation — postResult has empty TranslatedText
	postResult := &adapters.PipelineResult{}

	item := scriptpkg.GenerationItemV2{ID: "test-item"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Title:    "Test",
		Language: "en",
	}
	timings := scriptpkg.GenerationTimings{}

	result := buildGenerationResult(item, plan, engineResult, postResult, timings)

	if result.Output.Text != "Original English text." {
		t.Errorf("expected original text when no translation, got %q", result.Output.Text)
	}
}

func TestBuildGenerationResult_FallsBackToOriginalWhenPostResultNil(t *testing.T) {
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{
			Text: "Original text.",
			SpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{
					{ID: "scene-1", Index: 0, Text: "Original scene."},
				},
			},
		},
		WordCount: 2,
	}

	item := scriptpkg.GenerationItemV2{ID: "test-item"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Title:    "Test",
		Language: "en",
	}
	timings := scriptpkg.GenerationTimings{}

	// nil postResult — should not panic
	result := buildGenerationResult(item, plan, engineResult, nil, timings)

	if result.Output.Text != "Original text." {
		t.Errorf("expected original text when postResult is nil, got %q", result.Output.Text)
	}
}
