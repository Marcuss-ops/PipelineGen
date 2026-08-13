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

	marker := "<h2>SpecScene JSON</h2>"
	markerIdx := strings.Index(html, marker)
	if markerIdx < 0 {
		t.Fatalf("canonical document has no SpecScene JSON block")
	}
	human := html[:markerIdx]
	jsonPart := html[markerIdx:]

	for _, want := range []string{
		"<h1>Translated Script</h1>",
		"<h2>Scene 1</h2>",
		"Prima scena tradotta.",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("expected human document section to contain %q; human=%s", want, human)
		}
	}

	if strings.Contains(human, "https://drive.google.com/file/d/clip-1/view") {
		t.Errorf("clip drive link leaked into the human document section: %s", human)
	}

	for _, want := range []string{
		"<h2>SpecScene JSON</h2><pre><code>",
		"https://drive.google.com/file/d/clip-1/view",
		"&#34;clip_id&#34;",
		"&#34;drive_link&#34;",
	} {
		if !strings.Contains(jsonPart, want) {
			t.Errorf("expected SpecScene JSON to contain %q; JSON=%s", want, jsonPart)
		}
	}
}
