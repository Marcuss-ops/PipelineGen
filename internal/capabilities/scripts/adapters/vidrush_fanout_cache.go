package adapters

import (
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func vidRushFanoutArtlistCacheKey(plan *vidRushFanoutPlan, generation *scriptpkg.ResolvedGenerationPlan) string {
	return artlistSegmentCacheKey(plan.segmentID, plan.textHash, plan.artlistIntentHash, generation.Language, generation.Model, generation.PromptVersion)
}

func vidRushFanoutImageCacheKey(plan *vidRushFanoutPlan, generation *scriptpkg.ResolvedGenerationPlan) string {
	return segmentCacheKey("internet-images-assets-v3", plan.segmentID, plan.textHash, generation.Language, generation.Model, generation.PromptVersion, fmt.Sprintf("%d", plan.perQueryLimit))
}
