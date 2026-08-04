// Package adapters contains canonical document-rendering invariants for translated SpecScene input.
package adapters

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func canonicalTranslatedDocumentFixture() *scriptpkg.ModelScriptOutputV1 {
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Translated script prose.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-1",
					Index: 0,
					Text:  "Prima scena tradotta.",
					Kind:  scriptpkg.SceneClip,
					Bindings: scriptpkg.SceneBindings{
						Clip: &scriptpkg.ClipBinding{
							ClipID:    "clip-1",
							ClipTitle: "Clip tradotta",
							DriveLink: "https://drive.google.com/file/d/clip-1/view",
						},
					},
				},
			},
		},
	}
}

// TestTranslatedSpecScene_UsesCanonicalDocumentRenderer preserves the
// translated document invariants without depending on the removed legacy
// full-prose renderer: visible scene content and links must agree with the
// canonical SpecScene JSON snapshot, whose keys remain untranslated.
func TestTranslatedSpecScene_UsesCanonicalDocumentRenderer(t *testing.T) {
	t.Parallel()

	html := BuildSpecSceneDocumentHTML(
		canonicalTranslatedDocumentFixture(),
		"Translated Script",
		nil,
	)

	for _, want := range []string{
		"<h1>Translated Script</h1>",
		"<h2>Scenes</h2>",
		"<h2>SpecScene JSON</h2><pre>",
		"Prima scena tradotta.",
		"https://drive.google.com/file/d/clip-1/view",
		"&#34;clip_id&#34;",
		"&#34;drive_link&#34;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected canonical translated document to contain %q; HTML=%s", want, html)
		}
	}

	preStart := strings.Index(html, "<h2>SpecScene JSON</h2><pre>")
	if preStart < 0 {
		t.Fatalf("canonical document has no SpecScene JSON block")
	}
	preEnd := strings.Index(html[preStart:], "</pre>")
	if preEnd < 0 {
		t.Fatalf("canonical SpecScene JSON block is not closed")
	}
	preBlock := html[preStart : preStart+preEnd]
	for _, forbidden := range []string{
		"collegamenti", "tipo", "testo", "identificatore_clip",
		"collegamento_drive", "&#34;id_clip&#34;", "&#34;id_drive&#34;",
	} {
		if strings.Contains(preBlock, forbidden) {
			t.Errorf("SpecScene JSON contains translated key %q; JSON keys must remain canonical", forbidden)
		}
	}
}
