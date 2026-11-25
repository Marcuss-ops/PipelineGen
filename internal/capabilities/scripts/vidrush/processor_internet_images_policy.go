package vidrush

import (
	"fmt"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func internetImageOptions(plan *scriptpkg.ResolvedGenerationPlan) internetImageProcessOptions {
	return internetImageProcessOptions{
		cacheOnly:           plan.MediaPlan.Mode == mediadomain.MediaPlanModeCacheOnly,
		entityImagesEnabled: entityImageSurfaceEnabled(plan),
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

// internetImagePerQueryLimit divides the scene candidate budget over its
// entity queries. CandidateLimit is a segment budget; applying it separately
// to every entity multiplied a 30-candidate request into hundreds of remote
// downloads and visual checks.
func internetImagePerQueryLimit(segmentLimit, queryCount int) int {
	if segmentLimit <= 0 {
		segmentLimit = 10
	}
	if queryCount <= 1 {
		return min(segmentLimit, 50)
	}
	limit := (segmentLimit + queryCount - 1) / queryCount
	return max(1, min(limit, 50))
}

func internetImageCacheReadable(plan *scriptpkg.ResolvedGenerationPlan, options internetImageProcessOptions) bool {
	return options.cacheOnly || (!plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh)
}

func internetImageCacheMissWarning(segmentID string) string {
	return fmt.Sprintf("internet_images: cache-only miss for segment %s", segmentID)
}
