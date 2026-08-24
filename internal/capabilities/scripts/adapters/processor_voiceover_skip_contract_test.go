package adapters

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

func TestVoiceoverProcessor_TranslationSkipProducesOneOutcomePerScene(t *testing.T) {
	stub := &stubItemExecutor{}
	processor := NewVoiceoverProcessor(stub, zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID: "translation-skip-scenes", Title: "Skip", Language: "en", TranslateTo: "it",
	}
	result, err := processor.Process(context.Background(), plan, ProcessInput{
		EffectiveLanguage: "en",
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "First scene", Kind: scriptpkg.SceneNarration},
			{ID: "scene-1", Index: 1, Text: "Second scene", Kind: scriptpkg.SceneNarration},
		}},
	})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if len(result.Voiceovers) != 2 {
		t.Fatalf("voiceover outcomes = %d, want one per scene", len(result.Voiceovers))
	}
	for i, outcome := range result.Voiceovers {
		if outcome.SceneIndex != i || outcome.Language != "it" || outcome.Status != "skipped" {
			t.Fatalf("outcome[%d] = %#v, want scene index=%d language=it status=skipped", i, outcome, i)
		}
	}
	if stub.calls.Load() != 0 {
		t.Fatalf("executor calls = %d, want 0 when requested translation is unavailable", stub.calls.Load())
	}
}
