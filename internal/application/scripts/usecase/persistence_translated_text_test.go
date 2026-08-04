// persistence_translated_text_test.go — TDD regression guard for
// Bug 3: buildGenerationResult MUST prefer postResult.TranslatedText
// over engineResult.Output.Text when the TranslationProcessor
// succeeded. Without this fix, the API response always showed the
// original (e.g. English) text even when translation was completed.
//
// PR-TRANSLATION-PIPELINE-2026-07-09.
package usecase

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestBuildGenerationResult_SerializesMetadataArtifactsAsJSON(t *testing.T) {
	engineResult := &EngineResult{
		Output: scriptpkg.ModelScriptOutputV1{
			Text:      "Generated script text.",
			SpecScene: scriptpkg.SpecSceneOutput{Version: 1},
		},
		WordCount: 3,
	}
	postResult := &adapters.PipelineResult{
		VideoMetadata: []scriptpkg.VideoMetadata{{
			Language:    "it",
			Title:       "Titolo manuale",
			Description: "Descrizione manuale",
			Tags:        []string{"boxe", "analisi"},
		}},
	}

	result := buildGenerationResult(
		scriptpkg.GenerationItemV2{ID: "metadata-json-item"},
		scriptpkg.ResolvedGenerationPlan{Title: "Internal title", Language: "it"},
		engineResult,
		postResult,
		scriptpkg.GenerationTimings{},
	)

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal GenerationResult: %v", err)
	}

	var wire struct {
		Artifacts struct {
			Metadata []scriptpkg.VideoMetadata `json:"metadata"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal GenerationResult JSON: %v", err)
	}
	if len(wire.Artifacts.Metadata) != 1 {
		t.Fatalf("JSON artifacts.metadata length = %d, want 1; JSON=%s", len(wire.Artifacts.Metadata), raw)
	}
	metadata := wire.Artifacts.Metadata[0]
	if metadata.Language != "it" || metadata.Title != "Titolo manuale" ||
		metadata.Description != "Descrizione manuale" {
		t.Fatalf("JSON metadata = %#v, want manual values; JSON=%s", metadata, raw)
	}
	if len(metadata.Tags) != 2 || metadata.Tags[0] != "boxe" || metadata.Tags[1] != "analisi" {
		t.Fatalf("JSON metadata tags = %v, want [boxe analisi]; JSON=%s", metadata.Tags, raw)
	}
}

func TestBuildGenerationResult_PrefersTranslatedOutput_StandaloneCases(t *testing.T) {
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

func TestBuildGenerationResult_StripsVoiceoverLocalPathAndKeepsImages(t *testing.T) {
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
		FinalSpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-1",
					Index: 0,
					Text:  "Original English scene.",
					Bindings: scriptpkg.SceneBindings{
						Voiceover: &scriptpkg.VoiceoverBinding{
							Status:    "completed",
							Link:      "https://drive.google.com/file/d/voice-1/view",
							LocalPath: "/tmp/voice-1.mp3",
						},
					},
				},
			},
		},
		Scenes: []adapters.SceneImage{
			{
				Index: 0,
				URL:   "https://drive.google.com/file/d/image-1/view",
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

	if result.Output.SpecScene.Scenes[0].Bindings.Voiceover == nil {
		t.Fatal("expected voiceover binding in final result")
	}
	if got := result.Output.SpecScene.Scenes[0].Bindings.Voiceover.LocalPath; got != "" {
		t.Fatalf("expected voiceover local_path to be stripped from API result, got %q", got)
	}
	if got := result.Output.SpecScene.Scenes[0].Bindings.Voiceover.Link; got != "https://drive.google.com/file/d/voice-1/view" {
		t.Fatalf("expected voiceover link to survive in API result, got %q", got)
	}
	if result.Output.SpecScene.Scenes[0].Bindings.Image == nil {
		t.Fatal("expected image binding in final result")
	}
	if got := result.Output.SpecScene.Scenes[0].Bindings.Image.LocalPath; got != "" {
		t.Fatalf("expected image local_path to be stripped from API result, got %q", got)
	}
	if got := result.Output.SpecScene.Scenes[0].Bindings.Image.URL; got != "https://drive.google.com/file/d/image-1/view" {
		t.Fatalf("expected image url to survive in API result, got %q", got)
	}
	if got := result.Output.SpecScene.Scenes[0].Bindings.Image.Status; got != "generated" {
		t.Fatalf("expected image status generated in API result, got %q", got)
	}
}
