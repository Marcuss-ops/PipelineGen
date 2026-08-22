// Package scriptgeneration — runner_unit_test.go covers the
// pure helper-level tests for the document model adapter, deriveErrorCode,
// and containsAny. None of these touch the repository or the
// runner pipeline — they exercise the runner.go private helpers
// directly so a regression in error-code mapping or doc-content
// assembly is caught without spinning up a full Execute loop.
//
// godlike/06 SSOT invariants asserted:
//
//   - deriveErrorCode returns a STABLE machine-readable code per
//     trigger pattern (PROVIDER_TIMEOUT, PROVIDER_UNAVAILABLE,
//     EMPTY_RESULT, TEXT_GENERATION_FAILED, TRANSLATION_FAILED,
//     VOICEOVER_FAILED, DOCUMENT_FAILED, ENQUEUE_FAILED) and
//     falls back to <STAGE>_FAILED for unknown shapes.
//   - modelScriptOutputForDocument preserves ordered scenes and
//     language-specific text/voiceover data for the canonical renderer.
//   - containsAny is a small substring-match helper used by
//     deriveErrorCode; mirrors the semantics of strings.Contains
//     over a variadic list.
package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

type unitFinalAssembler struct{}

func (unitFinalAssembler) AssembleFinalVideo(_ context.Context, inputs []string, output string) error {
	if len(inputs) != 2 {
		return fmt.Errorf("got %d inputs", len(inputs))
	}
	return os.WriteFile(output, []byte("final"), 0o600)
}

// TestDeriveErrorCode is the canonical table-driven test for the
// error-code mapping. New trigger patterns MUST add a fixture here
// AND a corresponding case in the runner.go switch.
func TestDeriveErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		stage    Stage
		wantCode string
	}{
		{
			name:     "nil error",
			err:      nil,
			stage:    StageGeneratingSceneText,
			wantCode: "GENERATING_SCENE_TEXT_FAILED",
		},
		{
			name:     "timeout error",
			err:      errors.New("context deadline exceeded"),
			stage:    StageTranslatingScenes,
			wantCode: "PROVIDER_TIMEOUT",
		},
		{
			name:     "unavailable provider",
			err:      errors.New("connection refused"),
			stage:    StageGeneratingVoiceovers,
			wantCode: "PROVIDER_UNAVAILABLE",
		},
		{
			name:     "empty result",
			err:      errors.New("generate scene text returned zero scenes"),
			stage:    StageGeneratingSceneText,
			wantCode: "EMPTY_RESULT",
		},
		{
			name:     "text generation failed",
			err:      fmt.Errorf("generate scene text failed: %w", errors.New("ollama error")),
			stage:    StageGeneratingSceneText,
			wantCode: "TEXT_GENERATION_FAILED",
		},
		{
			name:     "translation failed",
			err:      errors.New("translate scene scene-2 to es failed: model returned gibberish"),
			stage:    StageTranslatingScenes,
			wantCode: "TRANSLATION_FAILED",
		},
		{
			name:     "voiceover failed",
			err:      errors.New("voiceover generation for scene scene-1 lang en failed: TTS error"),
			stage:    StageGeneratingVoiceovers,
			wantCode: "VOICEOVER_FAILED",
		},
		{
			name:     "document failed",
			err:      errors.New("upsert document for language es failed: document content rejected"),
			stage:    StagePublishingDocuments,
			wantCode: "DOCUMENT_FAILED",
		},
		{
			name:     "generic fallback",
			err:      errors.New("something unexpected happened"),
			stage:    StagePublishingDocuments,
			wantCode: "PUBLISHING_DOCUMENTS_FAILED",
		},
		{
			name:     "incomplete render set",
			err:      errors.New("INCOMPLETE_RENDER_SET: expected=20 successful=19 failed=1"),
			stage:    StagePublishingDocuments,
			wantCode: "INCOMPLETE_RENDER_SET",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveErrorCode(tt.err, tt.stage)
			assert.Equal(t, tt.wantCode, got, "deriveErrorCode(%v, %s)", tt.err, tt.stage)
		})
	}
}

