// Package scripts_test — generation_html_test.go exercises
// BuildGenerationDocumentHTML (PR 3).
//
// The renderer takes the canonical typed ModelScriptOutputV1 +
// EntityResult + VideoMetadata slices and emits an HTML body that
// covers (title, full text, scenes with bindings, entities, video
// metadata). The tests are smoke tests: assert the expected
// strings appear in the rendered HTML.
package adapters_test

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestBuildGenerationDocumentHTML_RendersTitleAndScenes is the
// canonical smoke test: a 2-scene model with bound image and
// voiceover produces an HTML body containing the title, prose,
// both scene texts, and both scene bindings (image URL +
// voiceover link).
func TestBuildGenerationDocumentHTML_RendersTitleAndScenes(t *testing.T) {
	t.Parallel()

	html := adapters.BuildGenerationDocumentHTML(
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
		false, // includeSpecSceneBlock — canonical production default
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

	html := adapters.BuildGenerationDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{SchemaVersion: 1, Text: "Body."},
		"Script",
		"en",
		&scriptpkg.EntityResult{
			Persons:  []scriptpkg.Entity{{Value: "Albert Einstein"}, {Value: "Marie Curie"}},
			Places:   []scriptpkg.Entity{{Value: "Paris"}, {Value: "Bern"}},
			Concepts: []scriptpkg.Entity{{Value: "relativity"}},
		},
		nil,
		false, // includeSpecSceneBlock — canonical production default
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

	html := adapters.BuildGenerationDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{SchemaVersion: 1, Text: "Body."},
		"Script",
		"en",
		&scriptpkg.EntityResult{}, // empty
		nil,
		false, // includeSpecSceneBlock — canonical production default
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

	html := adapters.BuildGenerationDocumentHTML(
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
		false, // includeSpecSceneBlock — canonical production default
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
	html := adapters.BuildGenerationDocumentHTML(nil, "t", "en", nil, nil, false)
	if html != "" {
		t.Errorf("expected empty string for nil model, got %d chars", len(html))
	}
}

// TestBuildGenerationDocumentHTML_LocalisedChapterLabel verifies
// the language mapping: "it" → "Capitolo".
func TestBuildGenerationDocumentHTML_LocalisedChapterLabel(t *testing.T) {
	t.Parallel()

	html := adapters.BuildGenerationDocumentHTML(
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
		false, // includeSpecSceneBlock — canonical production default
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
	html := adapters.BuildGenerationDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{SchemaVersion: 1},
		"", "en", nil, nil,
		false, // includeSpecSceneBlock — canonical production default
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

// TestBuildGenerationDocumentHTML_IncludeSpecSceneBlockTrue_AppendsDebugBlock
// pins the FASE-document-canonical (July 2026) optional SpecScene JSON
// debug block: when includeSpecSceneBlock=true, the canonical renderer
// appends a `<h2>SpecScene JSON</h2><pre>{json}</pre>` block immediately
// before the closing </body></html> tag, surfacing the canonical
// SpecSceneOutput shape verbatim via json.MarshalIndent +
// html.EscapeString. The per-scene scenes-section rendering is
// unchanged on top of this addendum (canonical behaviour).
func TestBuildGenerationDocumentHTML_IncludeSpecSceneBlockTrue_AppendsDebugBlock(t *testing.T) {
	t.Parallel()

	html := adapters.BuildGenerationDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{
			SchemaVersion: 1,
			Text:          "Body prose.",
			SpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{
					{
						ID:    "scene-X",
						Index: 0,
						Kind:  scriptpkg.SceneImage,
						Text:  "Narration.",
						Bindings: scriptpkg.SceneBindings{
							Clip: &scriptpkg.ClipBinding{
								ClipID:    "drive-file-X",
								DriveLink: "https://drive.google.com/file/d/drive-file-X/view",
							},
						},
					},
				},
			},
		},
		"Debug Script",
		"en",
		nil, nil,
		true, // includeSpecSceneBlock — opt-in debug surface
	)

	// Canonical production sections still present.
	for _, want := range []string{
		"<h1>Debug Script</h1>",
		"<h2>Script</h2>",
		"Body prose.",
		"<h2>Scenes</h2>",
		"drive-file-X",
		"https://drive.google.com/file/d/drive-file-X/view",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected HTML to contain canonical section %q; not found", want)
		}
	}

	// Optional debug block emitted at the canonical position
	// (immediately before </body></html>).
	if !strings.Contains(html, "<h2>SpecScene JSON</h2><pre>") {
		t.Errorf("expected <h2>SpecScene JSON</h2><pre> debug block; HTML=%s", html)
	}
	// Inline-encoded JSON inside <pre>html-escaped</pre>: the inner
	// SpecScene JSONMarshalIndent ship with literal quote marks; the
	// html.EscapeString call converts them to &#34; — we assert on
	// the escaped form so the pin is faithful to the actual byte
	// stream emitted.
	if !strings.Contains(html, "&#34;drive_link&#34;") {
		t.Errorf("expected html-escaped drive_link JSON key inside debug block; HTML=%s", html)
	}
	// Position invariant: the debug block must come immediately
	// before the closing </body></html> tag (canonical placement).
	specIdx := strings.Index(html, "<h2>SpecScene JSON</h2><pre>")
	closeIdx := strings.Index(html, "</body></html>")
	if specIdx == -1 || closeIdx == -1 || specIdx >= closeIdx {
		t.Errorf("SpecScene JSON block must appear before </body></html>; specIdx=%d closeIdx=%d",
			specIdx, closeIdx)
	}
}

// TestBuildGenerationDocumentHTML_RendersTechnicalProvenance verifies that
// when a non-nil GenerationProvenance is supplied, the renderer emits both
// the hidden HTML comment and a visible "Technical Provenance" section
// containing the JSON-serialised provenance fields.
func TestBuildGenerationDocumentHTML_RendersTechnicalProvenance(t *testing.T) {
	t.Parallel()

	prov := &scriptpkg.GenerationProvenance{
		SourceType:     "text",
		SourceTextHash: "sha256:abc123",
		ClipIDs:        []string{"clip-1", "clip-2"},
		RequestedMode:  "clip_native",
		UsedMode:       "prose",
		FallbackUsed:   true,
		Model:          "llama3:70b",
		PromptVersion:  "v2.1",
		PlannerVersion: "v1.0",
		DocID:          "doc-123",
		DocLink:        "https://docs.google.com/document/d/doc-123/edit",
	}

	html := adapters.BuildGenerationDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{SchemaVersion: 1, Text: "Body."},
		"Script",
		"en",
		nil, nil,
		false,
		prov,
	)

	for _, want := range []string{
		"<!-- PIPELINEGEN-PROVENANCE:",
		"<h2>Technical Provenance</h2>",
		"<pre>",
		"sha256:abc123",
		"clip-1",
		"clip-2",
		"clip_native",
		"prose",
		"llama3:70b",
		"v2.1",
		"doc-123",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected HTML to contain %q; not found", want)
		}
	}
}

