package wiring

import (
	"testing"

	scriptwiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/script"
	media "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildRuntimeMediaCertSpecUsesResolvedFanoutNotPlannerBudget(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaMode:      scriptpkg.MediaModeMixed,
		ImagesPerScene: 2,
		MediaRequirements: &scriptpkg.MediaRequirements{
			SceneImages:  true,
			EntityImages: scriptpkg.MediaRequirementDisabled,
			ImageFanout:  3,
		},
	}
	spec := scriptwiring.BuildRuntimeMediaCertSpec(plan)
	if spec.ImagesPerSegment != 3 {
		t.Fatalf("images_per_segment = %d, want the resolved contract 3, not ImagesPerScene=2", spec.ImagesPerSegment)
	}
}

func TestBuildRuntimeMediaCertSpecPlanOnlySkipsImageFanout(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaMode:      scriptpkg.MediaModeMixed,
		ImagesPerScene: 2,
		MediaRequirements: &scriptpkg.MediaRequirements{
			SceneImages:  false,
			EntityImages: scriptpkg.MediaRequirementDisabled,
			ImageFanout:  0,
		},
		MediaPlan: media.MediaPlanSpec{
			Mode:       media.MediaPlanModeHybrid,
			Extraction: media.MediaExtractionPolicy{Enabled: true, Include: []string{"entities"}},
		},
	}
	spec := scriptwiring.BuildRuntimeMediaCertSpec(plan)
	if spec.ImagesPerSegment != 0 {
		t.Fatalf("images_per_segment = %d, want 0 for a plan-only contract", spec.ImagesPerSegment)
	}
}
