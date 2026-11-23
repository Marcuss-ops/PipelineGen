package script

import (
	"context"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func TestSceneTextGeneratorBuildPlanResolvesPlanOnlyMediaRequirements(t *testing.T) {
	generator := &SceneTextGenerator{}
	req := scriptgen.GenerateRequest{
		Source: scriptgen.Source{
			Type:       scriptgen.SourceText,
			Topic:      "Corrado Augias",
			SourceText: "Corrado Augias racconta la sua infanzia in Libia.",
		},
		MediaMode:           scriptpkg.MediaModeMixed,
		GenerateSceneImages: scriptpkg.ToggleDisabled,
		ScriptParams:        scriptpkg.ScriptSpec{ImagesPerScene: 2},
		OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
			scriptpkg.OverlayCategoryImages: scriptpkg.ToggleDisabled,
		},
		MediaPlan: mediadomain.MediaPlanSpec{
			Extraction: mediadomain.MediaExtractionPolicy{
				Enabled: true,
				Include: []string{mediadomain.ExtractionIncludeEntities},
			},
		},
	}

	plan, err := generator.buildPlan(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.NotNil(t, plan.MediaRequirements)
	require.False(t, plan.MediaRequirements.SceneImages)
	require.Equal(t, 0, plan.MediaRequirements.ImageFanout)
	require.Equal(t, scriptpkg.MediaRequirementDisabled, plan.MediaRequirements.EntityImages)
	require.False(t, plan.RequestsImageAcquisition())

	spec := BuildRuntimeMediaCertSpec(plan)
	require.Equal(t, 0, spec.ImagesPerSegment, "the live plan must certify zero image fanout")
}
