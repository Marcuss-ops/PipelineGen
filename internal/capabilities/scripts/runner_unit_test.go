// Package scriptgeneration — runner_unit_test.go covers the
// pure helper-level tests for deriveErrorCode, buildDocumentContent,
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
//   - buildDocumentContent skips scenes that lack the requested
//     language; numbers scenes as Scene N+1 (1-based for display).
//   - containsAny is a small substring-match helper used by
//     deriveErrorCode; mirrors the semantics of strings.Contains
//     over a variadic list.
package scriptgeneration

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
			name:     "enqueue failed",
			err:      errors.New("enqueue render failed: worker queue full"),
			stage:    StageEnqueuingRender,
			wantCode: "ENQUEUE_FAILED",
		},
		{
			name:     "generic fallback",
			err:      errors.New("something unexpected happened"),
			stage:    StagePublishingDocuments,
			wantCode: "PUBLISHING_DOCUMENTS_FAILED",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveErrorCode(tt.err, tt.stage)
			assert.Equal(t, tt.wantCode, got, "deriveErrorCode(%v, %s)", tt.err, tt.stage)
		})
	}
}

// TestBuildDocumentContent asserts the canonical 1-based scene
// numbering + skip-on-empty language + double-newline scene
// separator. Mirrors the contract used by the runner's
// StagePublishingDocuments.
func TestBuildDocumentContent(t *testing.T) {
	scenes := []Scene{
		{Index: 0, Text: map[Language]string{"en": "Hello world", "es": "Hola mundo"}},
		{Index: 1, Text: map[Language]string{"en": "Second scene", "es": "Segunda escena"}},
		{Index: 2, Text: map[Language]string{}},
	}

	enContent := buildDocumentContent(scenes, "en")
	assert.Contains(t, enContent, "Scene 1")
	assert.Contains(t, enContent, "Hello world")
	assert.Contains(t, enContent, "Scene 2")
	assert.Contains(t, enContent, "Second scene")
	// Scene 3 has no EN text — should be skipped.
	assert.NotContains(t, enContent, "Scene 3")

	esContent := buildDocumentContent(scenes, "es")
	assert.Contains(t, esContent, "Hola mundo")
	assert.Contains(t, esContent, "Segunda escena")
}

// TestBuildDocumentContentHumanOnly asserts the document surface never
// leaks technical bindings (clip links) and renders voiceover as a
// bare, copyable URL rather than a language-labelled technical line.
func TestBuildDocumentContentHumanOnly(t *testing.T) {
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

	content := buildDocumentContent(scenes, "en")

	// Technical bindings must never leak into the human surface.
	assert.NotContains(t, content, "CLIP-SECRET")
	assert.NotContains(t, content, "CLIP-A")
	assert.NotContains(t, content, "CLIP-B")
	assert.NotContains(t, content, "Clip:")

	// Voiceover is a bare, copyable URL.
	assert.Contains(t, content, "Voiceover: https://drive.google.com/VOICE-EN")
	assert.NotContains(t, content, "Voiceover en:")
}

// TestContainsAny asserts the small substring-match helper used by
// deriveErrorCode. Variadic substring list; any match → true.
func TestContainsAny(t *testing.T) {
	assert.True(t, containsAny("hello world", "world"), "should find 'world'")
	assert.True(t, containsAny("timeout error", "timeout", "deadline"), "should find 'timeout'")
	assert.False(t, containsAny("hello world", "foo"), "should not find 'foo'")
	assert.False(t, containsAny("", "foo"), "empty string should not match anything")
}
