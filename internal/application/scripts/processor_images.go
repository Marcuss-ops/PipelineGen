// Package scripts — processor_images.go generates scene images
// for the script. Enabled as "images" in the plan's Postprocessors
// list.
//
// NOTE: The real ScenesService implementation was deleted. This
// processor is a forward-compatible placeholder that returns
// ErrPostprocessFailed when the service is not yet wired. When
// the scene pipeline is re-constituted, wire the real
// ScenesService here.
package scripts

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ImagesProcessor generates scene images. Currently a real-failure
// placeholder — returns ErrPostprocessFailed until the ScenesService
// is re-constituted.
type ImagesProcessor struct {
	scenesSvc *ScenesService
}

// NewImagesProcessor creates an ImagesProcessor.
// scenesSvc may be nil (placeholder failure mode).
func NewImagesProcessor(scenesSvc *ScenesService) *ImagesProcessor {
	return &ImagesProcessor{scenesSvc: scenesSvc}
}

func (p *ImagesProcessor) Name() string { return "images" }

func (p *ImagesProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, script string) (*PostProcessResult, error) {
	if p.scenesSvc == nil {
		return nil, fmt.Errorf("%w: images processor: ScenesService not yet re-constituted (placeholder)", scriptpkg.ErrPostprocessFailed)
	}
	if script == "" {
		return &PostProcessResult{}, nil
	}

	// Build a legacy spec for the service call.
	spec := legacySpecFromPlan(*plan)
	spec.GenerateSceneImages = true

	_ = spec // TODO: call p.scenesSvc.GenerateSceneImages(ctx, spec, script)
	_ = ctx

	// Placeholder: return error until real implementation is restored.
	return nil, fmt.Errorf("%w: images processor: GenerateSceneImages not yet implemented (pending ScenesService restoration)", scriptpkg.ErrPostprocessFailed)
}
