// Package scriptgeneration — runner_unit_test.go covers pure helper-level tests.
package scriptgeneration

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeriveErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		stage    Stage
		wantCode string
	}{
		{"nil error", nil, StageGeneratingSceneText, "GENERATING_SCENE_TEXT_FAILED"},
		{"timeout error", errors.New("context deadline exceeded"), StageTranslatingScenes, "PROVIDER_TIMEOUT"},
		{"unavailable provider", errors.New("connection refused"), StageGeneratingVoiceovers, "PROVIDER_UNAVAILABLE"},
		{"empty result", errors.New("generate scene text returned zero scenes"), StageGeneratingSceneText, "EMPTY_RESULT"},
		{"text generation failed", fmt.Errorf("generate scene text failed: %w", errors.New("ollama error")), StageGeneratingSceneText, "TEXT_GENERATION_FAILED"},
		{"translation failed", errors.New("translate scene scene-2 to es failed: model returned gibberish"), StageTranslatingScenes, "TRANSLATION_FAILED"},
		{"voiceover failed", errors.New("voiceover generation for scene scene-1 lang en failed: TTS error"), StageGeneratingVoiceovers, "VOICEOVER_FAILED"},
		{"document failed", errors.New("upsert document for language es failed: document content rejected"), StagePublishingDocuments, "DOCUMENT_FAILED"},
		{"generic fallback", errors.New("something unexpected happened"), StagePublishingDocuments, "PUBLISHING_DOCUMENTS_FAILED"},
		{"incomplete render set", errors.New("INCOMPLETE_RENDER_SET: expected=20 successful=19 failed=1"), StagePublishingDocuments, "INCOMPLETE_RENDER_SET"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantCode, deriveErrorCode(tt.err, tt.stage))
		})
	}
}

func TestBindExplicitClipSceneTextPreservesGeneratedNarration(t *testing.T) {
	req := GenerateRequest{Source: Source{Type: SourceClips, ClipIDs: []string{"c1", "c2"}, SourceText: "SCENE 1: source fact one\nSCENE 2: source fact two"}, SourceLanguage: "en"}
	scenes := []Scene{{Text: map[Language]string{"en": "A new funny line"}}, {Text: map[Language]string{"en": "Another new funny line"}}}
	bindExplicitClipSceneText(req, scenes)
	assert.Equal(t, "A new funny line", scenes[0].Text["en"])
	assert.Equal(t, "Another new funny line", scenes[1].Text["en"])
}

func TestModelScriptOutputForDocumentPreservesCanonicalSceneData(t *testing.T) {
	scenes := []Scene{{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Hello world", "es": "Hola mundo"}}, {ID: "scene-1", Index: 1, Text: map[Language]string{"en": "Second scene", "es": "Segunda escena"}}}
	enModel := modelScriptOutputForDocument(&GenerateResult{Scenes: scenes}, "en")
	assert.Equal(t, "Hello world", enModel.SpecScene.Scenes[0].Text)
	assert.Equal(t, "Second scene", enModel.SpecScene.Scenes[1].Text)
	esModel := modelScriptOutputForDocument(&GenerateResult{Scenes: scenes}, "es")
	assert.Equal(t, "Hola mundo", esModel.SpecScene.Scenes[0].Text)
	assert.Equal(t, "Segunda escena", esModel.SpecScene.Scenes[1].Text)
}

func TestModelScriptOutputForDocumentPreservesTechnicalBindingsForJSON(t *testing.T) {
	scenes := []Scene{{Index: 0, Text: map[Language]string{"en": "First"}, Clip: &ClipReference{DriveLink: "https://drive.google.com/CLIP-SECRET"}, Clips: []*ClipReference{{DriveLink: "https://drive.google.com/CLIP-A"}, {DriveLink: "https://drive.google.com/CLIP-B"}}, Voiceover: map[Language]AudioReference{"en": {URL: "https://drive.google.com/VOICE-EN"}}}}
	model := modelScriptOutputForDocument(&GenerateResult{Scenes: scenes}, "en")
	bindings := model.SpecScene.Scenes[0].Bindings
	assert.Equal(t, "https://drive.google.com/CLIP-SECRET", bindings.Clip.DriveLink)
	assert.Len(t, bindings.Clips, 2)
	assert.Equal(t, "https://drive.google.com/VOICE-EN", bindings.Voiceover.Links["en"])
}

func TestContainsAny(t *testing.T) {
	assert.True(t, containsAny("hello world", "world"))
	assert.True(t, containsAny("timeout error", "timeout", "deadline"))
	assert.False(t, containsAny("hello world", "foo"))
	assert.False(t, containsAny("", "foo"))
}
