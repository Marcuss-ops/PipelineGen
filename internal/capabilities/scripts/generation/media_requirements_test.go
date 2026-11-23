package generation

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func planOnlyAugiasItem() scriptpkg.GenerationItemV2 {
	return scriptpkg.GenerationItemV2{
		ID:        "r41",
		MediaMode: scriptpkg.MediaModeMixed,
		Source:    scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "Corrado Augias"},
		ScriptParams: scriptpkg.ScriptSpec{
			ImagesPerScene: 2,
		},
		OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
			scriptpkg.OverlayCategoryImages: scriptpkg.ToggleDisabled,
		},
		Output: scriptpkg.OutputSpec{
			ExtractEntities:     scriptpkg.ToggleEnabled,
			GenerateSceneImages: scriptpkg.ToggleDisabled,
		},
		MediaPlan: mediadomain.MediaPlanSpec{
			Mode: mediadomain.MediaPlanModeHybrid,
			Extraction: mediadomain.MediaExtractionPolicy{
				Enabled: true,
				Include: []string{mediadomain.ExtractionIncludeEntities},
			},
		},
	}
}

func TestBuildPlanPlanOnlyHasNoImageRequirement(t *testing.T) {
	plan := BuildPlan(planOnlyAugiasItem())
	require.NotNil(t, plan.MediaRequirements)
	require.False(t, plan.MediaRequirements.SceneImages)
	require.Equal(t, 0, plan.MediaRequirements.ImageFanout)
	require.Equal(t, scriptpkg.MediaRequirementDisabled, plan.MediaRequirements.EntityImages)
	require.NotContains(t, plan.Postprocessors, string(adapters.ProcessorInternetImages))
	require.NotContains(t, plan.Postprocessors, string(adapters.ProcessorVidRushMaterialization))
	require.Contains(t, plan.Postprocessors, string(adapters.ProcessorClipSearch))
}

func TestBuildPlanFullVideoKeepsImageFanout(t *testing.T) {
	item := planOnlyAugiasItem()
	item.OverlayCategories = nil
	item.Output.GenerateSceneImages = scriptpkg.ToggleEnabled
	plan := BuildPlan(item)
	require.NotNil(t, plan.MediaRequirements)
	require.True(t, plan.MediaRequirements.SceneImages)
	require.Equal(t, 2, plan.MediaRequirements.ImageFanout)
	require.Contains(t, plan.Postprocessors, string(adapters.ProcessorInternetImages))
	require.Contains(t, plan.Postprocessors, string(adapters.ProcessorVidRushMaterialization))
}

func TestBuildPlanEntityImagesOnlyKeepsMaterialization(t *testing.T) {
	item := planOnlyAugiasItem()
	item.MediaPlan.Extraction.EntityImages = mediadomain.EntityImagePolicy{Enabled: true}
	plan := BuildPlan(item)
	require.NotNil(t, plan.MediaRequirements)
	require.False(t, plan.MediaRequirements.SceneImages)
	require.Equal(t, 0, plan.MediaRequirements.ImageFanout)
	require.Equal(t, scriptpkg.MediaRequirementOptional, plan.MediaRequirements.EntityImages)
	require.Contains(t, plan.Postprocessors, string(adapters.ProcessorInternetImages))
	require.Contains(t, plan.Postprocessors, string(adapters.ProcessorVidRushMaterialization))
}

func TestBuildPlanEntityImagesRequiredIsPreserved(t *testing.T) {
	item := planOnlyAugiasItem()
	item.MediaPlan.Extraction.EntityImages = mediadomain.EntityImagePolicy{Enabled: true, Required: true}
	plan := BuildPlan(item)
	require.Equal(t, scriptpkg.MediaRequirementRequired, plan.MediaRequirements.EntityImages)
}
