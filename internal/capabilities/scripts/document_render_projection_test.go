package scriptgeneration

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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

func TestModelScriptOutputForDocumentProjectsSegmentAnnotationsWhenScenePointerIsMissing(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{{
			ID: "scene-0", Index: 0,
			Text: map[Language]string{"en": "Ada Lovelace pioneered computing."},
		}},
		Segments: []scriptpkg.VidRushSegmentResult{{
			SceneID: "scene-0", Position: 0,
			Insights: scriptpkg.SegmentInsights{
				Entities:         []scriptpkg.ExtractedEntity{{Value: "Ada Lovelace", Type: "PERSON", Confidence: 1}},
				ImportantPhrases: []string{"Ada Lovelace"},
			},
		}},
	}

	model := modelScriptOutputForDocument(result, "en")
	require.NotNil(t, model.SpecScene.Scenes[0].Annotations)
	require.Len(t, model.SpecScene.Scenes[0].Annotations.PrimaryEntities, 1)
	require.Equal(t, "Ada Lovelace", model.SpecScene.Scenes[0].Annotations.PrimaryEntities[0].CanonicalName)
	require.Len(t, model.SpecScene.Scenes[0].Annotations.ImportantPhrases, 1)
}
