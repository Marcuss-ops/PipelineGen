package adapters

import (
	"fmt"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func (p *VidRushMaterializationProcessor) metadataOnlyResult(plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, bool) {
	if plan == nil || plan.MediaPlan.Materialization.Mode != mediadomain.MaterializationMetadataOnly {
		return nil, false
	}
	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input.VidRushSegments))
	for _, segment := range input.VidRushSegments {
		segments = append(segments, cloneVidRushSegmentResult(segment))
	}
	return &PostProcessResult{VidRushSegments: segments, Changed: len(segments) > 0}, true
}

func materializationDependenciesError(plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, providers *VidRushAssetProviderRegistry, finalizer scriptports.VidRushArtifactFinalizer) error {
	if providers != nil && finalizer != nil {
		return nil
	}
	if vidRushMaterializationRequested(plan, input) {
		return fmt.Errorf("vidrush materialization: provider registry and common finalizer are required")
	}
	return nil
}
