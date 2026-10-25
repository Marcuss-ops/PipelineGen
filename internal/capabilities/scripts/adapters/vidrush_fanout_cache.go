package adapters

import (
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func vidRushFanoutImageCacheKey(plan *vidRushFanoutPlan, generation *scriptpkg.ResolvedGenerationPlan) string {
	return segmentCacheKey("internet-images-assets-v5", plan.segmentID, plan.textHash, generation.Language, generation.Model, generation.PromptVersion, fmt.Sprintf("%d", plan.perQueryLimit))
}
