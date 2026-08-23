package scriptgeneration_test

import (
	"html"
	"strings"
	"testing"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"github.com/stretchr/testify/require"
)

func mustRender(t *testing.T, model *scriptpkg.ModelScriptOutputV1, opts scriptgeneration.DocumentRenderOptions) string {
	t.Helper()
	out, err := scriptgeneration.RenderDocument(model, opts)
	require.NoError(t, err)
	return out
}

func humanDocumentHTML(t *testing.T, output string) string {
	t.Helper()

	const marker = "<h2>SpecScene JSON</h2>"
	index := strings.Index(output, marker)
	if index < 0 {
		t.Fatal("SpecScene JSON marker missing")
	}
	return output[:index]
}

// extractSpecSceneJSON isolates the embedded SpecScene JSON snapshot from a
// rendered document body and unescapes it so it can be re-parsed and compared
// byte-faithfully against the canonical wire representation.
func extractSpecSceneJSON(t *testing.T, output string) string {
	t.Helper()

	const startMarker = "<h2>SpecScene JSON</h2><pre><code>"
	start := strings.Index(output, startMarker)
	require.NotEqual(t, -1, start, "SpecScene JSON marker missing")
	start += len(startMarker)

	end := strings.Index(output[start:], "</code></pre>")
	require.NotEqual(t, -1, end, "SpecScene JSON closing marker missing")

	return html.UnescapeString(output[start : start+end])
}

// complexSpecSceneFixture builds a rich, multi-scene SpecScene covering clip,
// multi-clip, voiceover, stock, image, and entity annotations. It is used by
// the round-trip and no-mutation tests to prove the embedded JSON snapshot
// preserves the complete canonical object.
func complexSpecSceneFixture() scriptpkg.SpecSceneOutput {
	return scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{
				ID:        "scene-0",
				SegmentID: "segment-0",
				Index:     0,
				Text:      "Scena introduttiva completa.",
				Title:     "Intro",
				Kind:      scriptpkg.SceneIntro,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID:         "clip-a",
						ClipTitle:      "Clip A",
						DriveLink:      "https://drive.google.com/file/d/clip-a/view",
						SubtitleLink:   "https://drive.google.com/file/d/sub-a/view",
						SubtitleFileID: "sub-a",
						StartMs:        1000,
						EndMs:          5000,
						DurationMs:     4000,
					},
					Voiceover: &scriptpkg.VoiceoverBinding{
						Status:     "completed",
						Link:       "https://drive.google.com/file/d/voice-legacy/view",
						Links:      map[string]string{"it": "https://drive.google.com/file/d/voice-it/view", "en": "https://drive.google.com/file/d/voice-en/view"},
						LocalPath:  "/tmp/voice.mp3",
						DurationMs: 4200,
					},
					Stock: &scriptpkg.StockBinding{
						AssetID:    "stock-1",
						Name:       "Stock One",
						Source:     "stock",
						DriveLink:  "https://drive.google.com/file/d/stock-1/view",
						FolderID:   "folder-1",
						FolderLink: "https://drive.google.com/drive/folders/folder-1",
						Score:      0.5,
						StartMs:    0,
						EndMs:      5000,
						DurationMs: 5000,
					},
					Image: &scriptpkg.ImageBinding{
						ImageID:   "img-1",
						Prompt:    "intro image",
						URL:       "https://img.example.com/intro.png",
						LocalPath: "/tmp/intro.png",
						Status:    "generated",
					},
				},
				Annotations: &scriptpkg.SceneAnnotations{
					Version:  1,
					Language: "it",
					PrimaryEntities: []scriptpkg.AnnotatedEntity{
						{ID: "e1", Text: "Jackie Chan", CanonicalName: "Jackie Chan", Type: "person", Confidence: 0.99},
					},
					Status: "completed",
				},
			},
			{
				ID:    "scene-1",
				Index: 1,
				Text:  "Scena con multi-clip.",
				Kind:  scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clips: []scriptpkg.ClipBinding{
						{ClipID: "clip-b", DriveLink: "https://drive.google.com/file/d/clip-b/view"},
						{ClipID: "clip-c", DriveLink: "https://drive.google.com/file/d/clip-c/view"},
					},
				},
			},
		},
	}
}