func TestBindExplicitClipSceneTextPreservesGeneratedNarration(t *testing.T) {
	req := GenerateRequest{
		Source:         Source{Type: SourceClips, ClipIDs: []string{"c1", "c2"}, SourceText: "SCENE 1: source fact one\nSCENE 2: source fact two"},
		SourceLanguage: "en",
	}
	scenes := []Scene{
		{Text: map[Language]string{"en": "A new funny line"}},
		{Text: map[Language]string{"en": "Another new funny line"}},
	}
	bindExplicitClipSceneText(req, scenes)
	assert.Equal(t, "A new funny line", scenes[0].Text["en"])
	assert.Equal(t, "Another new funny line", scenes[1].Text["en"])
}

func TestAssembleFinalVideoRequiresCompleteLocalInputs(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.mp4")
	second := filepath.Join(dir, "second.mp4")
	if err := os.WriteFile(first, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{finalVideoAssembler: unitFinalAssembler{}}
	result := &GenerateResult{ExpectedRenderCount: 2, LocalizedRenders: []LocalizedRenderResult{
		{SceneID: "s2", SceneIndex: 1, LocalPath: second},
		{SceneID: "s1", SceneIndex: 0, LocalPath: first},
	}}
	if err := runner.assembleFinalVideo(context.Background(), "unit", result); err != nil {
		t.Fatal(err)
	}
	if result.FinalVideo == nil || result.FinalVideo.SHA256 == "" {
		t.Fatal("final video was not recorded")
	}
}

func TestModelScriptOutputForDocumentPreservesCanonicalSceneData(t *testing.T) {
	scenes := []Scene{
		{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Hello world", "es": "Hola mundo"}},
		{ID: "scene-1", Index: 1, Text: map[Language]string{"en": "Second scene", "es": "Segunda escena"}},
	}

	enModel := modelScriptOutputForDocument(&GenerateResult{Scenes: scenes}, "en")
	assert.Equal(t, "Hello world", enModel.SpecScene.Scenes[0].Text)
	assert.Equal(t, "Second scene", enModel.SpecScene.Scenes[1].Text)
	esModel := modelScriptOutputForDocument(&GenerateResult{Scenes: scenes}, "es")
	assert.Equal(t, "Hola mundo", esModel.SpecScene.Scenes[0].Text)
	assert.Equal(t, "Segunda escena", esModel.SpecScene.Scenes[1].Text)
}

// TestBuildDocumentContentHumanOnly asserts the document surface never
// leaks technical bindings (clip links) and renders voiceover as a
// bare, copyable URL rather than a language-labelled technical line.
func TestModelScriptOutputForDocumentPreservesTechnicalBindingsForJSON(t *testing.T) {
	scenes := []Scene{
		{
			Index: 0,
			Text:  map[Language]string{"en": "First"},
			Clip:  &ClipReference{DriveLink: "https://drive.google.com/CLIP-SECRET"},
			Clips: []*ClipReference{
				{DriveLink: "https://drive.google.com/CLIP-A"},
				{DriveLink: "https://drive.google.com/CLIP-B"},
			},
			Voiceover: map[Language]AudioReference{
				"en": {URL: "https://drive.google.com/VOICE-EN"},
			},
		},
	}

	model := modelScriptOutputForDocument(&GenerateResult{Scenes: scenes}, "en")
	bindings := model.SpecScene.Scenes[0].Bindings
	assert.Equal(t, "https://drive.google.com/CLIP-SECRET", bindings.Clip.DriveLink)
	assert.Len(t, bindings.Clips, 2)
	assert.Equal(t, "https://drive.google.com/VOICE-EN", bindings.Voiceover.Links["en"])
}

// TestContainsAny asserts the small substring-match helper used by
// deriveErrorCode. Variadic substring list; any match → true.
func TestContainsAny(t *testing.T) {
	assert.True(t, containsAny("hello world", "world"), "should find 'world'")
	assert.True(t, containsAny("timeout error", "timeout", "deadline"), "should find 'timeout'")
	assert.False(t, containsAny("hello world", "foo"), "should not find 'foo'")
	assert.False(t, containsAny("", "foo"), "empty string should not match anything")
}
