package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestVidRushSceneMerger_AppliesAnnotationsWithoutMutation(t *testing.T) {
	merger := NewVidRushSceneMerger("it")
	scene := scriptpkg.SpecScene{ID: "scene-0", Index: 0, Text: "Mike Tyson mostrava una potenza esplosiva."}
	result := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-0",
		SceneID:   "scene-0",
		Position:  0,
		Text:      scene.Text,
		Insights: scriptpkg.SegmentInsights{
			SegmentID: "scene-0",
			TextHash:  segmentTextHash(scene.Text),
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "Mike Tyson", Type: "PERSON", Confidence: 0.9},
			},
			ImportantPhrases: []string{scene.Text},
		},
	}

	out := merger.Merge(scene, result)

	// Input scene annotations must remain nil (no mutation).
	assert.Nil(t, scene.Annotations, "input scene must not be mutated")

	require.NotNil(t, out.Annotations)
	assert.Equal(t, "it", out.Annotations.Language)
	require.NotEmpty(t, out.Annotations.PrimaryEntities, "primary entities must be projected from the segment")
	assert.Equal(t, "Mike Tyson", out.Annotations.PrimaryEntities[0].Text)
	assert.NotEmpty(t, out.Annotations.ImportantPhrases, "important phrases must be projected from the segment")
}

func TestVidRushSceneMerger_NilMergerReturnsSceneUnchanged(t *testing.T) {
	var merger *VidRushSceneMerger
	scene := scriptpkg.SpecScene{ID: "scene-0", Index: 0, Text: "Text"}
	result := scriptpkg.VidRushSegmentResult{SceneID: "scene-0"}

	out := merger.Merge(scene, result)
	assert.Equal(t, scene, out)
	assert.Nil(t, out.Annotations)
}
