// Package scripts_test — generation_html_test.go exercises
// BuildGenerationDocumentHTML (PR 3).
//
// The renderer takes the canonical typed ModelScriptOutputV1 +
// EntityResult + VideoMetadata slices and emits an HTML body that
// covers (title, full text, scenes with bindings, entities, video
// metadata). The tests are smoke tests: assert the expected
// strings appear in the rendered HTML.
package scripts_test

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestBuildGenerationDocumentHTML_RendersTitleAndScenes is the
// canonical smoke test: a 2-scene model with bound image and
// voiceover produces an HTML body containing the title, prose,
// both scene texts, and both scene bindings (image URL +
// voiceover link).
func TestBuildGenerationDocumentHTML_RendersTitleAndScenes(t *testing.T) {
	t.Parallel()

	html := scripts.BuildGenerationDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{
			SchemaVersion: 1,
			Text:          "Scene one prose.\n\nScene two prose.",
			SpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{
					{
						ID:    "scene-1",
						Index: 0,
						Text:  "First narration.",
						Kind:  scriptpkg.SceneImage,
						Bindings: scriptpkg.SceneBindings{
							Image: &scriptpkg.ImageBinding{
								URL:    "http://img1.jpg",
								Status: "generated",
							},
							Voiceover: &scriptpkg.VoiceoverBinding{
								Status: "completed",
								Link:   "http://vo1.mp3",
							},
						},
					},
					{
						ID:    "scene-2",
						Index: 1,
						Text:  "Second narration.",
						Kind:  scriptpkg.SceneImage,
						Bindings: scriptpkg.SceneBindings{
							Image: &scriptpkg.ImageBinding{
								URL:    "http://img2.jpg",
								Status: "generated",
							},
						},
					},
				},
			},
		},
		"My Script",
		"en",
		nil,
		nil,
	)

	if html == "" {
		t.Fatal("expected non-empty HTML")
	}
	for _, want := range []string{
		"<h1>My Script</h1>",
		"Scene one prose.",
		"Scene two prose.",
		"First narration.",
		"Second narration.",
		"http://img1.jpg",
		"http://img2.jpg",
		"http://vo1.mp3",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected HTML to contain %q; not found", want)
		}
	}
}

// TestBuildGenerationDocumentHTML_RendersEntities verifies the
// Entities section renders Persons, Places, Concepts separately
// and that an empty EntityResult produces no Entities header.
func TestBuildGenerationDocumentHTML_RendersEntities(t *testing.T) {
	t.Parallel()

	html := scripts.BuildGenerationDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{SchemaVersion: 1, Text: "Body."},
		"Script",
		"en",
		&scriptpkg.EntityResult{
			Persons:  []scriptpkg.Entity{{Value: "Albert Einstein"}, {Value: "Marie Curie"}},
			Places:   []scriptpkg.Entity{{Value: "Paris"}, {Value: "Bern"}},
			Concepts: []scriptpkg.Entity{{Value: "relativity"}},
		},
		nil,
	)

	for _, want := range []string{
		"<h2>Entities</h2>",
		"<h3>Persons</h3>",
		"Albert Einstein",
		"Marie Curie",
		"<h3>Places</h3>",
		"Paris",
		"Bern",
		"<h3>Concepts</h3>",
		"relativity",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected HTML to contain %q; not found", want)
		}
	}
}

// TestBuildGenerationDocumentHTML_NoEntities_NoHeader asserts
// that an entirely empty EntityResult does NOT emit the
// <h2>Entities</h2> header.
func TestBuildGenerationDocumentHTML_NoEntities_NoHeader(t *testing.T) {
	t.Parallel()

	html := scripts.BuildGenerationDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{SchemaVersion: 1, Text: "Body."},
		"Script",
		"en",
		&scriptpkg.EntityResult{}, // empty
		nil,
	)
	if strings.Contains(html, "<h2>Entities</h2>") {
		t.Errorf("expected no Entities header when EntityResult is fully empty; got HTML=%s", html)
	}
}

// TestBuildGenerationDocumentHTML_RendersVideoMetadata verifies
// the Video Metadata section renders the per-language title +
// description + tags.
func TestBuildGenerationDocumentHTML_RendersVideoMetadata(t *testing.T) {
	t.Parallel()

	html := scripts.BuildGenerationDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{SchemaVersion: 1, Text: "Body."},
		"Script",
		"en",
		nil,
		[]scriptpkg.VideoMetadata{
			{
				Language:    "en",
				Title:       "English Title",
				Description: "English description.",
				Tags:        []string{"alpha", "beta"},
			},
			{
				Language: "it",
				Title:    "Titolo Italiano",
			},
		},
	)

	for _, want := range []string{
		"<h2>Video Metadata</h2>",
		"<h3>en</h3>",
		"English Title",
		"English description.",
		"alpha",
		"beta",
		"<h3>it</h3>",
		"Titolo Italiano",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected HTML to contain %q; not found", want)
		}
	}
}

// TestBuildGenerationDocumentHTML_NilModelReturnsEmpty pins the
// nil-model behaviour: empty string with a nil receiver.
func TestBuildGenerationDocumentHTML_NilModelReturnsEmpty(t *testing.T) {
	t.Parallel()
	html := scripts.BuildGenerationDocumentHTML(nil, "t", "en", nil, nil)
	if html != "" {
		t.Errorf("expected empty string for nil model, got %d chars", len(html))
	}
}

// TestBuildGenerationDocumentHTML_LocalisedChapterLabel verifies
// the language mapping: "it" → "Capitolo".
func TestBuildGenerationDocumentHTML_LocalisedChapterLabel(t *testing.T) {
	t.Parallel()

	html := scripts.BuildGenerationDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{
			SchemaVersion: 1,
			SpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{
					{ID: "scene-1", Index: 0, Kind: scriptpkg.SceneNarration},
				},
			},
		},
		"Script",
		"it",
		nil, nil,
	)
	if !strings.Contains(html, "Capitolo 1") {
		t.Errorf("expected Italian localised chapter label 'Capitolo 1'; HTML=%s", html)
	}
}

// TestBuildGenerationDocumentHTML_EmptyModelMinimal checks that
// a model with no Text and no Scenes still produces a tiny
// valid (empty body) HTML doc.
func TestBuildGenerationDocumentHTML_EmptyModelMinimal(t *testing.T) {
	t.Parallel()
	html := scripts.BuildGenerationDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{SchemaVersion: 1},
		"", "en", nil, nil,
	)
	if html == "" {
		t.Fatal("expected non-empty shell even for empty model")
	}
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("expected DOCTYPE in shell")
	}
	if !strings.Contains(html, "</body></html>") {
		t.Error("expected closing body+html tags")
	}
}
