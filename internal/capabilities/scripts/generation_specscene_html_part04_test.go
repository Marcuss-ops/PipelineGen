package scriptgeneration_test

import (
	"encoding/json"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestDocument_Golden_HumanSurfacePlusCompleteSpecScene(t *testing.T) {
	t.Parallel()

	original := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{
				ID:    "scene-0",
				Index: 0,
				Text:  "TESTO SCENA UNO",
				Kind:  scriptpkg.SceneIntro,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{ClipID: "CLIP-A", DriveLink: "CLIP-A-DRIVE"},
					Voiceover: &scriptpkg.VoiceoverBinding{
						Status: "completed",
						Links:  map[string]string{"it": "VOICE-IT-1"},
					},
				},
			},
			{
				ID:    "scene-1",
				Index: 1,
				Text:  "TESTO SCENA DUE",
				Kind:  scriptpkg.SceneNarration,
				Bindings: scriptpkg.SceneBindings{
					Clips: []scriptpkg.ClipBinding{
						{ClipID: "CLIP-B", DriveLink: "CLIP-B-DRIVE"},
						{ClipID: "CLIP-C", DriveLink: "CLIP-C-DRIVE"},
					},
				},
			},
		},
	}

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: original}
	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		Title:           "TITOLO TEST",
		Language:        "it",
		DefaultLanguage: "it",
	})

	human := humanDocumentHTML(t, out)
	for _, want := range []string{
		"TITOLO TEST",
		"<h2>Intro</h2>",
		"TESTO SCENA UNO",
		"<strong>Voiceover:</strong>",
		"VOICE-IT-1",
		"<h2>Scene 2</h2>",
		"TESTO SCENA DUE",
	} {
		require.Contains(t, human, want)
	}

	for _, forbidden := range []string{
		"Description", "Tags",
	} {
		require.NotContains(t, human, forbidden)
	}
	for _, want := range []string{
		"<strong>Clip:</strong>", "CLIP-A-DRIVE", "CLIP-B-DRIVE", "CLIP-C-DRIVE",
	} {
		require.Contains(t, human, want)
	}

	specJSON := extractSpecSceneJSON(t, out)
	for _, want := range []string{
		"scene-0", "scene-1",
		"CLIP-A", "CLIP-A-DRIVE", "CLIP-B", "CLIP-B-DRIVE", "CLIP-C", "CLIP-C-DRIVE",
		"VOICE-IT-1", "intro", "narration",
	} {
		require.Contains(t, specJSON, want)
	}

	var decoded scriptpkg.SpecSceneOutput
	require.NoError(t, json.Unmarshal([]byte(specJSON), &decoded))
	require.Equal(t, original, decoded, "golden SpecScene must round-trip byte-faithfully")
}

func TestDocument_DoesNotRepeatLegacyClipAlias(t *testing.T) {
	t.Parallel()

	const link = "https://drive.google.com/file/d/clip-once/view"
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{{
			ID:   "scene-0",
			Text: "Una clip.",
			Bindings: scriptpkg.SceneBindings{
				Clips: []scriptpkg.ClipBinding{{ClipID: "clip-once", DriveLink: link}},
				Clip:  &scriptpkg.ClipBinding{ClipID: "clip-once", DriveLink: link},
			},
		}},
	}}

	human := humanDocumentHTML(t, mustRender(t, model, scriptgeneration.DocumentRenderOptions{}))
	require.Equal(t, 1, strings.Count(human, `href="`+link+`">`), "legacy Clip alias must not duplicate the same Drive link")
}
