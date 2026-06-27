// Package scripts — processor_images.go generates AI images for
// each scene. Enabled as "images" in the plan's Postprocessors list.
//
// PR 9 (June 2026): the legacy scene-splitters
// (splitScriptIntoSegments / sceneCountFromPlan) were REMOVED.
// The processor now reads scenes directly from
// engineResult.Output.SpecScene.Scenes — the canonical structured
// output from PR 1, validated by PR 6's ValidateAndEnrichSpecScene.
// This eliminates the pre-V1 paragraph-splitting anti-pattern and
// ensures each generated image maps to a model-defined scene.
//
// Partial failures (one scene fails) are collected — the processor
// does NOT abort on first error. No-op when plan has no ClipEvidence
// or when the model output has zero scenes.
package adapters

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Typed port (PR 9 / PR 3) ─────────────────────────────────────────────

// ImageResult is the per-scene image generation outcome surfaced
// from ImageGenService.SearchAndDownload. The single SourceURL field
// is the public URL of the generated/uploaded asset.
type ImageResult struct {
	SourceURL string
}

// ImageGenService is the canonical port for image generation.
// Production implementations live in internal/application/images/
// (concrete *images.Service); stub implementations live in adapters/.
type ImageGenService interface {
	SearchAndDownload(ctx context.Context, sceneName, sceneText, altText, language string, opts interface{}) (*ImageResult, error)
}

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

func (p *ImageProcessor) Name() string { return "images" }

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
// used.
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
		language = "en"
	}

	images := make([]SceneImage, 0, len(scenes))
	var warnings []string

	for i, scene := range scenes {
		sceneText := scene.Text
		if sceneText == "" {
			sceneText = fmt.Sprintf("Scene %d", i+1)
		}
		sceneName := fmt.Sprintf("scene-%d", i)
		if scene.ID != "" {
			sceneName = scene.ID
		}

		query := scene.Title
		if query == "" {
			query = plan.Topic
		}
		if query == "" {
			query = plan.Title
		}
		if query == "" {
			query = sceneText
		}

		asset, err := p.gen.SearchAndDownload(ctx, sceneName, sceneText, query, language, nil)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("image generation failed for scene %d: %v", i, err))
			images = append(images, SceneImage{Index: i, Text: sceneText})
			continue
		}

		url := ""
		if asset != nil {
			url = asset.SourceURL
		}

		images = append(images, SceneImage{
			Index: i,
			Text:  sceneText,
			URL:   url,
		})
	}

	if len(warnings) > 0 && p.log != nil {
		p.log.Warn("image processor: partial failures",
			zap.Int("total", len(scenes)),
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