// TestBuildGenerationDocumentHTML_IncludeSpecSceneBlockFalse_DoesNotEmitDebugBlock
// pins the canonical production default: with includeSpecSceneBlock
// =false, no <h2>SpecScene JSON</h2><pre>...</pre> debug block is
// emitted. The canonical renderer stays a pristine typed renderer
// (production pristine — only the human-readable prose sections).
func TestBuildGenerationDocumentHTML_IncludeSpecSceneBlockFalse_DoesNotEmitDebugBlock(t *testing.T) {
	t.Parallel()

	html := adapters.BuildGenerationDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{
			SchemaVersion: 1,
			Text:          "Body prose.",
			SpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{
					{ID: "scene-X", Index: 0, Kind: scriptpkg.SceneImage, Text: "Narration."},
				},
			},
		},
		"Canonical Doc",
		"en",
		nil, nil,
		false, // includeSpecSceneBlock — canonical production default
	)

	// Canonical sections are present.
	for _, want := range []string{
		"<h1>Canonical Doc</h1>",
		"<h2>Scenes</h2>",
		"Narration.",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected HTML to contain %q; not found", want)
		}
	}

	// The optional debug block MUST NOT appear in canonical
	// production output.
	if strings.Contains(html, "<h2>SpecScene JSON</h2>") {
		t.Errorf("expected no <h2>SpecScene JSON</h2> debug block in canonical production render; HTML=%s", html)
	}
}
