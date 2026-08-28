package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelScriptOutputForDocumentProjectsLatestRenderedLink(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{{
			ID:   "scene-1",
			Clip: &ClipReference{ID: "source-1", DriveLink: "https://drive.google.com/source"},
		}},
		LocalizedRenders: []LocalizedRenderResult{
			{ClipID: "source-1", DriveLink: "https://drive.google.com/render-old"},
			{ClipID: "source-1", DriveLink: "https://drive.google.com/render-new"},
		},
	}

	model := modelScriptOutputForDocument(result, "en")
	require.Equal(t, "source-1", model.SpecScene.Scenes[0].Bindings.Clip.ClipID)
	require.Equal(t, "https://drive.google.com/render-new", model.SpecScene.Scenes[0].Bindings.Clip.DriveLink)
	// The source result remains the source-oriented payload; only the document
	// projection is late-bound to the certified output.
	require.Equal(t, "https://drive.google.com/source", result.Scenes[0].Clip.DriveLink)
}
