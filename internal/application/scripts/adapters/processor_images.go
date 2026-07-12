// Package adapters — processor_images.go (commit 6, July 2026):
// scene-image generation entry point in its POST-SPLIT SLIM form.
//
// godlike/06 SSOT — file ownership after the commit 6 split:
//
//	processor_images.go              — owns ONLY ImageProcessor type +
//	                                  NewImageProcessor + Name + Policy +
//	                                  Process + specScenesFromInput
//	processor_images_contracts.go    — ImageResult + ImageGenService +
//	                                  imagePrewarmer + smartImageGenService +
//	                                  imageSceneOutcome (internal buffer) +
//	                                  SceneImageOutcome (NEW exported) +
//	                                  SceneImageStatus + default*I constants
//	processor_images_fanout.go       — defaultImageSceneConcurrency +
//	                                  imageFanoutConcurrency +
//	                                  runImageSceneFanout
//	processor_images_scene.go        — resolveSceneQuery +
//	                                  generateSceneImage + fallbackSceneText +
//	                                  cleanPromptForSubject +
//	                                  canonicalSceneImageURL
//
// PR 9 (June 2026): the legacy scene-splitters
// (splitScriptIntoSegments / sceneCountFromPlan) were REMOVED.
// The processor now reads scenes directly from
// engineResult.Output.SpecScene.Scenes — the canonical structured
// output from PR 1, validated by PR 6's ValidateAndEnrichSpecScene.
//
// Partial failures (one scene fails) are collected — the processor
// does NOT abort on first error. No-op when plan has no ClipEvidence
// or when the model output has zero scenes.
package adapters

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"

	"go.uber.org/zap"
)

// ImageProcessor generates scene images via ImageGenService.
// Uses engineResult.Output.SpecScene.Scenes to drive per-scene
// image generation (PR 9 contract).
type ImageProcessor struct {
	gen ImageGenService
	log *zap.Logger
}

// NewImageProcessor creates an ImageProcessor.
// gen must be non-nil (enforced at registration time by wire_script.go).
func NewImageProcessor(gen ImageGenService, log *zap.Logger) *ImageProcessor {
	return &ImageProcessor{gen: gen, log: log}
}

func (p *ImageProcessor) Name() ProcessorName { return ProcessorImages }

// Policy classifies images as ProcessorBestEffort: a missing image
// service (typed adapter nil at composition time) or a runtime failure
// degrades gracefully into a Warning + empty result, not a hard
// failure. Operators who need hard-failure semantics can flip the
// registered policy via a future PR (per PR 2 spec: "images =
// configurabile"). The plan arg is accepted for interface uniformity
// but ignored — images are unconditionally best-effort for now.
func (p *ImageProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

// Process generates per-scene images. PR 9 contract: scenes come
// directly from engineResult.Output.SpecScene.Scenes (validated by
// ValidateAndEnrichSpecScene); no paragraph-splitting helper is
// used. Commit 6 split: the per-scene dispatch is delegated to
// runImageSceneFanout (processor_images_fanout.go); the per-scene
// helpers (resolveSceneQuery, generateSceneImage, fallbackSceneText,
// cleanPromptForSubject, canonicalSceneImageURL) live in
// processor_images_scene.go.
func (p *ImageProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.gen == nil {
		return nil, fmt.Errorf("%w: image processor: ImageGenService not configured", scriptpkg.ErrPostprocessFailed)
	}

	scenes := specScenesFromInput(input)
	if len(scenes) == 0 {
		if p.log != nil {
			p.log.Debug("image processor: no scenes to render (no specscene scenes)",
				zap.String("item_id", plan.ID))
		}
		return &PostProcessResult{}, nil
	}

	if input.Text == "" {
		return &PostProcessResult{}, nil
	}

	language := plan.Language
	if language == "" {
		language = defaults.DefaultScriptConfig().DefaultLanguage
	}

	if prewarmer, ok := p.gen.(imagePrewarmer); ok {
		prewarmCount := imageFanoutConcurrency(len(scenes))
		prewarmer.TriggerPrewarm(ctx, plan.ID, prewarmCount)
	}

	outcomes := runImageSceneFanout(ctx, p.gen, plan, scenes, language)
	images := make([]SceneImage, 0, len(outcomes))
	var warnings []string
	for _, out := range outcomes {
		images = append(images, out.image)
		if out.warning != "" {
			warnings = append(warnings, out.warning)
		}
	}

	if len(warnings) > 0 && p.log != nil {
		p.log.Warn("image processor: partial failures",
			zap.Int("total", len(outcomes)),
			zap.Int("failed", len(warnings)),
			zap.Int("succeeded", len(images)-len(warnings)),
			zap.Strings("warnings", warnings))
	}

	return &PostProcessResult{SceneImages: images}, nil
}

// specScenesFromInput returns the canonical scene list from the
// ProcessInput envelope (typed MSOV1 output). PR 9 contract: post-
// processors consume scenes through this lens; any attempt to
// re-derive scenes via text-splitting is a regression caught by
// ci-architectural-checks Check 15.
func specScenesFromInput(input ProcessInput) []scriptpkg.SpecScene {
	if len(input.SpecScene.Scenes) == 0 {
		return nil
	}
	return input.SpecScene.Scenes
}
