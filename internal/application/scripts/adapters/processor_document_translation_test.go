// Package adapters — processor_document_translation_test.go is the
// internal-package test surface (package adapters, not adapters_test)
// for the canonical BuildGenerationDocumentHTML renderer under
// translation-aware inputs.
//
// Per godlike/06 SSOT (one canonical owner per fact):
//   - BuildGenerationDocumentHTML lives ONLY at generation_html.go
//   - chapterLabel (the localised "Chapter" / "Capitolo" / "Capítulo"
//     / "Chapitre" / "Kapitel" mapper) lives ONLY at generation_html.go
//   - The 7 invariants in this test live ONLY at
//     processor_document_translation_test.go
//
// Per godlike/07 NO-FAKE-AVAILABILITY: 7 hermetic regression-guards
// locking the canonical wire shape produced by the renderer when
// language=it + language=es + includeSpecSceneBlock=true. Every
// invariant is falsifiable by construction: a future regression
// that drops the chapter-label localization, removes the
// SpecScene JSON block, or accidentally translates the JSON keys
// to Italian would surface as a test failure.
//
// Per godlike/07 minimum-blast-radius: zero production-code change.
// The existing BuildGenerationDocumentHTML already satisfies all 7
// invariants; this test file is purely additive.
package adapters

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// canonicalTwoSceneFixture returns a 2-scene EN script model with
// the canonical clip + image bindings. Used by the test to render
// the same fixture under 2 different languages (it + es) so the
// per-language invariants can be compared apples-to-apples.
func canonicalTwoSceneFixture() *scriptpkg.ModelScriptOutputV1 {
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Full script prose for the English source.\n\nSecond paragraph here.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-1",
					Index: 0,
					Text:  "First narration segment.",
					Title: "Opening",
					Kind:  scriptpkg.SceneClip,
					Bindings: scriptpkg.SceneBindings{
						Clip: &scriptpkg.ClipBinding{
							ClipID:    "clip-1",
							ClipTitle: "EN clip clip-1",
							DriveLink: "https://drive.google.com/file/d/abcdef1234567890/view",
							StartMs:   1000,
							EndMs:     5000,
						},
						Image: &scriptpkg.ImageBinding{
							ImageID: "img-1",
							Prompt:  "EN prompt for scene 1",
							URL:     "https://storage.example.com/img-1.png",
							Status:  "generated",
						},
					},
				},
				{
					ID:    "scene-2",
					Index: 1,
					Text:  "Second narration segment.",
					Title: "Middle",
					Kind:  scriptpkg.SceneClip,
					Bindings: scriptpkg.SceneBindings{
						Clip: &scriptpkg.ClipBinding{
							ClipID:    "clip-2",
							ClipTitle: "EN clip clip-2",
							DriveLink: "https://drive.google.com/file/d/ghijkl0987654321/view",
							StartMs:   11000,
							EndMs:     15000,
						},
					},
				},
			},
		},
	}
}

