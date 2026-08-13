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
// translated document invariants and includes the canonical SpecScene JSON
// snapshot with untranslated machine keys.
func TestTranslatedSpecScene_UsesCanonicalDocumentRenderer(t *testing.T) {
	t.Parallel()

	html := BuildSpecSceneDocumentHTML(
		canonicalTranslatedDocumentFixture(),
		SpecSceneDocumentOptions{Title: "Translated Script"},
	)

	for _, want := range []string{
		"<h1>Translated Script</h1>",
		"<h2>Scenes</h2>",
		"<h2>SpecScene JSON</h2><pre><code>",
		"Prima scena tradotta.",
		"https://drive.google.com/file/d/clip-1/view",
		"&#34;clip_id&#34;",
		"&#34;drive_link&#34;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected canonical translated document to contain %q; HTML=%s", want, html)
		}
	}

	preStart := strings.Index(html, "<h2>SpecScene JSON</h2><pre><code>")
	if preStart < 0 {
		t.Fatalf("canonical document has no SpecScene JSON block")
	}
	if !strings.Contains(html[preStart:], "&#34;clip_id&#34;") {
		t.Fatalf("SpecScene JSON block lost canonical clip_id key: %s", html)
	}
}
