package adapters

import (
	"fmt"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func internetImageOptions(plan *scriptpkg.ResolvedGenerationPlan) internetImageProcessOptions {
	return internetImageProcessOptions{
		cacheOnly:           plan.MediaPlan.Mode == mediadomain.MediaPlanModeCacheOnly,
		entityImagesEnabled: plan.MediaPlan.Extraction.EntityImages.Enabled,
	}
}

func internetImageCandidateLimit(plan *scriptpkg.ResolvedGenerationPlan) int {
	limit := 10
	if plan.MediaPlan.Planner.CandidateLimit > 0 {
		limit = plan.MediaPlan.Planner.CandidateLimit
	}
	if limit > 50 {
		return 50
	}
	return limit
}

func internetImageCacheReadable(plan *scriptpkg.ResolvedGenerationPlan, options internetImageProcessOptions) bool {
	return options.cacheOnly || (!plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh)
}

func internetImageCacheMissWarning(segmentID string) string {
	return fmt.Sprintf("internet_images: cache-only miss for segment %s", segmentID)
}