// TestTranslatedScript_CreatesGoogleDocWithSpecSceneBlock pins the
// 7 invariants from the SCRIPT-TRANSLATION-TESTING-2026-08-08 action
// plan §3.PR-4 user-spec on the canonical BuildGenerationDocumentHTML
// output for the translated-script happy path. The fixture is
// rendered under 2 different languages (it + es) so the per-language
// invariants can be verified apples-to-apples.
func TestTranslatedScript_CreatesGoogleDocWithSpecSceneBlock(t *testing.T) {
	t.Parallel()

	model := canonicalTwoSceneFixture()

	// Render the same fixture under 2 different languages with
	// includeSpecSceneBlock=true (the canonical production path
	// for translated Google Docs). The two HTML outputs differ
	// ONLY in the chapter-label localization ("Capitolo" vs
	// "Capítulo") and the per-scene heading text — all other
	// canonical wire-shape sections are language-invariant.
	htmlIT := BuildGenerationDocumentHTML(model, "Translated Script", "it", nil, nil, true)
	htmlES := BuildGenerationDocumentHTML(model, "Translated Script", "es", nil, nil, true)

	// ── Invariant 1: <h2>Script</h2> section header present ──
	// The renderer always emits the Script section header when
	// the model has non-empty Text (per generation_html.go
	// `if t := strings.TrimSpace(model.Text); t != ""` guard).
	if !strings.Contains(htmlIT, "<h2>Script</h2>") {
		t.Errorf("invariant 1 (it): expected <h2>Script</h2> section header; not found")
	}
	if !strings.Contains(htmlES, "<h2>Script</h2>") {
		t.Errorf("invariant 1 (es): expected <h2>Script</h2> section header; not found")
	}

	// ── Invariant 2: <h2>Scenes</h2> section header present ──
	// The renderer always emits the Scenes section header when
	// the model has at least 1 scene (per generation_html.go
	// `if len(model.SpecScene.Scenes) > 0` guard). The 2-scene
	// fixture satisfies this guard.
	if !strings.Contains(htmlIT, "<h2>Scenes</h2>") {
		t.Errorf("invariant 2 (it): expected <h2>Scenes</h2> section header; not found")
	}
	if !strings.Contains(htmlES, "<h2>Scenes</h2>") {
		t.Errorf("invariant 2 (es): expected <h2>Scenes</h2> section header; not found")
	}

	// ── Invariant 3: <h2>SpecScene JSON</h2> debug block present ──
	// The renderer emits the SpecScene JSON debug block ONLY when
	// includeSpecSceneBlock=true (the canonical production
	// path for translated docs per processor_document.go).
	if !strings.Contains(htmlIT, "<h2>SpecScene JSON</h2>") {
		t.Errorf("invariant 3 (it): expected <h2>SpecScene JSON</h2> debug block (includeSpecSceneBlock=true); not found")
	}
	if !strings.Contains(htmlES, "<h2>SpecScene JSON</h2>") {
		t.Errorf("invariant 3 (es): expected <h2>SpecScene JSON</h2> debug block (includeSpecSceneBlock=true); not found")
	}

	// ── Invariant 4: "Capitolo" present when Language=it ──
	// The renderer localises the per-scene chapter label via the
	// chapterLabel(language) function: "it" → "Capitolo", "es"
	// → "Capítulo", "fr" → "Chapitre", "de" → "Kapitel",
	// default → "Chapter". The "Capitolo" literal must appear in
	// the <h3> per-scene heading when language=it, and "Capítulo"
	// must NOT appear (single-language canonical output).
	if !strings.Contains(htmlIT, "Capitolo") {
		t.Errorf("invariant 4 (it): expected 'Capitolo' chapter label (language=it); not found")
	}
	if strings.Contains(htmlIT, "Capítulo") {
		t.Errorf("invariant 4 (it): unexpected 'Capítulo' in Italian render (language should map to one localized label, not both)")
	}

	// ── Invariant 5: clip.drive_link URL present ──
	// The renderer renders each scene's Clip.DriveLink as an
	// <a href="...">link</a> inline annotation. Both fixture
	// DriveLinks must appear in both HTML outputs (language-
	// invariant canonical wire shape).
	if !strings.Contains(htmlIT, "https://drive.google.com/file/d/abcdef1234567890/view") {
		t.Errorf("invariant 5 (it): expected scene[0] clip.drive_link URL in Italian render; not found")
	}
	if !strings.Contains(htmlIT, "https://drive.google.com/file/d/ghijkl0987654321/view") {
		t.Errorf("invariant 5 (it): expected scene[1] clip.drive_link URL in Italian render; not found")
	}
	if !strings.Contains(htmlES, "https://drive.google.com/file/d/abcdef1234567890/view") {
		t.Errorf("invariant 5 (es): expected scene[0] clip.drive_link URL in Spanish render; not found")
	}
	if !strings.Contains(htmlES, "https://drive.google.com/file/d/ghijkl0987654321/view") {
		t.Errorf("invariant 5 (es): expected scene[1] clip.drive_link URL in Spanish render; not found")
	}

	// ── Invariant 6: "Capítulo" present when Language=es ──
	// Mirror of invariant 4 for the Spanish locale. The "Capítulo"
	// literal must appear in the <h3> per-scene heading when
	// language=es, and "Capitolo" must NOT appear.
	if !strings.Contains(htmlES, "Capítulo") {
		t.Errorf("invariant 6 (es): expected 'Capítulo' chapter label (language=es); not found")
	}
	if strings.Contains(htmlES, "Capitolo") {
		t.Errorf("invariant 6 (es): unexpected 'Capitolo' in Spanish render (language should map to one localized label, not both)")
	}

	// ── Invariant 7: FORBIDDEN — no Italian-translated JSON keys ──
	// The SpecScene JSON block (emitted by includeSpecSceneBlock=true)
	// contains the canonical English JSON keys (ID, Index, Text,
	// Kind, Bindings, Clip, ClipID, DriveLink, etc.). A future
	// regression that accidentally translates these keys to
	// Italian (e.g. via a hostile localization layer applied to
	// the JSON dump) would break the per-text strategy contract
	// from translation.go: the LLM NEVER sees the JSON keys,
	// and the JSON dump must preserve the canonical English
	// shape verbatim. The 5 forbidden substrings ("collegamento",
	// "tipo", "testo", "id_clip", "link_drive") are the canonical
	// Italian translations of "bindings", "kind", "text", "clip_id",
	// "drive_link" — they must NOT appear anywhere in the
	// rendered HTML (the per-text strategy guarantees the
	// renderer only emits canonical English keys).
	forbiddenItalian := []string{
		"collegamento", // Italian for "bindings" (canonical key: "Bindings")
		"tipo",         // Italian for "kind" (canonical key: "Kind")
		"testo",        // Italian for "text" (canonical key: "Text")
		"id_clip",      // Italianized form of "clip_id" (canonical key: "ClipID")
		"link_drive",   // Italianized form of "drive_link" (canonical key: "DriveLink")
	}
	for _, word := range forbiddenItalian {
		if strings.Contains(htmlIT, word) {
			t.Errorf("invariant 7 (it): forbidden Italian word %q found in Italian render (canonical JSON keys must stay English: ID/Index/Text/Kind/Bindings/ClipID/DriveLink)", word)
		}
		if strings.Contains(htmlES, word) {
			t.Errorf("invariant 7 (es): forbidden Italian word %q found in Spanish render (canonical JSON keys must stay English: ID/Index/Text/Kind/Bindings/ClipID/DriveLink)", word)
		}
	}
}
